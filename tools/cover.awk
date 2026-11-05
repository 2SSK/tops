# Coverage gate over a go test -coverprofile file: fails below 80% overall, or below 90%
# for the packages that decide what tops may do to a cluster (TRD "Test strategy").
# Each line after the first is: file:start,end statements hits.

NR > 1 {
  pkg = $1
  sub(/\/[^\/]*$/, "",pkg)
  statements[pkg] += $2
  total += $2
  if ($3 > 0) {
    covered[pkg] += $2
    hit += $2
  }
}

function percent(part, whole) {
  return whole ? 100 * part / whole : 0
}

END {
  for (pkg in statements){
    p = percent(covered[pkg], statements[pkg])    
    if (pkg ~ /\/internal\/(safety|plan|sizing)$/ && p < 90) {
      printf "FAIL %s: %.1f%% of statements, want 90%%\n", pkg, p
      failed = 1
    }
  }
  p = percent(hit, total)
  if (p < 80) {
    printf "FAIL total: %.1f%% of statements, want 80%%\n", p
    failed = 1
  } else {
      printf "coverage: %.1f%% of statements\n", p
  }
  exit failed
}

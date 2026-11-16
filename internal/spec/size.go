package spec

import (
	"fmt"
	"math"
	"strconv"
)

// sizeUnits are the binary units a size may end with: 512M is 512 MiB.
var sizeUnits = map[byte]int64{'K': 1 << 10, 'M': 1 << 20, 'G': 1 << 30, 'T': 1 << 40}

// ParseSize parses a memory size with a binary unit, like 512M or 26, into a bytes. Plain
// numbers are refused: 512 without a unit is almost always a type for 512M.
func ParseSize(s string) (int64, error) {
	bad := fmt.Errorf("%q is not a size like 512M or 2G", s)
	if len(s) < 2 {
		return 0, bad
	}
	unit, ok := sizeUnits[s[len(s)-1]]
	if !ok {
		return 0, bad
	}
	n, err := strconv.ParseInt(s[:len(s)-1], 10, 64)
	if err != nil || n <= 0 || n > math.MaxInt64/unit {
		return 0, bad
	}
	return n * unit, nil
}

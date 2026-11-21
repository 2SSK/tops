package sizing

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// The measure file: raw samples from VPSLAB (backend schema "Sizing").
type measureFile struct {
	Temporal string `json:"temporal"`
	Runs     []struct {
		Node    string   `json:"node"`
		CapCpus float64  `json:"capCpus"`
		Roles   []string `json:"roles"`
		Phase   string   `json:"phase"`
		Samples []sample `json:"samples"`
	} `json:"runs"`
	S3FitVps03 *struct {
		CapMemoryBytes int64    `json:"capMemoryBytes"`
		Roles          []string `json:"roles"`
		FreeBytes      int64    `json:"freeBytes"`
		Fits           bool     `json:"fits"`
	} `json:"s3FitVps03"`
	PerShard []json.RawMessage `json:"perShard"`
}

// sample is one line of the sampler: a cgroup reading (1 s) or a role's metrics (10 s).
type sample struct {
	T          string   `json:"t"`
	Unit       string   `json:"unit"` // "node" or "temporal@<role>"
	MemCurrent *int64   `json:"memoryCurrentBytes"`
	CPUUsec    *int64   `json:"cpuUsageUsec"`
	HeapInuse  *float64 `json:"memHeapInuseBytes"`
	SQLOpen    *float64 `json:"sqlOpen"`
	SQLVisOpen *float64 `json:"sqlVisOpen"`
}

func loadMeasure(t *testing.T) measureFile {
	t.Helper()
	files, err := filepath.Glob("testdata/measure-*.json")
	if err != nil || len(files) != 1 {
		t.Fatalf("want one testdata/measure-*.json, found %v (%v)", files, err)
	}
	data, err := os.ReadFile(files[0])
	if err != nil {
		t.Fatal(err)
	}
	var m measureFile
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

// derived is every constant the measure file decides, by the rules in TRD "Constants and how
// they are measured" and internal/sizing/testdata/README.md.
type derived struct {
	osBase                int64
	floor, peak           map[string]int64
	share, burst, nonHeap map[string]float64
	cpu, conn, visConn    map[string]float64
	floorSum              int64 // of the four roles
}

func derive(m measureFile) derived {
	d := derived{floor: map[string]int64{}, peak: map[string]int64{}, share: map[string]float64{}, burst: map[string]float64{},
		nonHeap: map[string]float64{}, cpu: map[string]float64{}, conn: map[string]float64{}, visConn: map[string]float64{}}
	for _, run := range m.Runs {
		switch run.Phase {
		case "idle":
			d.idle(run.Roles, run.Samples)
		case "load":
			d.load(run.Roles, run.CapCpus, run.Samples)
		}
	}
	d.finish()
	return d
}

// idle reads osBase (the node's memory.current minus its Temporal units, worst second) and
// each role's floor.
func (d *derived) idle(roles []string, samples []sample) {
	byT := map[string]map[string]int64{}
	for _, s := range samples {
		if s.MemCurrent == nil {
			continue
		}
		if byT[s.T] == nil {
			byT[s.T] = map[string]int64{}
		}
		byT[s.T][s.Unit] = *s.MemCurrent
	}
	for _, units := range byT {
		node, ok := units["node"]
		if !ok || len(units) < 2 {
			continue
		}
		for u, v := range units {
			if u != "node" {
				node -= v
			}
		}
		d.osBase = max(d.osBase, node)
	}
	for _, r := range roles {
		d.floor[r] = max(d.floor[r], slices.Max(memSeries(samples, "temporal@"+r)))
	}
}

// load reads each role's peak and burst (on a node with all four roles), CPU, non-heap and
// connections.
func (d *derived) load(roles []string, cpus float64, samples []sample) {
	at := map[[2]string]int64{}
	for _, s := range samples {
		if s.MemCurrent != nil {
			at[[2]string{s.Unit, s.T}] = *s.MemCurrent
		}
	}
	for _, r := range roles {
		u := "temporal@" + r
		if len(roles) == 4 {
			v := memSeries(samples, u)
			d.peak[r] = max(d.peak[r], slices.Max(v))
			for i := range v { // the largest rise within any 10 s window
				d.burst[r] = max(d.burst[r], float64(slices.Max(v[i:min(i+11, len(v))])-v[i]))
			}
		}
		d.cpu[r] = max(d.cpu[r], cpuSeconds(samples, u))
		nh, open, vis := metrics(samples, u, at)
		d.nonHeap[r] = max(d.nonHeap[r], nh)
		d.conn[r] = max(d.conn[r], open/cpus*1.5)
		d.visConn[r] = max(d.visConn[r], vis/cpus*1.5)
	}
}

// finish normalises the shares and rounds every value once: memory up to a MiB, fractions to
// three places, CPU seconds and connections to one.
func (d *derived) finish() {
	gain, total := map[string]float64{}, 0.0
	for r, p := range d.peak {
		gain[r] = float64(max(p-d.floor[r], 0))
		total += gain[r]
	}
	for r := range d.peak {
		d.share[r] = round(gain[r]/total, 3)
		d.burst[r] = round(d.burst[r]/float64(d.peak[r]), 3)
	}
	d.osBase = ceilMiB(d.osBase)
	for r := range d.floor {
		d.floor[r] = ceilMiB(d.floor[r])
		d.floorSum += d.floor[r]
	}
	for r := range d.cpu {
		d.cpu[r], d.nonHeap[r] = round(d.cpu[r], 1), round(d.nonHeap[r], 3)
		d.conn[r], d.visConn[r] = round(d.conn[r], 1), round(d.visConn[r], 1)
	}
}

// memSeries is a unit's memory.current, one per second.
func memSeries(samples []sample, unit string) []int64 {
	var v []int64
	for _, s := range samples {
		if s.Unit == unit && s.MemCurrent != nil {
			v = append(v, *s.MemCurrent)
		}
	}
	return v
}

// cpuSeconds is a unit's CPU time over the run.
func cpuSeconds(samples []sample, unit string) float64 {
	var first, last int64 = -1, 0
	for _, s := range samples {
		if s.Unit == unit && s.CPUUsec != nil {
			if first < 0 {
				first = *s.CPUUsec
			}
			last = *s.CPUUsec
		}
	}
	return float64(last-first) / 1e6
}

// metrics returns a role's median non-heap fraction at load (memory.current less the Go heap in
// use, of memory.current, at the seconds its metrics were read) and its peak open connections.
func metrics(samples []sample, unit string, mem map[[2]string]int64) (nonHeap, open, vis float64) {
	var fractions []float64
	for _, s := range samples {
		if s.Unit != unit || s.HeapInuse == nil {
			continue
		}
		if m, ok := mem[[2]string{unit, s.T}]; ok && m > 0 {
			fractions = append(fractions, (float64(m)-*s.HeapInuse)/float64(m))
		}
		open, vis = max(open, *s.SQLOpen), max(vis, *s.SQLVisOpen)
	}
	slices.Sort(fractions)
	return fractions[len(fractions)/2], open, vis
}

func ceilMiB(b int64) int64 { return int64(math.Ceil(float64(b)/mib)) * mib }
func round(x float64, places int) float64 {
	p := math.Pow(10, float64(places))
	return math.Round(x*p) / p
}

// TestConstantsFromMeasurements: the 1.31 constants are what the measure file's samples give
// by the measuring rules, so a constant cannot drift from its evidence.
func TestConstantsFromMeasurements(t *testing.T) {
	m := loadMeasure(t)
	d := derive(m)
	c, err := For("1.31")
	if err != nil {
		t.Fatal(err)
	}
	if c.MeasuredAt == "" || m.Temporal != "1.31.3" {
		t.Fatalf("constants for 1.31 are not the measured ones (MeasuredAt %q, file for %s)", c.MeasuredAt, m.Temporal)
	}
	if c.OSBase != d.osBase {
		t.Errorf("OSBase = %d MiB, the samples give %d MiB", c.OSBase/mib, d.osBase/mib)
	}
	for _, r := range []string{"frontend", "history", "matching", "worker"} {
		got := [8]float64{float64(c.Floor[r] / mib), c.Share[r], c.Burst[r], c.NonHeap[r], c.CPU[r], c.Conn[r], c.VisConn[r]}
		want := [8]float64{float64(d.floor[r] / mib), d.share[r], d.burst[r], d.nonHeap[r], d.cpu[r], d.conn[r], d.visConn[r]}
		if got != want {
			t.Errorf("%s: constants (floor MiB, share, burst, nonHeap, cpu, conn, visConn) = %v, the samples give %v", r, got[:7], want[:7])
		}
	}
}

// TestMeasureFileRecordsS3Fit: all four roles on vps-03's cap, from the measured floors
// (implementation plan T2.1b step 5), fit or not.
func TestMeasureFileRecordsS3Fit(t *testing.T) {
	m := loadMeasure(t)
	d := derive(m)
	s3 := m.S3FitVps03
	if s3 == nil || len(s3.Roles) != 4 {
		t.Fatalf("s3FitVps03 = %+v", s3)
	}
	free := s3.CapMemoryBytes - d.osBase - d.floorSum
	if s3.FreeBytes != free || s3.Fits != (free >= 0) {
		t.Errorf("s3FitVps03 says free %d MiB, fits %v; the floors give %d MiB", s3.FreeBytes/mib, s3.Fits, free/mib)
	}
}

//go:build !linux

package helper

import (
	"errors"
	"runtime"
)

// pid1Affinity fails outside Linux: the helper only runs on Linux nodes, but tops itself
// also builds for the workstations that drive them.
func pid1Affinity() (int, error) {
	return 0, errors.New("CPU affinity is only read on Linux, not " + runtime.GOOS)
}

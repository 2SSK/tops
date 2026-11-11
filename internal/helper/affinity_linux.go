package helper

import (
	"fmt"

	"golang.org/x/sys/unix"
)

// pid1Affinity counts the CPUs PID 1 may run on.
func pid1Affinity() (int, error) {
	var set unix.CPUSet
	if err := unix.SchedGetaffinity(1, &set); err != nil {
		return 0, fmt.Errorf("sched_getaffinity: %w", err)
	}
	return set.Count(), nil
}

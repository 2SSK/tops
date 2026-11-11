package helper

import (
	"fmt"

	"golang.org/x/sys/unix"
)

// clockSynced reports whether the kernel clock is synchronised to a time  source: what
// timedatectl shows as NTPSynchronized, read the same way, from adjtimex(2).
func clockSynced() (bool, error) {
	var tx unix.Timex
	state, err := unix.Adjtimex(&tx)
	if err != nil {
		return false, fmt.Errorf("adjtimex: %w", err)
	}
	return state != unix.TIME_ERROR && tx.Status&unix.STA_UNSYNC == 0, nil
}

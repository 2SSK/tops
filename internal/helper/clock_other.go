//go:build !linux

package helper

import (
	"errors"
	"runtime"
)

// clockSynced fails outside Linux, like pid1Affinity.
func clockSynced() (bool, error) {
	return false, errors.New("clock sync is only read on Linux, not " + runtime.GOOS)
}

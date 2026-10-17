package spec

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// ParseDuration parses a Go duration ("72h", "90m") or a whole number of days ("7d").
// Go's time.ParseDuration has no unit for days, and retention is usually written in days.
func ParseDuration(s string) (time.Duration, error) {
	if days, ok := strings.CutSuffix(s, "d"); ok {
		n, err := strconv.Atoi(days)
		if err != nil {
			return 0, fmt.Errorf("%q is not a duration like 72h or 7d", s)
		}
		return time.Duration(n) * 24 * time.Hour, nil
	}
	return time.ParseDuration(s)
}

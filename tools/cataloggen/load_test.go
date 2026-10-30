package main

import (
	"testing"
	"time"
)

func TestShortDuration(t *testing.T) {
	tests := []struct {
		d    time.Duration
		want string
	}{
		{time.Hour, "1h"},
		{5 * time.Minute, "5m"},
		{90 * time.Second, "1m30s"},
		{time.Hour + time.Second, "1h0m1s"},
		{2 * time.Hour, "2h"},
		{100 * time.Millisecond, "100ms"},
		{0, "0s"},
	}

	for _, tt := range tests {
		if got := shortDuration(tt.d); got != tt.want {
			t.Errorf("shortDuration(%v) = %q, want %q", tt.d, got, tt.want)
		}
	}
}

func TestDuplicates(t *testing.T) {
	if err := duplicates([]setting{{Key: "a.b"}, {Key: "a.c"}}); err != nil {
		t.Errorf("distinct keys: err = %v", err)
	}
	if err := duplicates([]setting{{Key: "a.B"}, {Key: "a.b"}}); err == nil {
		t.Errorf("keys that differ only in case :err = nil")
	}
}

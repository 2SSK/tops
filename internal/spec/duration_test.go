package spec

import (
	"testing"
	"time"
)

func TestParseDuration(t *testing.T) {
	tests := []struct {
		in      string
		want    time.Duration
		wantErr bool
	}{
		{"72h", 72 * time.Hour, false},
		{"90m", 90 * time.Minute, false},
		{"7d", 7 * 24 * time.Hour, false},
		{"0d", 0, false},
		{"", 0, true},
		{"7x", 0, true},
		{"d", 0, true},
		{"1.5d", 0, true},
	}

	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			got, err := ParseDuration(tt.in)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ParseDuration(%q) error = %v, wantErr %v", tt.in, err, tt.wantErr)
			}
			if got != tt.want {
				t.Errorf("ParseDuration(%q) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}

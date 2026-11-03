package term

import "testing"

func TestColorEnabled(t *testing.T) {
	tests := []struct {
		name       string
		env        map[string]string
		isTerminal bool
		want       bool
	}{
		{"terminal", map[string]string{"TERM": "xterm-256color"}, true, true},
		{"pipe", map[string]string{"TERM": "xterm-256color"}, false, false},
		{"NO_COLOR set", map[string]string{"TERM": "xterm-256color", "NO_COLOR": "1"}, true, false},
		{"dumb terminal", map[string]string{"TERM": "dumb"}, true, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			getenv := func(key string) string { return tt.env[key] }
			if got := ColorEnabled(getenv, tt.isTerminal); got != tt.want {
				t.Errorf("ColorEnabled() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestPaint(t *testing.T) {
	tests := []struct {
		name   string
		symbol Symbol
		color  bool
		want   string
	}{
		{"colour on", Fail, true, "\x1b[31mx\x1b[0m"},
		{"colour off", Fail, false, "x"},
		{"added is green", Add, true, "\x1b[32mx\x1b[0m"},
		{"removed is red", Remove, true, "\x1b[31mx\x1b[0m"},
		{"changed is yellow", Change, true, "\x1b[33mx\x1b[0m"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Paint("x", tt.symbol, tt.color); got != tt.want {
				t.Errorf("Paint() = %q, want %q", got, tt.want)
			}
		})
	}
}

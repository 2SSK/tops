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

func TestPain(t *testing.T) {
	tests := []struct {
		name  string
		color bool
		want  string
	}{
		{"colour on", true, "\x1b[31mx\x1b[0m"},
		{"colour off", false, "x"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Paint("x", Fail, tt.color); got != tt.want {
				t.Errorf("Paint() = %q, want %q", got, tt.want)
			}
		})
	}
}

package term

import "testing"

func TestStyleMark(t *testing.T) {
	tests := []struct {
		name  string
		style Style
		want  string
	}{
		{"unicode and colour", Style{Unicode: true, Color: true}, "\x1b[31m✗\x1b[0m"},
		{"unicode only", Style{Unicode: true}, "✗"},
		{"colour only", Style{Color: true}, "\x1b[31mFAIL\x1b[0m"},
		{"plain", Style{}, "FAIL"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.style.Mark(Fail); got != tt.want {
				t.Errorf("Mark(Fail) = %q, want %q", got, tt.want)
			}
		})
	}
}

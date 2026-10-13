package term

import "testing"

func TestMark(t *testing.T) {
	tests := []struct {
		symbol  Symbol
		unicode bool
		want    string
	}{
		{OK, true, "✓"},
		{Warn, true, "!"},
		{Fail, true, "✗"},
		{Skip, true, "·"},
		{OK, false, "ok  "},
		{Warn, false, "WARN"},
		{Fail, false, "FAIL"},
		{Skip, false, "-   "},
	}

	for _, tt := range tests {
		if got := Mark(tt.symbol, tt.unicode); got != tt.want {
			t.Errorf("Mark(%d, %v) = %q, want %q", tt.symbol, tt.unicode, got, tt.want)
		}
	}
}

func TestUnicodeLocale(t *testing.T) {
	tests := []struct {
		name string
		env  map[string]string
		want bool
	}{
		{"LANG UTF-8", map[string]string{"LANG": "en_US.UTF-8"}, true},
		{"LANG utf8 lowercase", map[string]string{"LANG": "C.utf8"}, true},
		{"LANG C", map[string]string{"LANG": "C"}, false},
		{"nothing set", map[string]string{}, false},
		{"LC_ALL overrides LANG", map[string]string{"LC_ALL": "C", "LANG": "en_US.UTF-8"}, false},
		{"LC_CTYPE overrides LANG", map[string]string{"LC_CTYPE": "en_US.UTF-8", "LANG": "C"}, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			getenv := func(key string) string { return tt.env[key] }
			if got := UnicodeLocale(getenv); got != tt.want {
				t.Errorf("UnicodeLocale() = %v, want %v", got, tt.want)
			}
		})
	}
}

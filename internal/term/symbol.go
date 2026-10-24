package term

import "strings"

// Symbol is the status marker printed at the start of a line.
type Symbol int

// The status markers from the design brief.
const (
	OK Symbol = iota
	Warn
	Fail
	Skip
)

var (
	unicodeMarks = map[Symbol]string{OK: "✓", Warn: "!", Fail: "✗", Skip: "·"}
	asciiMarks   = map[Symbol]string{OK: "ok  ", Warn: "WARN", Fail: "FAIL", Skip: "-   "}
)

// Mark returns s as printed: a UTF-8 symbol, or a 4-character word when the locale is not UTF-8.
func Mark(s Symbol, unicode bool) string {
	if unicode {
		return unicodeMarks[s]
	}

	return asciiMarks[s]
}

// UnicodeLocale reports whether the locale allows UTF-8 output.
// LC_ALL overrides LC_CTYPE, which overrides LANG; the first one that is set decides.
func UnicodeLocale(getenv func(string) string) bool {
	for _, name := range []string{"LC_ALL", "LC_CTYPE", "LANG"} {
		v := getenv(name)
		if v == "" {
			continue
		}
		v = strings.ToLower(v)
		return strings.Contains(v, "utf-8") || strings.Contains(v, "utf8")
	}
	return false
}

package term

import (
	"io"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

var markColors = map[Symbol]lipgloss.Color{OK: "2", Warn: "3", Fail: "1", Skip: "8"}

// ColorEnabled reports whether output may use colour: only on a terminal,
// and never when NO_COLOR is set or TERM is dumb.
func ColorEnabled(getenv func(string) string, isTerminal bool) bool {
	if !isTerminal {
		return false
	}
	return getenv("NO_COLOR") == "" && getenv("TERM") != "dumb"
}

// Paint colours text with the color of s, or returns it unchanged when color is false.
func Paint(text string, s Symbol, color bool) string {
	if !color {
		return text
	}
	r := lipgloss.NewRenderer(io.Discard)
	r.SetColorProfile(termenv.ANSI)
	return r.NewStyle().Foreground(markColors[s]).Render(text)
}

package cli

import (
	"io"
	"os"

	xterm "golang.org/x/term"

	"github.com/2SSK/tops/internal/term"
)

// StyleFor returns how output written to w may be decorated: symbols from the
// locale, colour only on a terminal and only without --no-color or NO_COLOR.
func StyleFor(w io.Writer, opts Options) term.Style {
	f, ok := w.(*os.File)
	tty := ok && xterm.IsTerminal(int(f.Fd()))
	return term.Style{
		Unicode: term.UnicodeLocale(opts.Getenv),
		Color:   !opts.NoColor && term.ColorEnabled(opts.Getenv, tty),
	}
}

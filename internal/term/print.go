package term

import (
	"errors"
	"io"
	"strings"
)

// PrintError writes err for the operator: what happened, why, and how to fix it.
func PrintError(w io.Writer, style Style, err error) error {
	mark := style.Mark(Fail) + " "
	var b strings.Builder

	var ue *UserError
	if !errors.As(err, &ue) {
		b.WriteString(mark + err.Error() + "\n")
	} else {
		b.WriteString(mark + ue.What + "\n")
		if ue.Why != "" {
			b.WriteString("  why: " + ue.Why + "\n")
		}
		if ue.Fix != "" {
			b.WriteString("  fix: " + ue.Fix + "\n")
		}
		b.WriteString("  (code: " + ue.Code + ")\n")
	}

	_, err = io.WriteString(w, b.String())
	return err
}

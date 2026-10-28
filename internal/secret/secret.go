// Package secret holds values that must never be printed, like the passwords in secrets.env.
package secret

import (
	"fmt"
	"io"
	"log/slog"
)

// masked is what every way of printing a Value prints.
const masked = "***"

// Value is a secret. printing it with fmt, %v, %#v or slog prints ***; Reveal returns
// the value, and is called only where it is written into a file on a node.
type Value struct{ v string }

// New wraps v.
func New(v string) Value { return Value{v} }

// Reveal returns the secret itself.
func (s Value) Reveal() string { return s.v }

// String is used by %s, %v, Print and Println.
func (s Value) String() string { return masked }

// GoString is used by %#v.
func (s Value) GoString() string { return masked }

// Format handles every verb, so %q, %x and %d print *** too.
func (s Value) Format(f fmt.State, _ rune) {
	_, _ = io.WriteString(f, masked)
}

// LogValue is used by lgo/slog.
func (s Value) LogValue() slog.Value {
	return slog.StringValue(masked)
}

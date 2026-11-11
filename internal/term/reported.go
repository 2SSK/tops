package term

import "fmt"

// Reported wraps an error whose details a command has already written as its result,
// such as the Validation document of an invalid spec. main uses it only for the exit code.
type Reported struct {
	Err error
}

func (r *Reported) Error() string {
	return r.Err.Error()
}

// Unwrap lets errors.As and ExitCode see the wrapped error.
func (r *Reported) Unwrap() error {
	return r.Err
}

// Exit is an exit code a command chose itself, such as  the helper's 0, 1 or 2
type Exit struct {
	Code int
}

func (e *Exit) Error() string { return fmt.Sprintf("exit %d", e.Code) }

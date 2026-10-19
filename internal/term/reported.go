package term

// Reported wraps an error whose details a command has already written as its result,
// such as the Validation document of an invalid spec. main uses ot only for the exit code.
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

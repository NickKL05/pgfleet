package main

// Exit codes shared across commands (spec 4.5, 5.4):
//
//	0 success / no drift
//	1 a tenant failed, or drift was found
//	2 config or usage error
//	3 connection or discovery error
const (
	exitOK         = 0
	exitFailure    = 1
	exitUsage      = 2
	exitConnection = 3
)

// exitError carries an explicit process exit code out of a command.
type exitError struct {
	code int
	err  error
}

func (e *exitError) Error() string {
	if e.err == nil {
		return ""
	}
	return e.err.Error()
}

func (e *exitError) Unwrap() error { return e.err }

func usageErr(err error) error   { return &exitError{code: exitUsage, err: err} }
func connErr(err error) error    { return &exitError{code: exitConnection, err: err} }
func failureErr(err error) error { return &exitError{code: exitFailure, err: err} }
func failureCode() error         { return &exitError{code: exitFailure} }

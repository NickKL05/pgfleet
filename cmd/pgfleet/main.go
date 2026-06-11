// Command pgfleet is a multi-tenant PostgreSQL migration and drift toolkit for
// schema-per-tenant databases.
package main

import (
	"errors"
	"fmt"
	"os"
)

func main() {
	err := newRootCmd().Execute()
	if err == nil {
		return
	}

	var ec *exitError
	if errors.As(err, &ec) {
		if ec.err != nil {
			fmt.Fprintln(os.Stderr, "pgfleet: "+ec.err.Error())
		}
		os.Exit(ec.code)
	}

	// Anything not classified is treated as a generic failure.
	fmt.Fprintln(os.Stderr, "pgfleet: "+err.Error())
	os.Exit(1)
}

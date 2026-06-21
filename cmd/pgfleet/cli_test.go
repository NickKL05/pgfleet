package main

import (
	"errors"
	"fmt"
	"io"
	"testing"

	"github.com/NickKL05/pgfleet/internal/migrate"
)

// execArgs runs the root command with the given arguments, discarding output,
// and returns the resulting error. None of the paths exercised here touch the
// database or the config file, so the tests stay fast and offline.
func execArgs(args ...string) error {
	root := newRootCmd()
	root.SetArgs(args)
	root.SetOut(io.Discard)
	root.SetErr(io.Discard)
	return root.Execute()
}

// wantExit asserts that err carries the expected process exit code.
func wantExit(t *testing.T, err error, code int) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected an error with exit code %d, got nil", code)
	}
	var ec *exitError
	if !errors.As(err, &ec) {
		t.Fatalf("error %v is not an *exitError", err)
	}
	if ec.code != code {
		t.Fatalf("exit code = %d, want %d (err: %v)", ec.code, code, err)
	}
}

func TestExitErrorClassification(t *testing.T) {
	base := errors.New("boom")
	cases := []struct {
		name string
		err  error
		code int
	}{
		{"usage", usageErr(base), exitUsage},
		{"connection", connErr(base), exitConnection},
		{"failure", failureErr(base), exitFailure},
		{"failure code only", failureCode(), exitFailure},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var ec *exitError
			if !errors.As(tc.err, &ec) {
				t.Fatalf("not an exitError: %v", tc.err)
			}
			if ec.code != tc.code {
				t.Fatalf("code = %d, want %d", ec.code, tc.code)
			}
		})
	}

	// Unwrap exposes the wrapped cause for errors.Is/As chains.
	wrapped := usageErr(base)
	if !errors.Is(wrapped, base) {
		t.Error("usageErr should unwrap to its cause")
	}
	// A code-only error has no message and no cause.
	if msg := failureCode().Error(); msg != "" {
		t.Errorf("failureCode().Error() = %q, want empty", msg)
	}
}

func TestMigrateDownRequiresExplicitTarget(t *testing.T) {
	// Without --to, the command must refuse before doing any work (usage error).
	wantExit(t, execArgs("migrate", "down"), exitUsage)
}

func TestDriftDiffArgValidation(t *testing.T) {
	// Neither a tenant nor --all: ambiguous, usage error.
	wantExit(t, execArgs("drift", "diff"), exitUsage)
	// Both a tenant and --all: also ambiguous.
	wantExit(t, execArgs("drift", "diff", "tenant_1", "--all"), exitUsage)
}

func TestDriftRepairArgValidation(t *testing.T) {
	wantExit(t, execArgs("drift", "repair"), exitUsage)
	wantExit(t, execArgs("drift", "repair", "tenant_1", "--all"), exitUsage)
}

func TestRootCommandWiring(t *testing.T) {
	root := newRootCmd()

	for _, name := range []string{"migrate", "drift"} {
		if _, _, err := root.Find([]string{name}); err != nil {
			t.Errorf("expected a %q subcommand: %v", name, err)
		}
	}

	for _, flag := range []string{"config", "tenants", "json", "log-format"} {
		if root.PersistentFlags().Lookup(flag) == nil {
			t.Errorf("expected persistent flag --%s", flag)
		}
	}

	// Every leaf command should be registered under its parent.
	migrateSubs := map[string]bool{"up": false, "down": false, "status": false, "new": false}
	mig, _, _ := root.Find([]string{"migrate"})
	for _, c := range mig.Commands() {
		migrateSubs[c.Name()] = true
	}
	for name, found := range migrateSubs {
		if !found {
			t.Errorf("migrate is missing subcommand %q", name)
		}
	}
}

func TestCommandName(t *testing.T) {
	if got := commandName(migrate.Up); got != "migrate up" {
		t.Errorf("commandName(Up) = %q", got)
	}
	if got := commandName(migrate.Down); got != "migrate down" {
		t.Errorf("commandName(Down) = %q", got)
	}
}

func TestShortHashTruncation(t *testing.T) {
	long := "0123456789abcdef0123456789"
	if got := short(long); got != "0123456789ab" || len(got) != 12 {
		t.Errorf("short(long) = %q, want first 12 chars", got)
	}
	if got := short("abc"); got != "abc" {
		t.Errorf("short(short input) = %q, want unchanged", got)
	}
}

func TestGenRunIDFormat(t *testing.T) {
	id := genRunID()
	if len(id) == 0 || id[:4] != "run-" {
		t.Errorf("genRunID() = %q, want a run- prefix", id)
	}
}

// Example documents the exit-code contract the CLI promises in its help text.
func ExampleexitError() {
	for _, e := range []*exitError{
		usageErr(errors.New("bad config")).(*exitError),
		connErr(errors.New("no route to host")).(*exitError),
		failureErr(errors.New("tenant failed")).(*exitError),
	} {
		fmt.Printf("code=%d msg=%q\n", e.code, e.Error())
	}
	// Output:
	// code=2 msg="bad config"
	// code=3 msg="no route to host"
	// code=1 msg="tenant failed"
}

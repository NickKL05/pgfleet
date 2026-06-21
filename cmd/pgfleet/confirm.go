package main

import (
	"bufio"
	"fmt"
	"os"
	"strings"
)

// confirmAction prints a one-line plan and waits for an interactive yes (R6.3,
// R5.8). Callers bypass it with --yes for CI. The prompt goes to stderr so it
// never contaminates machine-readable output on stdout (e.g. migrate down --json).
func confirmAction(action string, tenantCount int) error {
	fmt.Fprintf(os.Stderr, "About to %s across %d tenant(s).\n", action, tenantCount)
	fmt.Fprint(os.Stderr, "Proceed? type 'yes' to continue: ")

	reader := bufio.NewReader(os.Stdin)
	line, _ := reader.ReadString('\n')
	if strings.TrimSpace(line) != "yes" {
		return usageErr(fmt.Errorf("aborted by user"))
	}
	return nil
}

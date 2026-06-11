package main

import (
	"fmt"

	"github.com/spf13/cobra"
)

// The drift subsystem lands in milestones M3 to M5. The command surface is
// defined now so the CLI shape is stable; each command reports that it is not
// yet implemented rather than silently doing nothing.
func newDriftCmd(gf *globalFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "drift",
		Short: "Detect and repair schema drift across tenant schemas",
	}
	cmd.AddCommand(
		driftStub(gf, "verify", "Check each tenant against the canonical reference (pass/fail)"),
		driftStub(gf, "diff", "Show object-level differences for a tenant"),
		driftStub(gf, "repair", "Generate corrective DDL for a drifted tenant"),
		driftStub(gf, "snapshot", "Write a deterministic schema.lock.json reference"),
	)
	return cmd
}

func driftStub(_ *globalFlags, use, short string) *cobra.Command {
	return &cobra.Command{
		Use:   use,
		Short: short,
		RunE: func(_ *cobra.Command, _ []string) error {
			return usageErr(fmt.Errorf("drift %s is not implemented yet (planned for milestones M3-M5)", use))
		},
	}
}

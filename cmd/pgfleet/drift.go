package main

import (
	"context"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/NickKL05/pgfleet/internal/config"
	"github.com/NickKL05/pgfleet/internal/drift"
	"github.com/NickKL05/pgfleet/internal/drift/diffgen"
	"github.com/NickKL05/pgfleet/internal/drift/model"
	"github.com/NickKL05/pgfleet/internal/report"
)

// driftFlags hold options shared by the drift subcommands.
type driftFlags struct {
	ignoreColumnOrder bool
	strict            bool
}

func newDriftCmd(gf *globalFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "drift",
		Short: "Detect and repair schema drift across tenant schemas",
	}
	df := &driftFlags{}
	cmd.PersistentFlags().BoolVar(&df.ignoreColumnOrder, "ignore-column-order", false, "treat column order as insignificant (R5.3)")
	cmd.PersistentFlags().BoolVar(&df.strict, "strict", false, "include storage parameters in comparison (R5.4)")

	cmd.AddCommand(
		newDriftVerifyCmd(gf, df),
		newDriftDiffCmd(gf, df),
		newDriftRepairCmd(gf, df),
		newDriftSnapshotCmd(gf, df),
	)
	return cmd
}

func newDriftVerifyCmd(gf *globalFlags, df *driftFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "verify",
		Short: "Check each tenant against the canonical reference (pass/fail)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			a, err := loadApp(gf)
			if err != nil {
				return err
			}
			if err := a.connect(ctx); err != nil {
				return err
			}
			defer a.close()

			tenants, err := a.tenants(ctx)
			if err != nil {
				return err
			}
			opts := driftOptions(a.cfg, df)

			ref, err := resolveReference(ctx, a, opts)
			if err != nil {
				return err
			}

			rep, err := drift.Verify(ctx, a.pool, tenants, ref, opts)
			if err != nil {
				return connErr(err)
			}
			if err := emitDrift(gf, rep); err != nil {
				return err
			}
			if rep.Drifted() {
				return failureCode()
			}
			return nil
		},
	}
}

func newDriftDiffCmd(gf *globalFlags, df *driftFlags) *cobra.Command {
	var all bool
	cmd := &cobra.Command{
		Use:   "diff <tenant>|--all",
		Short: "Show object-level and field-level differences for a tenant",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if all == (len(args) == 1) {
				return usageErr(fmt.Errorf("provide exactly one of <tenant> or --all"))
			}
			ctx := cmd.Context()
			a, err := loadApp(gf)
			if err != nil {
				return err
			}
			if err := a.connect(ctx); err != nil {
				return err
			}
			defer a.close()

			var tenants []string
			if all {
				if tenants, err = a.tenants(ctx); err != nil {
					return err
				}
			} else {
				tenants = []string{args[0]}
			}

			spec, err := referenceSpec(a.cfg)
			if err != nil {
				return err
			}
			rep, err := drift.Diff(ctx, a.pool, tenants, spec, driftOptions(a.cfg, df))
			if err != nil {
				return connErr(err)
			}

			if gf.json {
				if err := report.RenderDriftJSON(os.Stdout, rep); err != nil {
					return err
				}
			} else if err := report.RenderDiffHuman(os.Stdout, rep); err != nil {
				return err
			}
			if rep.Drifted() {
				return failureCode()
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&all, "all", false, "diff every discovered tenant")
	return cmd
}

func newDriftRepairCmd(gf *globalFlags, df *driftFlags) *cobra.Command {
	var all, apply, yes, allowDestructive bool
	var out string
	cmd := &cobra.Command{
		Use:   "repair <tenant>|--all",
		Short: "Generate (or apply) dependency-ordered corrective DDL",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if all == (len(args) == 1) {
				return usageErr(fmt.Errorf("provide exactly one of <tenant> or --all"))
			}
			ctx := cmd.Context()
			a, err := loadApp(gf)
			if err != nil {
				return err
			}
			// Repair needs full object definitions, which a snapshot does not carry.
			if a.cfg.Drift.Reference.Mode != config.ReferenceSchema {
				return usageErr(fmt.Errorf("repair requires drift.reference.mode = schema (needs full object definitions)"))
			}
			if err := a.connect(ctx); err != nil {
				return err
			}
			defer a.close()

			var tenants []string
			if all {
				if tenants, err = a.tenants(ctx); err != nil {
					return err
				}
			} else {
				tenants = []string{args[0]}
			}

			plans, err := drift.Repair(ctx, a.pool, tenants, a.cfg.Drift.Reference.Schema, diffgen.RepairOptions{
				Model:            driftOptions(a.cfg, df).Model,
				AllowDestructive: allowDestructive,
			})
			if err != nil {
				return connErr(err)
			}

			if apply {
				return runRepairApply(ctx, a, plans, yes)
			}
			return runRepairGenerate(plans, out)
		},
	}
	cmd.Flags().BoolVar(&all, "all", false, "repair every discovered tenant")
	cmd.Flags().BoolVar(&apply, "apply", false, "apply the repair instead of writing files")
	cmd.Flags().BoolVar(&yes, "yes", false, "skip the confirmation prompt (for CI)")
	cmd.Flags().BoolVar(&allowDestructive, "allow-destructive", false, "permit DROP TABLE and DROP COLUMN (R5.9)")
	cmd.Flags().StringVar(&out, "out", "repair", "directory for generated .sql files")
	return cmd
}

// runRepairGenerate writes per-tenant repair files and reports a summary.
func runRepairGenerate(plans []*diffgen.RepairPlan, out string) error {
	written, err := drift.WriteRepairFiles(out, plans)
	if err != nil {
		return failureErr(err)
	}

	work := false
	skipped := 0
	for _, p := range plans {
		if p.HasWork() {
			work = true
		}
		skipped += len(p.Skipped)
	}

	if len(written) == 0 {
		fmt.Println("No drift; nothing to repair.")
		return nil
	}
	for _, path := range written {
		fmt.Printf("wrote %s\n", path)
	}
	if skipped > 0 {
		fmt.Printf("\n%d action(s) skipped (see file comments; --allow-destructive or manual action required)\n", skipped)
	}
	if work {
		return failureCode()
	}
	return nil
}

// runRepairApply confirms then applies the repair to each tenant in a guarded
// transaction (R5.8).
func runRepairApply(ctx context.Context, a *app, plans []*diffgen.RepairPlan, yes bool) error {
	var withWork []*diffgen.RepairPlan
	total := 0
	for _, p := range plans {
		if p.HasWork() {
			withWork = append(withWork, p)
			total += len(p.Statements)
		}
	}
	if len(withWork) == 0 {
		fmt.Println("No drift; nothing to apply.")
		return nil
	}
	if !yes {
		if err := confirmAction(fmt.Sprintf("apply %d repair statement(s)", total), len(withWork)); err != nil {
			return err
		}
	}

	applyOpts := drift.ApplyOptions{
		LockID:           a.cfg.Migrations.LockID,
		StatementTimeout: a.cfg.Run.StatementTimeout.Std(),
		LockTimeout:      a.cfg.Run.LockTimeout.Std(),
	}
	failed := 0
	for _, p := range withWork {
		if err := drift.ApplyRepair(ctx, a.pool, p, applyOpts); err != nil {
			failed++
			fmt.Printf("%s: FAILED: %v\n", p.Tenant, err)
			continue
		}
		fmt.Printf("%s: applied %d statement(s)\n", p.Tenant, len(p.Statements))
	}
	if failed > 0 {
		return failureErr(fmt.Errorf("%d of %d tenant(s) failed to repair", failed, len(withWork)))
	}
	return nil
}

func newDriftSnapshotCmd(gf *globalFlags, df *driftFlags) *cobra.Command {
	var out string
	cmd := &cobra.Command{
		Use:   "snapshot",
		Short: "Write a deterministic schema.lock.json reference",
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			a, err := loadApp(gf)
			if err != nil {
				return err
			}
			if a.cfg.Drift.Reference.Mode != config.ReferenceSchema {
				return usageErr(fmt.Errorf("snapshot requires drift.reference.mode = schema (the template to capture)"))
			}
			if err := a.connect(ctx); err != nil {
				return err
			}
			defer a.close()

			snap, err := drift.BuildSnapshot(ctx, a.pool, a.cfg.Drift.Reference.Schema, driftOptions(a.cfg, df))
			if err != nil {
				return connErr(err)
			}
			if out == "" {
				out = a.cfg.Drift.Reference.Snapshot
			}
			if err := drift.WriteSnapshot(out, snap); err != nil {
				return failureErr(err)
			}
			fmt.Printf("wrote %s (%d objects, fingerprint %s)\n",
				out, len(snap.Fingerprint.Objects), short(snap.Fingerprint.Hash))
			return nil
		},
	}
	cmd.Flags().StringVar(&out, "out", "", "snapshot output path (default: drift.reference.snapshot)")
	return cmd
}

// resolveReference builds the canonical reference from config: either a live
// template schema or a committed snapshot file (spec 5.1).
func resolveReference(ctx context.Context, a *app, opts drift.Options) (*drift.Reference, error) {
	switch a.cfg.Drift.Reference.Mode {
	case config.ReferenceSchema:
		ref, err := drift.ReferenceFromSchema(ctx, a.pool, a.cfg.Drift.Reference.Schema, opts)
		if err != nil {
			return nil, connErr(err)
		}
		return ref, nil
	case config.ReferenceSnapshot:
		ref, err := drift.ReadSnapshot(a.cfg.Drift.Reference.Snapshot)
		if err != nil {
			return nil, usageErr(err)
		}
		return ref, nil
	default:
		return nil, usageErr(fmt.Errorf("unknown drift.reference.mode %q", a.cfg.Drift.Reference.Mode))
	}
}

// referenceSpec maps the config reference block to a drift.ReferenceSpec.
func referenceSpec(cfg *config.Config) (drift.ReferenceSpec, error) {
	switch cfg.Drift.Reference.Mode {
	case config.ReferenceSchema:
		return drift.ReferenceSpec{Mode: drift.ModeSchema, Schema: cfg.Drift.Reference.Schema}, nil
	case config.ReferenceSnapshot:
		return drift.ReferenceSpec{Mode: drift.ModeSnapshot, SnapshotPath: cfg.Drift.Reference.Snapshot}, nil
	default:
		return drift.ReferenceSpec{}, usageErr(fmt.Errorf("unknown drift.reference.mode %q", cfg.Drift.Reference.Mode))
	}
}

func driftOptions(cfg *config.Config, df *driftFlags) drift.Options {
	return drift.Options{
		Model: model.Options{
			IgnoreColumnOrder: df.ignoreColumnOrder,
			Strict:            df.strict,
			Ignore:            cfg.Drift.Ignore,
		},
	}
}

func emitDrift(gf *globalFlags, rep *report.DriftReport) error {
	if gf.json {
		return report.RenderDriftJSON(os.Stdout, rep)
	}
	return report.RenderDriftHuman(os.Stdout, rep)
}

func short(hash string) string {
	if len(hash) > 12 {
		return hash[:12]
	}
	return hash
}

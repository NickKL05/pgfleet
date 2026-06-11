package main

import (
	"context"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/NickKL05/pgfleet/internal/config"
	"github.com/NickKL05/pgfleet/internal/drift"
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
		newDriftSnapshotCmd(gf, df),
		driftStub("diff", "Show object-level differences for a tenant (planned M4)"),
		driftStub("repair", "Generate corrective DDL for a drifted tenant (planned M5)"),
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

func driftStub(use, short string) *cobra.Command {
	return &cobra.Command{
		Use:   use,
		Short: short,
		RunE: func(_ *cobra.Command, _ []string) error {
			return usageErr(fmt.Errorf("drift %s is not implemented yet", use))
		},
	}
}

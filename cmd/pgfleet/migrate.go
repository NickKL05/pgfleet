package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/NickKL05/pgfleet/internal/migrate"
	"github.com/NickKL05/pgfleet/internal/report"
)

func newMigrateCmd(gf *globalFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "migrate",
		Short: "Apply and inspect versioned migrations across tenant schemas",
	}
	cmd.AddCommand(
		newMigrateUpCmd(gf),
		newMigrateDownCmd(gf),
		newMigrateStatusCmd(gf),
		newMigrateNewCmd(gf),
	)
	return cmd
}

func newMigrateUpCmd(gf *globalFlags) *cobra.Command {
	var to int
	var dryRun, allowDirty bool
	cmd := &cobra.Command{
		Use:   "up",
		Short: "Apply pending migrations up to --to (default: latest)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runMigrate(cmd.Context(), gf, migrateParams{
				dir: migrate.Up, to: to, dryRun: dryRun, allowDirty: allowDirty,
			})
		},
	}
	cmd.Flags().IntVar(&to, "to", 0, "stop at this version (0 = latest)")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "print SQL without applying")
	cmd.Flags().BoolVar(&allowDirty, "allow-dirty", false, "proceed despite checksum mismatches (logged loudly)")
	return cmd
}

func newMigrateDownCmd(gf *globalFlags) *cobra.Command {
	var to int
	var toSet, dryRun, allowDirty, yes bool
	cmd := &cobra.Command{
		Use:   "down --to N",
		Short: "Roll back migrations down to version N (explicit target required)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if !toSet {
				return usageErr(fmt.Errorf("migrate down requires an explicit --to target"))
			}
			return runMigrate(cmd.Context(), gf, migrateParams{
				dir: migrate.Down, to: to, dryRun: dryRun, allowDirty: allowDirty,
				confirm: !yes, action: fmt.Sprintf("roll back to version %d", to),
			})
		},
	}
	cmd.Flags().IntVar(&to, "to", 0, "roll back down to this version")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "print SQL without applying")
	cmd.Flags().BoolVar(&allowDirty, "allow-dirty", false, "proceed despite checksum mismatches")
	cmd.Flags().BoolVar(&yes, "yes", false, "skip the confirmation prompt (for CI)")
	cmd.PreRun = func(cmd *cobra.Command, _ []string) {
		toSet = cmd.Flags().Changed("to")
	}
	return cmd
}

func newMigrateStatusCmd(gf *globalFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show each tenant's current version, grouped",
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			a, err := loadApp(gf)
			if err != nil {
				return err
			}
			set, err := migrate.Load(a.cfg.Migrations.Dir)
			if err != nil {
				return usageErr(err)
			}
			if err := a.connect(ctx); err != nil {
				return err
			}
			defer a.close()

			tenants, err := a.tenants(ctx)
			if err != nil {
				return err
			}
			rep, err := migrate.Status(ctx, a.pool, tenants, set, a.cfg.Migrations.Table, a.cfg.Run.Concurrency)
			if err != nil {
				return connErr(err)
			}
			return emitReport(gf, rep)
		},
	}
}

func newMigrateNewCmd(gf *globalFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "new <description>",
		Short: "Scaffold an up/down migration pair",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			a, err := loadApp(gf)
			if err != nil {
				return err
			}
			up, down, err := migrate.Scaffold(a.cfg.Migrations.Dir, args[0])
			if err != nil {
				return usageErr(err)
			}
			fmt.Printf("created %s\ncreated %s\n", up, down)
			return nil
		},
	}
}

// migrateParams carries the options for an up/down run.
type migrateParams struct {
	dir        migrate.Direction
	to         int
	dryRun     bool
	allowDirty bool
	confirm    bool   // prompt before applying (down migrations)
	action     string // human description of the destructive action
}

// runMigrate wires config, pool, discovery, and the runner for up/down.
func runMigrate(ctx context.Context, gf *globalFlags, p migrateParams) error {
	a, err := loadApp(gf)
	if err != nil {
		return err
	}
	set, err := migrate.Load(a.cfg.Migrations.Dir)
	if err != nil {
		return usageErr(err)
	}
	if err := a.connect(ctx); err != nil {
		return err
	}
	defer a.close()

	tenants, err := a.tenants(ctx)
	if err != nil {
		return err
	}

	// Destructive paths print a plan and require confirmation unless --yes or
	// --dry-run is set (R6.3).
	if p.confirm && !p.dryRun {
		if err := confirmAction(p.action, len(tenants)); err != nil {
			return err
		}
	}

	runner := migrate.NewRunner(a.pool, migrate.Options{
		Set:              set,
		Table:            a.cfg.Migrations.Table,
		LockID:           a.cfg.Migrations.LockID,
		Concurrency:      a.cfg.Run.Concurrency,
		StatementTimeout: a.cfg.Run.StatementTimeout.Std(),
		LockTimeout:      a.cfg.Run.LockTimeout.Std(),
		FailFast:         a.cfg.Run.FailFast,
		AllowDirty:       p.allowDirty,
		DryRun:           p.dryRun,
		Direction:        p.dir,
		Target:           p.to,
		Logger:           a.logger,
		DryRunOut:        os.Stdout,
	})

	rep, err := runner.Run(ctx, genRunID(), commandName(p.dir), tenants)
	if err != nil {
		return failureErr(err)
	}
	if err := emitReport(gf, rep); err != nil {
		return err
	}
	if rep.Failed() {
		return failureCode()
	}
	return nil
}

func emitReport(gf *globalFlags, rep *report.RunReport) error {
	if gf.json {
		return report.RenderJSON(os.Stdout, rep)
	}
	return report.RenderHuman(os.Stdout, rep)
}

func commandName(dir migrate.Direction) string {
	if dir == migrate.Down {
		return "migrate down"
	}
	return "migrate up"
}

func genRunID() string {
	return fmt.Sprintf("run-%s", time.Now().UTC().Format("20060102T150405Z"))
}

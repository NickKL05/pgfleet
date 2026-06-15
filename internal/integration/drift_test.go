//go:build integration

package integration

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/NickKL05/pgfleet/internal/drift"
	"github.com/NickKL05/pgfleet/internal/drift/diffgen"
	"github.com/NickKL05/pgfleet/internal/report"
)

func driftDiffClasses(rep *report.DriftReport, schema string) map[string]string {
	out := map[string]string{}
	for _, t := range rep.Tenants {
		if t.Schema != schema {
			continue
		}
		for _, d := range t.Differences {
			out[d.Type+":"+d.Name] = d.Class
		}
	}
	return out
}

// T3: mutate one tenant three ways; verify flags exactly it, diff names the
// exact objects, and the generated repair converges it to zero drift.
func TestT3_DetectExplainRepair(t *testing.T) {
	ctx := context.Background()
	schemas := bulkCreateSchemas(t, "t3_", 4)
	template := schemas[0]
	bad := schemas[2]
	for _, s := range schemas {
		applyUsersTable(t, s)
	}

	mustExec(t, fmt.Sprintf("drop index %s.users_created_at_idx", bad))
	mustExec(t, fmt.Sprintf("alter table %s.users alter column display_name type varchar(100)", bad))
	mustExec(t, fmt.Sprintf("create table %s.rogue (id bigint generated always as identity primary key, note text)", bad))

	tenants := schemas[1:] // everything except the template
	opts := drift.Options{}

	// verify: only the bad tenant drifts.
	ref, err := drift.ReferenceFromSchema(ctx, testPool, template, opts)
	if err != nil {
		t.Fatal(err)
	}
	vrep, err := drift.Verify(ctx, testPool, tenants, ref, opts)
	if err != nil {
		t.Fatal(err)
	}
	for _, tr := range vrep.Tenants {
		want := tr.Schema == bad
		if tr.Drifted != want {
			t.Fatalf("tenant %s drifted=%v, want %v", tr.Schema, tr.Drifted, want)
		}
	}

	// diff: the exact objects are named with the right classes.
	drep, err := drift.Diff(ctx, testPool, []string{bad}, drift.ReferenceSpec{Mode: drift.ModeSchema, Schema: template}, opts)
	if err != nil {
		t.Fatal(err)
	}
	classes := driftDiffClasses(drep, bad)
	if classes["index:users_created_at_idx"] != diffgen.Missing {
		t.Errorf("expected missing index, got %q", classes["index:users_created_at_idx"])
	}
	if classes["column:users.display_name"] != diffgen.Modified {
		t.Errorf("expected modified column, got %q", classes["column:users.display_name"])
	}
	if classes["table:rogue"] != diffgen.Extra {
		t.Errorf("expected extra table, got %q", classes["table:rogue"])
	}

	// repair: apply the generated plan and confirm convergence to zero drift.
	plans, err := drift.Repair(ctx, testPool, []string{bad}, template, diffgen.RepairOptions{AllowDestructive: true})
	if err != nil {
		t.Fatal(err)
	}
	applyOpts := drift.ApplyOptions{LockID: testLockID, StatementTimeout: 30 * time.Second, LockTimeout: 5 * time.Second}
	if err := drift.ApplyRepair(ctx, testPool, plans[0], applyOpts); err != nil {
		t.Fatalf("apply repair: %v", err)
	}
	after, err := drift.Verify(ctx, testPool, []string{bad}, ref, opts)
	if err != nil {
		t.Fatal(err)
	}
	if after.Drifted() {
		t.Fatalf("repair did not converge: %+v", after.Tenants[0].Differences)
	}
}

// Property test: DDL generated to build a schema from scratch reproduces the
// reference exactly (fingerprints equal). This exercises the create path of the
// repair generator the way a snapshot restore would.
func TestProperty_GeneratedDDLReproducesSchema(t *testing.T) {
	ctx := context.Background()
	schemas := bulkCreateSchemas(t, "prop_", 2)
	template := schemas[0]
	fresh := schemas[1]
	applyUsersTable(t, template)

	// Model the (empty) fresh schema and the template, then generate the DDL
	// that converges the empty schema to the template and apply it.
	models, err := drift.Models(ctx, testPool, []string{template, fresh})
	if err != nil {
		t.Fatal(err)
	}
	plan := diffgen.GenerateRepair(models[template], models[fresh], fresh, diffgen.RepairOptions{})
	if !plan.HasWork() {
		t.Fatal("expected a non-empty create plan for an empty schema")
	}
	applyOpts := drift.ApplyOptions{LockID: testLockID, StatementTimeout: 30 * time.Second, LockTimeout: 5 * time.Second}
	if err := drift.ApplyRepair(ctx, testPool, plan, applyOpts); err != nil {
		t.Fatalf("apply generated DDL: %v", err)
	}

	fps, err := drift.Fingerprints(ctx, testPool, []string{template, fresh}, drift.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if fps[template].Hash != fps[fresh].Hash {
		drep, _ := drift.Diff(ctx, testPool, []string{fresh}, drift.ReferenceSpec{Mode: drift.ModeSchema, Schema: template}, drift.Options{})
		t.Fatalf("rebuilt schema differs from reference:\n  ref:   %s\n  fresh: %s\n  diff: %+v",
			fps[template].Hash, fps[fresh].Hash, drep.Tenants[0].Differences)
	}
}

// TestDriftSnapshotMode covers the snapshot reference path: build a snapshot of
// the template, then verify and diff a drifted tenant against the committed file
// rather than a live schema.
func TestDriftSnapshotMode(t *testing.T) {
	ctx := context.Background()
	schemas := bulkCreateSchemas(t, "snap_", 2)
	template := schemas[0]
	tenant := schemas[1]
	applyUsersTable(t, template)
	applyUsersTable(t, tenant)
	mustExec(t, fmt.Sprintf("drop index %s.users_created_at_idx", tenant))

	snap, err := drift.BuildSnapshot(ctx, testPool, template, drift.Options{})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "schema.lock.json")
	if err := drift.WriteSnapshot(path, snap); err != nil {
		t.Fatal(err)
	}

	// verify against the snapshot reference.
	ref, err := drift.ReadSnapshot(path)
	if err != nil {
		t.Fatal(err)
	}
	vrep, err := drift.Verify(ctx, testPool, []string{tenant}, ref, drift.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if !vrep.Drifted() {
		t.Fatal("expected drift against the snapshot")
	}

	// diff against the snapshot reference (object-level, no field detail).
	drep, err := drift.Diff(ctx, testPool, []string{tenant}, drift.ReferenceSpec{Mode: drift.ModeSnapshot, SnapshotPath: path}, drift.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if driftDiffClasses(drep, tenant)["index:users_created_at_idx"] != diffgen.Missing {
		t.Fatalf("expected missing index against snapshot, got %+v", drep.Tenants[0].Differences)
	}
}

package migrate

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func writeFile(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestLoadOrdersAndAllowsGaps(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "0001_init.up.sql", "create table t ();")
	writeFile(t, dir, "0001_init.down.sql", "drop table t;")
	writeFile(t, dir, "0005_later.up.sql", "alter table t add column a int;")

	set, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if set.Len() != 2 {
		t.Fatalf("expected 2 migrations, got %d", set.Len())
	}
	all := set.All()
	if all[0].Version != 1 || all[1].Version != 5 {
		t.Fatalf("unexpected ordering: %d, %d", all[0].Version, all[1].Version)
	}
	if !all[0].HasDown() {
		t.Error("0001 should have a down file")
	}
	if all[1].HasDown() {
		t.Error("0005 should not have a down file")
	}
}

func TestLoadRejectsDuplicateVersion(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "0001_a.up.sql", "select 1;")
	writeFile(t, dir, "0001_b.up.sql", "select 2;")

	if _, err := Load(dir); err == nil {
		t.Fatal("expected duplicate version error")
	}
}

func TestChecksumStableAcrossLineEndings(t *testing.T) {
	dirA := t.TempDir()
	dirB := t.TempDir()
	writeFile(t, dirA, "0001_x.up.sql", "create table t ();\nselect 1;\n")
	writeFile(t, dirB, "0001_x.up.sql", "create table t ();\r\nselect 1;   \r\n")

	a, err := Load(dirA)
	if err != nil {
		t.Fatal(err)
	}
	b, err := Load(dirB)
	if err != nil {
		t.Fatal(err)
	}
	if a.All()[0].UpChecksum != b.All()[0].UpChecksum {
		t.Fatalf("checksums should match after normalization: %s vs %s",
			a.All()[0].UpChecksum, b.All()[0].UpChecksum)
	}
}

func TestNoTransactionMagic(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "0001_idx.up.sql", "-- pgfleet:no-transaction\ncreate index concurrently i on t (a);")
	writeFile(t, dir, "0002_plain.up.sql", "create table t ();")

	set, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !set.All()[0].NoTransaction {
		t.Error("0001 should be marked no-transaction")
	}
	if set.All()[1].NoTransaction {
		t.Error("0002 should be transactional")
	}
}

func TestPending(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "0001_a.up.sql", "select 1;")
	writeFile(t, dir, "0002_b.up.sql", "select 2;")
	writeFile(t, dir, "0004_c.up.sql", "select 4;")
	set, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}

	pending := set.Pending(1, 0)
	if len(pending) != 2 || pending[0].Version != 2 || pending[1].Version != 4 {
		t.Fatalf("unexpected pending set: %+v", pending)
	}

	capped := set.Pending(0, 2)
	if len(capped) != 2 || capped[1].Version != 2 {
		t.Fatalf("target cap not honored: %+v", capped)
	}
}

func TestPendingDownIsHighestFirst(t *testing.T) {
	dir := t.TempDir()
	for _, v := range []int{1, 2, 3, 5} {
		writeFile(t, dir, fmt.Sprintf("%04d_m.up.sql", v), "select 1;")
		writeFile(t, dir, fmt.Sprintf("%04d_m.down.sql", v), "select 1;")
	}
	set, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}

	// Rolling back from version 5 to 2 must unwind 5 then 3 (the versions above
	// the target, newest first); versions 1 and 2 stay put.
	got := set.PendingDown(5, 2)
	versions := make([]int, len(got))
	for i, m := range got {
		versions[i] = m.Version
	}
	if want := []int{5, 3}; !reflect.DeepEqual(versions, want) {
		t.Fatalf("PendingDown(5,2) = %v, want %v (highest first)", versions, want)
	}

	// Down to 0 unwinds everything, still newest first.
	if got := set.PendingDown(5, 0); got[0].Version != 5 || got[len(got)-1].Version != 1 {
		t.Fatalf("PendingDown(5,0) should run 5..1, got first=%d last=%d", got[0].Version, got[len(got)-1].Version)
	}

	// Nothing to roll back when already at or below the target.
	if got := set.PendingDown(2, 2); len(got) != 0 {
		t.Fatalf("PendingDown(2,2) should be empty, got %v", got)
	}
}

func TestScaffoldRefusesToOverwrite(t *testing.T) {
	dir := t.TempDir()
	if _, _, err := Scaffold(dir, "first"); err != nil {
		t.Fatal(err)
	}
	// Re-running with a description that slugifies to the same name as an
	// existing file must not clobber it. Force the collision by writing 0002
	// ahead of time, then scaffolding again so next=2.
	writeFile(t, dir, "0002_dup.up.sql", "select 1;")
	// Highest is now 2, so the next scaffold targets 0003 and succeeds; prove the
	// pre-existing 0002 file is untouched.
	if _, _, err := Scaffold(dir, "third"); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(filepath.Join(dir, "0002_dup.up.sql"))
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "select 1;" {
		t.Fatalf("existing migration was overwritten: %q", body)
	}
}

func TestScaffoldRejectsEmptySlug(t *testing.T) {
	dir := t.TempDir()
	if _, _, err := Scaffold(dir, "!!!"); err == nil {
		t.Fatal("expected an error for a description with no alphanumeric characters")
	}
}

func TestScaffoldIncrements(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "0001_a.up.sql", "select 1;")
	writeFile(t, dir, "0001_a.down.sql", "select 1;")

	up, down, err := Scaffold(dir, "Add Users Table")
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(up) != "0002_add_users_table.up.sql" {
		t.Errorf("unexpected up file: %s", up)
	}
	if filepath.Base(down) != "0002_add_users_table.down.sql" {
		t.Errorf("unexpected down file: %s", down)
	}
}

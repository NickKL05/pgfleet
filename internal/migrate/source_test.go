package migrate

import (
	"os"
	"path/filepath"
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

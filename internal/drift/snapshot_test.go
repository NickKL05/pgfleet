package drift

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/NickKL05/pgfleet/internal/drift/fingerprint"
	"github.com/NickKL05/pgfleet/internal/drift/model"
)

func sampleSnapshot() *Snapshot {
	s := model.NewSchema("tenant_template")
	s.Tables["users"] = &model.Table{
		Name: "users",
		Columns: []*model.Column{
			{Name: "id", Position: 1, Type: "bigint", NotNull: true, Identity: "a"},
			{Name: "email", Position: 2, Type: "text", NotNull: true},
		},
		Constraints: map[string]*model.Constraint{
			"users_pkey": {Name: "users_pkey", Type: "p", Definition: "PRIMARY KEY (id)"},
		},
		Indexes: map[string]*model.Index{},
	}
	fp := fingerprint.Compute(s.Flatten(model.Options{}))
	return &Snapshot{FormatVersion: snapshotFormatVersion, Fingerprint: fp}
}

func TestSnapshotRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "schema.lock.json")
	snap := sampleSnapshot()

	if err := WriteSnapshot(path, snap); err != nil {
		t.Fatal(err)
	}
	ref, err := ReadSnapshot(path)
	if err != nil {
		t.Fatal(err)
	}
	if ref.Fingerprint.Hash != snap.Fingerprint.Hash {
		t.Fatalf("round-trip hash mismatch: %s vs %s", ref.Fingerprint.Hash, snap.Fingerprint.Hash)
	}
}

func TestSnapshotWriteIsDeterministic(t *testing.T) {
	dir := t.TempDir()
	p1 := filepath.Join(dir, "a.json")
	p2 := filepath.Join(dir, "b.json")

	if err := WriteSnapshot(p1, sampleSnapshot()); err != nil {
		t.Fatal(err)
	}
	if err := WriteSnapshot(p2, sampleSnapshot()); err != nil {
		t.Fatal(err)
	}
	a, _ := os.ReadFile(p1)
	b, _ := os.ReadFile(p2)
	if !bytes.Equal(a, b) {
		t.Fatal("snapshot output is not byte-for-byte deterministic")
	}
}

func TestReadSnapshotRejectsWrongVersion(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.json")
	if err := os.WriteFile(path, []byte(`{"format_version":999,"fingerprint":{"objects":[],"hash":"x"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadSnapshot(path); err == nil {
		t.Fatal("expected error for unknown format version")
	}
}

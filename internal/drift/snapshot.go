package drift

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/NickKL05/pgfleet/internal/drift/catalog"
	"github.com/NickKL05/pgfleet/internal/drift/fingerprint"
)

// snapshotFormatVersion is bumped if the on-disk shape changes.
const snapshotFormatVersion = 1

// Snapshot is the committed canonical reference written by drift snapshot and
// read back for snapshot-mode verification (spec 5.1). It contains no
// timestamps or schema name so it diffs cleanly in git and matches any tenant
// (R5.11).
type Snapshot struct {
	FormatVersion int                     `json:"format_version"`
	Fingerprint   fingerprint.Fingerprint `json:"fingerprint"`
}

// BuildSnapshot fingerprints the given source schema for use as a reference.
func BuildSnapshot(ctx context.Context, db catalog.Querier, schema string, opts Options) (*Snapshot, error) {
	ref, err := ReferenceFromSchema(ctx, db, schema, opts)
	if err != nil {
		return nil, err
	}
	return &Snapshot{FormatVersion: snapshotFormatVersion, Fingerprint: ref.Fingerprint}, nil
}

// WriteSnapshot writes a snapshot as indented JSON with a trailing newline. The
// object list is already sorted by (type, name), so output is deterministic.
func WriteSnapshot(path string, snap *Snapshot) error {
	data, err := json.MarshalIndent(snap, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("write snapshot %s: %w", path, err)
	}
	return nil
}

// ReadSnapshot loads a snapshot file and returns it as a Reference.
func ReadSnapshot(path string) (*Reference, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read snapshot %s: %w", path, err)
	}
	var snap Snapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return nil, fmt.Errorf("parse snapshot %s: %w", path, err)
	}
	if snap.FormatVersion != snapshotFormatVersion {
		return nil, fmt.Errorf("snapshot %s has format version %d, this build expects %d",
			path, snap.FormatVersion, snapshotFormatVersion)
	}
	return &Reference{Label: "snapshot " + path, Fingerprint: snap.Fingerprint}, nil
}

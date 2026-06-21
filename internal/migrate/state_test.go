package migrate

import "testing"

func TestHighestApplied(t *testing.T) {
	if got := HighestApplied(map[int]AppliedRecord{}); got != 0 {
		t.Errorf("empty set should be version 0, got %d", got)
	}
	applied := map[int]AppliedRecord{
		1: {Version: 1},
		5: {Version: 5},
		3: {Version: 3},
	}
	if got := HighestApplied(applied); got != 5 {
		t.Errorf("HighestApplied = %d, want 5", got)
	}
}

// TestCheckChecksums proves the drift guard: an applied migration whose file
// changed on disk is reported by its version, and the lowest such version wins
// so the operator sees the earliest divergence first (R4.1).
func TestCheckChecksums(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "0001_a.up.sql", "select 1;")
	writeFile(t, dir, "0002_b.up.sql", "select 2;")
	writeFile(t, dir, "0003_c.up.sql", "select 3;")
	set, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	all := set.All()

	// All checksums match what is on disk: no mismatch.
	applied := map[int]AppliedRecord{
		1: {Version: 1, Name: "a", Checksum: all[0].UpChecksum},
		2: {Version: 2, Name: "b", Checksum: all[1].UpChecksum},
		3: {Version: 3, Name: "c", Checksum: all[2].UpChecksum},
	}
	if v := checkChecksums(applied, set); v != 0 {
		t.Fatalf("matching checksums should report 0, got mismatch at %d", v)
	}

	// Tamper with two versions; the lowest tampered version is reported.
	applied[3] = AppliedRecord{Version: 3, Name: "c", Checksum: "deadbeef"}
	applied[2] = AppliedRecord{Version: 2, Name: "b", Checksum: "cafebabe"}
	if v := checkChecksums(applied, set); v != 2 {
		t.Fatalf("expected the lowest mismatching version (2), got %d", v)
	}

	// A migration that was applied but no longer has a file is not flagged here
	// (it cannot be checksum-compared); only known files are checked.
	delete(applied, 2)
	applied[99] = AppliedRecord{Version: 99, Name: "gone", Checksum: "whatever"}
	if v := checkChecksums(applied, set); v != 3 {
		t.Fatalf("expected mismatch at 3 with an unknown applied version present, got %d", v)
	}
}

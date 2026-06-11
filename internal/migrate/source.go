// Package migrate implements the fleet-wide migration runner: parsing the
// migration directory, the per-tenant state table, advisory locking, and the
// concurrent apply loop.
package migrate

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// noTransactionMagic on line 1 of an up file opts the migration out of
// transactional execution, which is required for statements such as
// CREATE INDEX CONCURRENTLY.
const noTransactionMagic = "-- pgfleet:no-transaction"

// fileNamePattern matches NNNN_description.(up|down).sql.
var fileNamePattern = regexp.MustCompile(`^(\d+)_(.+)\.(up|down)\.sql$`)

// Migration is a single versioned migration with its up and down SQL. The
// bytes are loaded once and shared across all tenant workers (R4.12).
type Migration struct {
	Version       int
	Name          string
	UpSQL         []byte
	DownSQL       []byte
	UpChecksum    string
	NoTransaction bool
	hasDown       bool
}

// HasDown reports whether a down file was present for this version.
func (m Migration) HasDown() bool { return m.hasDown }

// Set is the ordered, validated collection of migrations in a directory.
type Set struct {
	dir        string
	migrations []Migration
}

// Dir returns the source directory.
func (s *Set) Dir() string { return s.dir }

// All returns the migrations ordered by ascending version.
func (s *Set) All() []Migration { return s.migrations }

// Len returns the number of migrations.
func (s *Set) Len() int { return len(s.migrations) }

// Highest returns the greatest version present, or 0 when the set is empty.
func (s *Set) Highest() int {
	if len(s.migrations) == 0 {
		return 0
	}
	return s.migrations[len(s.migrations)-1].Version
}

// Pending returns migrations with a version greater than appliedVersion, up to
// and including target. A target of 0 means "all". Used to compute the work
// for one tenant given its current state.
func (s *Set) Pending(appliedVersion, target int) []Migration {
	var out []Migration
	for _, m := range s.migrations {
		if m.Version <= appliedVersion {
			continue
		}
		if target > 0 && m.Version > target {
			break
		}
		out = append(out, m)
	}
	return out
}

// Load reads and validates every migration file in dir. Duplicate versions are
// a hard error; gaps in the version sequence are allowed (spec 4.1).
func Load(dir string) (*Set, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read migrations dir %s: %w", dir, err)
	}

	type halves struct {
		name          string
		up, down      []byte
		upChecksum    string
		noTransaction bool
		hasUp, hasDn  bool
	}
	byVersion := map[int]*halves{}
	upSeen := map[int]string{}

	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		m := fileNamePattern.FindStringSubmatch(e.Name())
		if m == nil {
			continue
		}
		version, err := strconv.Atoi(m[1])
		if err != nil {
			return nil, fmt.Errorf("migration %s: invalid version: %w", e.Name(), err)
		}
		name, direction := m[2], m[3]

		raw, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			return nil, fmt.Errorf("read migration %s: %w", e.Name(), err)
		}
		normalized := normalize(raw)

		h := byVersion[version]
		if h == nil {
			h = &halves{name: name}
			byVersion[version] = h
		}
		switch direction {
		case "up":
			if prev, dup := upSeen[version]; dup {
				return nil, fmt.Errorf("duplicate migration version %04d: %s and %s", version, prev, e.Name())
			}
			upSeen[version] = e.Name()
			h.up = normalized
			h.upChecksum = checksum(normalized)
			h.noTransaction = hasNoTransactionMagic(normalized)
			h.name = name
			h.hasUp = true
		case "down":
			h.down = normalized
			h.hasDn = true
		}
	}

	versions := make([]int, 0, len(byVersion))
	for v, h := range byVersion {
		if !h.hasUp {
			return nil, fmt.Errorf("migration version %04d has a down file but no up file", v)
		}
		versions = append(versions, v)
	}
	sort.Ints(versions)

	set := &Set{dir: dir, migrations: make([]Migration, 0, len(versions))}
	for _, v := range versions {
		h := byVersion[v]
		set.migrations = append(set.migrations, Migration{
			Version:       v,
			Name:          h.name,
			UpSQL:         h.up,
			DownSQL:       h.down,
			UpChecksum:    h.upChecksum,
			NoTransaction: h.noTransaction,
			hasDown:       h.hasDn,
		})
	}
	return set, nil
}

// normalize converts CRLF to LF and strips trailing whitespace from every line
// so checksums are stable across platforms and editors (spec 4.2).
func normalize(raw []byte) []byte {
	s := strings.ReplaceAll(string(raw), "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	lines := strings.Split(s, "\n")
	for i, ln := range lines {
		lines[i] = strings.TrimRight(ln, " \t")
	}
	out := strings.Join(lines, "\n")
	out = strings.TrimRight(out, "\n")
	return []byte(out)
}

func checksum(normalized []byte) string {
	sum := sha256.Sum256(normalized)
	return hex.EncodeToString(sum[:])
}

func hasNoTransactionMagic(normalized []byte) bool {
	s := string(normalized)
	line1 := s
	if idx := strings.IndexByte(s, '\n'); idx >= 0 {
		line1 = s[:idx]
	}
	return strings.TrimSpace(line1) == noTransactionMagic
}

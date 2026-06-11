package migrate

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var slugCleaner = regexp.MustCompile(`[^a-z0-9]+`)

// Scaffold creates an up/down migration pair in dir for the given description,
// using the next version after the highest already present. It returns the two
// created file paths.
func Scaffold(dir, description string) (upPath, downPath string, err error) {
	set, err := Load(dir)
	if err != nil {
		// A missing directory is fine for the very first migration.
		if !errors.Is(err, fs.ErrNotExist) {
			return "", "", err
		}
		if mkErr := os.MkdirAll(dir, 0o755); mkErr != nil {
			return "", "", mkErr
		}
		set = &Set{dir: dir}
	}

	next := set.Highest() + 1
	slug := slugify(description)
	if slug == "" {
		return "", "", fmt.Errorf("description must contain at least one alphanumeric character")
	}

	base := fmt.Sprintf("%04d_%s", next, slug)
	upPath = filepath.Join(dir, base+".up.sql")
	downPath = filepath.Join(dir, base+".down.sql")

	upBody := fmt.Sprintf("-- %04d %s (up)\n-- Runs inside the tenant schema with search_path already set.\n\n", next, description)
	downBody := fmt.Sprintf("-- %04d %s (down)\n\n", next, description)

	if err := writeNew(upPath, upBody); err != nil {
		return "", "", err
	}
	if err := writeNew(downPath, downBody); err != nil {
		return "", "", err
	}
	return upPath, downPath, nil
}

func slugify(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = slugCleaner.ReplaceAllString(s, "_")
	return strings.Trim(s, "_")
}

// writeNew refuses to overwrite an existing file.
func writeNew(path, body string) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("create %s: %w", path, err)
	}
	_, writeErr := f.WriteString(body)
	closeErr := f.Close()
	if writeErr != nil {
		return fmt.Errorf("write %s: %w", path, writeErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close %s: %w", path, closeErr)
	}
	return nil
}

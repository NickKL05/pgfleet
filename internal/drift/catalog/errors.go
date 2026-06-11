package catalog

import "fmt"

// wrap annotates a catalog read error with the stage it came from, or returns
// nil when err is nil so it can wrap rows.Err() directly.
func wrap(stage string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("catalog read %s: %w", stage, err)
}

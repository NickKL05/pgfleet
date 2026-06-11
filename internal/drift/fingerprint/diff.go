package fingerprint

import "sort"

// Classification of one object-level difference (R5.6).
const (
	Missing  = "missing"  // present in reference, absent in tenant
	Extra    = "extra"    // present in tenant only
	Modified = "modified" // present in both, hash differs
)

// Difference is one object-level drift finding.
type Difference struct {
	Type  string `json:"type"`
	Name  string `json:"name"`
	Class string `json:"class"`
}

// Diff compares a tenant fingerprint against a reference, returning the
// object-level differences sorted by (type, name). An empty result means the
// tenant matches the reference exactly.
func Diff(reference, tenant Fingerprint) []Difference {
	ref := index(reference)
	ten := index(tenant)

	var diffs []Difference
	for key, r := range ref {
		t, ok := ten[key]
		if !ok {
			diffs = append(diffs, Difference{Type: r.Type, Name: r.Name, Class: Missing})
			continue
		}
		if t.Hash != r.Hash {
			diffs = append(diffs, Difference{Type: r.Type, Name: r.Name, Class: Modified})
		}
	}
	for key, t := range ten {
		if _, ok := ref[key]; !ok {
			diffs = append(diffs, Difference{Type: t.Type, Name: t.Name, Class: Extra})
		}
	}

	sort.Slice(diffs, func(i, j int) bool {
		if diffs[i].Type != diffs[j].Type {
			return diffs[i].Type < diffs[j].Type
		}
		return diffs[i].Name < diffs[j].Name
	})
	return diffs
}

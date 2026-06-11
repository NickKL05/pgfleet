package report

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"text/tabwriter"
)

// ObjectDiff is one object-level difference for drift output (R5.6). Class is
// one of "missing", "extra", or "modified". For a modified column or
// constraint, Changes explains which fields differ.
type ObjectDiff struct {
	Type    string        `json:"type"`
	Name    string        `json:"name"`
	Class   string        `json:"class"`
	Changes []FieldChange `json:"changes,omitempty"`
}

// FieldChange is one field-level difference within a modified object, e.g. a
// column whose type changed from text to character varying(100).
type FieldChange struct {
	Field string `json:"field"`
	From  string `json:"from"`
	To    string `json:"to"`
}

// TenantDrift is the drift outcome for one tenant.
type TenantDrift struct {
	Schema      string       `json:"schema"`
	Drifted     bool         `json:"drifted"`
	Differences []ObjectDiff `json:"differences,omitempty"`
}

// DriftReport is the machine-readable result of drift verify.
type DriftReport struct {
	Reference string         `json:"reference"`
	Tenants   []TenantDrift  `json:"tenants"`
	Summary   map[string]int `json:"summary"`
}

// NewDriftReport seeds a drift report naming its reference.
func NewDriftReport(reference string) *DriftReport {
	return &DriftReport{Reference: reference, Summary: map[string]int{"clean": 0, "drifted": 0}}
}

// Add records one tenant outcome.
func (r *DriftReport) Add(t TenantDrift) {
	r.Tenants = append(r.Tenants, t)
	if t.Drifted {
		r.Summary["drifted"]++
	} else {
		r.Summary["clean"]++
	}
}

// Drifted reports whether any tenant drifted (drives exit code 1).
func (r *DriftReport) Drifted() bool { return r.Summary["drifted"] > 0 }

// RenderDriftJSON writes the report as indented JSON.
func RenderDriftJSON(w io.Writer, r *DriftReport) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(r)
}

// RenderDriftHuman writes a compact human summary: one line per drifted tenant
// with its object differences, then a final tally. Clean tenants are collapsed
// into the tally so a healthy fleet stays quiet.
func RenderDriftHuman(w io.Writer, r *DriftReport) error {
	drifted := make([]TenantDrift, 0)
	for _, t := range r.Tenants {
		if t.Drifted {
			drifted = append(drifted, t)
		}
	}
	sort.Slice(drifted, func(i, j int) bool { return drifted[i].Schema < drifted[j].Schema })

	if len(drifted) == 0 {
		fmt.Fprintf(w, "No drift. %d tenants match %s.\n", len(r.Tenants), r.Reference)
		return nil
	}

	for _, t := range drifted {
		fmt.Fprintf(w, "%s: %d difference(s)\n", t.Schema, len(t.Differences))
		tw := tabwriter.NewWriter(w, 0, 2, 2, ' ', 0)
		for _, d := range t.Differences {
			fmt.Fprintf(tw, "  %s\t%s\t%s\n", d.Class, d.Type, d.Name)
		}
		if err := tw.Flush(); err != nil {
			return err
		}
	}
	fmt.Fprintf(w, "\n%d of %d tenants drifted from %s.\n",
		r.Summary["drifted"], len(r.Tenants), r.Reference)
	return nil
}

// RenderDiffHuman writes the detailed per-object differences for each tenant,
// including the field-level changes of modified columns and constraints (R5.6).
// It is the renderer for drift diff, where the user asked about specific
// tenants and wants the full explanation rather than a summary.
func RenderDiffHuman(w io.Writer, r *DriftReport) error {
	for _, t := range r.Tenants {
		if len(t.Differences) == 0 {
			fmt.Fprintf(w, "%s: no differences from %s\n", t.Schema, r.Reference)
			continue
		}
		fmt.Fprintf(w, "%s: %d difference(s) from %s\n", t.Schema, len(t.Differences), r.Reference)
		for _, d := range t.Differences {
			fmt.Fprintf(w, "  %-8s %s %s\n", d.Class, d.Type, d.Name)
			for _, ch := range d.Changes {
				fmt.Fprintf(w, "      %s: %s -> %s\n", ch.Field, quoteEmpty(ch.From), quoteEmpty(ch.To))
			}
		}
	}
	return nil
}

// quoteEmpty renders an empty field value as "(none)" so a default going from
// unset to a value reads clearly.
func quoteEmpty(s string) string {
	if s == "" {
		return "(none)"
	}
	return s
}

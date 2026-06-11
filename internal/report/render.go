package report

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"text/tabwriter"
)

// RenderJSON writes the report as indented JSON (R4.5).
func RenderJSON(w io.Writer, r *RunReport) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(r)
}

// RenderHuman writes an aligned table. Tenants are grouped by (to-version,
// status) so a fleet of identical tenants collapses to one line per group;
// failures are then expanded with their Postgres error (R4.8, R4.5).
func RenderHuman(w io.Writer, r *RunReport) error {
	type key struct {
		to     int
		status string
	}
	groups := map[key][]TenantResult{}
	var order []key
	for _, res := range r.Tenants {
		k := key{to: res.To, status: res.Status}
		if _, seen := groups[k]; !seen {
			order = append(order, k)
		}
		groups[k] = append(groups[k], res)
	}
	sort.Slice(order, func(i, j int) bool {
		if order[i].to != order[j].to {
			return order[i].to < order[j].to
		}
		return order[i].status < order[j].status
	})

	tw := tabwriter.NewWriter(w, 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, "VERSION\tSTATUS\tTENANTS")
	for _, k := range order {
		members := groups[k]
		example := ""
		if len(members) <= 3 {
			names := make([]string, len(members))
			for i, m := range members {
				names[i] = m.Schema
			}
			example = strings.Join(names, ", ")
		} else {
			example = fmt.Sprintf("%s, ... (%d total)", members[0].Schema, len(members))
		}
		fmt.Fprintf(tw, "%d\t%s\t%d  %s\n", k.to, k.status, len(members), example)
	}
	if err := tw.Flush(); err != nil {
		return err
	}

	// Expand failures with the exact error so a reviewer can act on them.
	var failures []TenantResult
	for _, res := range r.Tenants {
		if res.Error != "" {
			failures = append(failures, res)
		}
	}
	if len(failures) > 0 {
		fmt.Fprintln(w, "\nFailures:")
		for _, f := range failures {
			fmt.Fprintf(w, "  %s (at version %d): %s\n", f.Schema, f.To, f.Error)
		}
	}

	fmt.Fprintf(w, "\nrun %s  %d tenants  ", r.RunID, len(r.Tenants))
	summaryKeys := make([]string, 0, len(r.Summary))
	for k := range r.Summary {
		summaryKeys = append(summaryKeys, k)
	}
	sort.Strings(summaryKeys)
	parts := make([]string, 0, len(summaryKeys))
	for _, k := range summaryKeys {
		parts = append(parts, fmt.Sprintf("%s=%d", k, r.Summary[k]))
	}
	fmt.Fprintln(w, strings.Join(parts, " "))
	return nil
}

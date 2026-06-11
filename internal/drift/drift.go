// Package drift orchestrates the catalog read, model assembly, fingerprinting,
// and comparison that back the drift verify and snapshot commands.
package drift

import (
	"context"
	"fmt"
	"runtime"

	"golang.org/x/sync/errgroup"

	"github.com/NickKL05/pgfleet/internal/drift/catalog"
	"github.com/NickKL05/pgfleet/internal/drift/fingerprint"
	"github.com/NickKL05/pgfleet/internal/drift/model"
	"github.com/NickKL05/pgfleet/internal/report"
)

// Options controls a drift operation.
type Options struct {
	Model model.Options // ignore list, column-order, strict
}

// Reference is the canonical fingerprint every tenant is compared against,
// along with a human label describing where it came from.
type Reference struct {
	Label       string
	Fingerprint fingerprint.Fingerprint
}

// ReferenceFromSchema reads one template schema and fingerprints it (spec 5.1
// schema mode). The same model options are applied as for tenants so the
// comparison is apples to apples.
func ReferenceFromSchema(ctx context.Context, db catalog.Querier, schema string, opts Options) (*Reference, error) {
	fps, err := Fingerprints(ctx, db, []string{schema}, opts)
	if err != nil {
		return nil, err
	}
	fp, ok := fps[schema]
	if !ok {
		return nil, fmt.Errorf("reference schema %q not found", schema)
	}
	return &Reference{Label: "schema " + schema, Fingerprint: fp}, nil
}

// Models reads the catalog for the given schemas in one set-based pass and
// assembles a normalized model per schema.
func Models(ctx context.Context, db catalog.Querier, schemas []string) (map[string]*model.Schema, error) {
	rows, err := catalog.Read(ctx, db, schemas)
	if err != nil {
		return nil, err
	}
	return catalog.Assemble(rows, schemas), nil
}

// Fingerprints reads the catalog for the given schemas in one set-based pass,
// assembles a model per schema, and computes their fingerprints concurrently
// (parallel hashing keeps verify under the 5s target at 250 schemas, R5.5).
func Fingerprints(ctx context.Context, db catalog.Querier, schemas []string, opts Options) (map[string]fingerprint.Fingerprint, error) {
	models, err := Models(ctx, db, schemas)
	if err != nil {
		return nil, err
	}

	out := make(map[string]fingerprint.Fingerprint, len(schemas))
	results := make([]fingerprint.Fingerprint, len(schemas))

	g, _ := errgroup.WithContext(ctx)
	g.SetLimit(runtime.GOMAXPROCS(0))
	for i, schema := range schemas {
		i, schema := i, schema
		g.Go(func() error {
			s := models[schema]
			if s == nil {
				s = model.NewSchema(schema)
			}
			results[i] = fingerprint.Compute(s.Flatten(opts.Model))
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return nil, err
	}
	for i, schema := range schemas {
		out[schema] = results[i]
	}
	return out, nil
}

// Verify compares every tenant fingerprint against the reference and returns a
// drift report. Exit-code mapping is the caller's responsibility (R5.12).
func Verify(ctx context.Context, db catalog.Querier, tenants []string, ref *Reference, opts Options) (*report.DriftReport, error) {
	fps, err := Fingerprints(ctx, db, tenants, opts)
	if err != nil {
		return nil, err
	}

	rep := report.NewDriftReport(ref.Label)
	for _, schema := range tenants {
		fp := fps[schema]
		diffs := fingerprint.Diff(ref.Fingerprint, fp)
		rep.Add(report.TenantDrift{
			Schema:      schema,
			Drifted:     len(diffs) > 0,
			Differences: toReportDiffs(diffs),
		})
	}
	return rep, nil
}

func toReportDiffs(diffs []fingerprint.Difference) []report.ObjectDiff {
	if len(diffs) == 0 {
		return nil
	}
	out := make([]report.ObjectDiff, len(diffs))
	for i, d := range diffs {
		out[i] = report.ObjectDiff{Type: d.Type, Name: d.Name, Class: d.Class}
	}
	return out
}

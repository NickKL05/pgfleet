package drift

import (
	"context"
	"fmt"

	"github.com/NickKL05/pgfleet/internal/drift/catalog"
	"github.com/NickKL05/pgfleet/internal/drift/diffgen"
	"github.com/NickKL05/pgfleet/internal/drift/fingerprint"
	"github.com/NickKL05/pgfleet/internal/drift/model"
	"github.com/NickKL05/pgfleet/internal/report"
)

// ReferenceMode selects where the canonical reference comes from.
type ReferenceMode string

const (
	ModeSchema   ReferenceMode = "schema"
	ModeSnapshot ReferenceMode = "snapshot"
)

// ReferenceSpec describes the canonical reference without depending on the
// config package, keeping the layering one-directional.
type ReferenceSpec struct {
	Mode         ReferenceMode
	Schema       string // when Mode is schema
	SnapshotPath string // when Mode is snapshot
}

// Diff returns the object-level and field-level differences of each tenant
// against the reference. In schema mode the reference model is available, so
// modified columns and constraints get a field-level explanation (R5.6). In
// snapshot mode only per-object hashes are available, so differences are
// reported at object level without field detail.
func Diff(ctx context.Context, db catalog.Querier, tenants []string, spec ReferenceSpec, opts Options) (*report.DriftReport, error) {
	tenantModels, err := Models(ctx, db, tenants)
	if err != nil {
		return nil, err
	}

	switch spec.Mode {
	case ModeSchema:
		return diffAgainstSchema(ctx, db, tenants, tenantModels, spec.Schema, opts)
	case ModeSnapshot:
		return diffAgainstSnapshot(tenants, tenantModels, spec.SnapshotPath, opts)
	default:
		return nil, fmt.Errorf("unknown reference mode %q", spec.Mode)
	}
}

func diffAgainstSchema(ctx context.Context, db catalog.Querier, tenants []string, tenantModels map[string]*model.Schema, schema string, opts Options) (*report.DriftReport, error) {
	refModels, err := Models(ctx, db, []string{schema})
	if err != nil {
		return nil, err
	}
	refModel := refModels[schema]
	if refModel == nil {
		return nil, fmt.Errorf("reference schema %q not found", schema)
	}

	rep := report.NewDriftReport("schema " + schema)
	for _, t := range tenants {
		diffs := diffgen.Compare(refModel, modelOrEmpty(tenantModels[t], t), opts.Model)
		rep.Add(report.TenantDrift{Schema: t, Drifted: len(diffs) > 0, Differences: diffs})
	}
	return rep, nil
}

func diffAgainstSnapshot(tenants []string, tenantModels map[string]*model.Schema, path string, opts Options) (*report.DriftReport, error) {
	ref, err := ReadSnapshot(path)
	if err != nil {
		return nil, err
	}
	rep := report.NewDriftReport(ref.Label)
	for _, t := range tenants {
		fp := fingerprint.Compute(modelOrEmpty(tenantModels[t], t).Flatten(opts.Model))
		diffs := fingerprint.Diff(ref.Fingerprint, fp)
		rep.Add(report.TenantDrift{
			Schema:      t,
			Drifted:     len(diffs) > 0,
			Differences: objectLevel(diffs),
		})
	}
	return rep, nil
}

// objectLevel converts hash-level differences into object diffs without field
// detail, used when only a snapshot (per-object hashes) is available.
func objectLevel(diffs []fingerprint.Difference) []report.ObjectDiff {
	if len(diffs) == 0 {
		return nil
	}
	out := make([]report.ObjectDiff, len(diffs))
	for i, d := range diffs {
		out[i] = report.ObjectDiff{Type: d.Type, Name: d.Name, Class: d.Class}
	}
	return out
}

func modelOrEmpty(s *model.Schema, name string) *model.Schema {
	if s == nil {
		return model.NewSchema(name)
	}
	return s
}

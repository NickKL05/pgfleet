package catalog

import (
	"testing"

	"github.com/NickKL05/pgfleet/internal/drift/fingerprint"
	"github.com/NickKL05/pgfleet/internal/drift/model"
)

// rowsFor builds catalog rows for one schema describing the same users table,
// with schema-qualified definitions as Postgres would emit them.
func rowsFor(schema string) *Rows {
	seqDefault := "nextval('" + schema + ".users_id_seq'::regclass)"
	return &Rows{
		Columns: []ColumnRow{
			{Schema: schema, Table: "users", Name: "id", AttNum: 1, Type: "bigint", NotNull: true, Default: &seqDefault, Identity: "d"},
			{Schema: schema, Table: "users", Name: "email", AttNum: 2, Type: "character varying(255)", NotNull: true},
		},
		Constraints: []ConstraintRow{
			{Schema: schema, Table: "users", Name: "users_pkey", Contype: "p", Def: "PRIMARY KEY (id)"},
		},
		Indexes: []IndexRow{
			{Schema: schema, Table: "users", Name: "users_email_idx",
				Def: "CREATE INDEX users_email_idx ON " + schema + ".users USING btree (email)"},
		},
		Sequences: []SequenceRow{
			{Schema: schema, Name: "users_id_seq", DataType: "bigint", Start: 1, Min: 1, Max: 9223372036854775807, Increment: 1},
		},
	}
}

func TestAssembleStripsSchemaAndMatchesAcrossTenants(t *testing.T) {
	a := Assemble(rowsFor("tenant_42"), []string{"tenant_42"})["tenant_42"]
	b := Assemble(rowsFor("tenant_template"), []string{"tenant_template"})["tenant_template"]

	fpA := fingerprint.Compute(a.Flatten(model.Options{}))
	fpB := fingerprint.Compute(b.Flatten(model.Options{}))
	if fpA.Hash != fpB.Hash {
		t.Fatalf("tenants with identical structure should match:\n  %s\n  %s\n  diff=%v",
			fpA.Hash, fpB.Hash, fingerprint.Diff(fpB, fpA))
	}

	// The index definition must have lost its schema qualifier.
	idx := a.Tables["users"].Indexes["users_email_idx"]
	if idx == nil || idx.Definition != "CREATE INDEX users_email_idx ON users USING btree (email)" {
		t.Fatalf("index schema qualifier not stripped: %+v", idx)
	}
	// The regclass cast in the default must be preserved, only the schema gone.
	col := a.Tables["users"].Columns[0]
	if col.Default != "nextval('users_id_seq'::regclass)" {
		t.Fatalf("default not normalized as expected: %q", col.Default)
	}
}

func TestAssembleDetectsRealDrift(t *testing.T) {
	ref := Assemble(rowsFor("tenant_template"), []string{"tenant_template"})["tenant_template"]

	drifted := rowsFor("tenant_087")
	drifted.Indexes = nil // someone dropped the index
	tenant := Assemble(drifted, []string{"tenant_087"})["tenant_087"]

	diffs := fingerprint.Diff(
		fingerprint.Compute(ref.Flatten(model.Options{})),
		fingerprint.Compute(tenant.Flatten(model.Options{})),
	)
	if len(diffs) != 1 || diffs[0].Class != fingerprint.Missing || diffs[0].Name != "users_email_idx" {
		t.Fatalf("expected exactly one missing index, got %v", diffs)
	}
}

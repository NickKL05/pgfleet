package fingerprint

import (
	"testing"

	"github.com/NickKL05/pgfleet/internal/drift/model"
)

// usersSchema builds a small but representative schema model under the given
// name. Definitions are written as they would be after normalization (schema
// qualifier already stripped).
func usersSchema(name string) *model.Schema {
	s := model.NewSchema(name)
	s.Tables["users"] = &model.Table{
		Name: "users",
		Columns: []*model.Column{
			{Name: "id", Position: 1, Type: "bigint", NotNull: true, Identity: "a"},
			{Name: "email", Position: 2, Type: "text", NotNull: true},
			{Name: "display_name", Position: 3, Type: "text", NotNull: true},
		},
		Constraints: map[string]*model.Constraint{
			"users_pkey": {Name: "users_pkey", Type: "p", Definition: "PRIMARY KEY (id)"},
		},
		Indexes: map[string]*model.Index{
			"users_created_at_idx": {Name: "users_created_at_idx", Definition: "CREATE INDEX users_created_at_idx ON users USING btree (created_at)"},
		},
	}
	return s
}

func TestIdenticalSchemasHashEqualAcrossNames(t *testing.T) {
	a := Compute(usersSchema("tenant_42").Flatten(model.Options{}))
	b := Compute(usersSchema("tenant_template").Flatten(model.Options{}))

	if a.Hash != b.Hash {
		t.Fatalf("structurally identical schemas hashed differently:\n  %s\n  %s", a.Hash, b.Hash)
	}
	if len(Diff(b, a)) != 0 {
		t.Fatalf("expected no diff between identical schemas, got %v", Diff(b, a))
	}
}

func TestDiffClassifies(t *testing.T) {
	ref := Compute(usersSchema("tenant_template").Flatten(model.Options{}))

	// Drift the tenant: drop the index (missing), change a column type
	// (modified), and add a rogue table (extra).
	tenant := usersSchema("tenant_087")
	delete(tenant.Tables["users"].Indexes, "users_created_at_idx")
	tenant.Tables["users"].Columns[1].Type = "character varying(255)"
	tenant.Tables["audit"] = &model.Table{Name: "audit", Columns: []*model.Column{{Name: "id", Position: 1, Type: "bigint"}}}

	diffs := Diff(ref, Compute(tenant.Flatten(model.Options{})))

	got := map[string]string{}
	for _, d := range diffs {
		got[d.Type+":"+d.Name] = d.Class
	}
	if got["index:users_created_at_idx"] != Missing {
		t.Errorf("expected missing index, got %q", got["index:users_created_at_idx"])
	}
	if got["column:users.email"] != Modified {
		t.Errorf("expected modified column, got %q", got["column:users.email"])
	}
	if got["table:audit"] != Extra {
		t.Errorf("expected extra table, got %q", got["table:audit"])
	}
}

func TestIgnoreList(t *testing.T) {
	s := usersSchema("tenant_42")
	s.Tables["_pgfleet_migrations"] = &model.Table{
		Name:    "_pgfleet_migrations",
		Columns: []*model.Column{{Name: "version", Position: 1, Type: "integer"}},
	}

	withMig := Compute(s.Flatten(model.Options{}))
	ignoring := Compute(s.Flatten(model.Options{Ignore: []string{"table:_pgfleet_migrations"}}))

	if withMig.Hash == ignoring.Hash {
		t.Fatal("ignoring the migrations table should change the fingerprint")
	}
	for _, o := range ignoring.Objects {
		if o.Name == "_pgfleet_migrations" || o.Name == "_pgfleet_migrations.version" {
			t.Errorf("ignored object leaked into fingerprint: %s", o.Name)
		}
	}
	// The reference fingerprint of a schema without the migrations table should
	// equal the ignoring fingerprint of one with it.
	clean := Compute(usersSchema("tenant_template").Flatten(model.Options{Ignore: []string{"table:_pgfleet_migrations"}}))
	if clean.Hash != ignoring.Hash {
		t.Errorf("ignore did not normalize away the migrations table cleanly")
	}
}

func TestIgnoreColumnOrder(t *testing.T) {
	a := usersSchema("tenant_a")
	b := usersSchema("tenant_b")
	// Swap two columns' positions in b (same columns, different order).
	b.Tables["users"].Columns[1].Position = 3
	b.Tables["users"].Columns[2].Position = 2

	strict := model.Options{}
	if Compute(a.Flatten(strict)).Hash == Compute(b.Flatten(strict)).Hash {
		t.Fatal("column order should be significant by default")
	}

	relaxed := model.Options{IgnoreColumnOrder: true}
	if Compute(a.Flatten(relaxed)).Hash != Compute(b.Flatten(relaxed)).Hash {
		t.Fatal("--ignore-column-order should make differing positions hash equal")
	}
}

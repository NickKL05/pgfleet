package diffgen

import (
	"testing"

	"github.com/NickKL05/pgfleet/internal/drift/model"
	"github.com/NickKL05/pgfleet/internal/report"
)

func usersSchema(name string) *model.Schema {
	s := model.NewSchema(name)
	s.Tables["users"] = &model.Table{
		Name: "users",
		Columns: []*model.Column{
			{Name: "id", Position: 1, Type: "bigint", NotNull: true, Identity: "a"},
			{Name: "display_name", Position: 2, Type: "text", NotNull: true},
		},
		Constraints: map[string]*model.Constraint{
			"users_pkey": {Name: "users_pkey", Type: "p", Definition: "PRIMARY KEY (id)"},
		},
		Indexes: map[string]*model.Index{
			"users_name_idx": {Name: "users_name_idx", Definition: "CREATE INDEX users_name_idx ON users USING btree (display_name)"},
		},
	}
	return s
}

func findChange(changes []report.FieldChange, field string) (report.FieldChange, bool) {
	for _, c := range changes {
		if c.Field == field {
			return c, true
		}
	}
	return report.FieldChange{}, false
}

func TestCompareModifiedColumnFieldLevel(t *testing.T) {
	ref := usersSchema("tenant_template")
	ten := usersSchema("tenant_142")
	ten.Tables["users"].Columns[1].Type = "character varying(100)"
	ten.Tables["users"].Columns[1].NotNull = false

	diffs := Compare(ref, ten, model.Options{})

	var col *report.ObjectDiff
	for i := range diffs {
		if diffs[i].Type == model.KindColumn && diffs[i].Name == "users.display_name" {
			col = &diffs[i]
		}
	}
	if col == nil {
		t.Fatalf("expected a modified column diff, got %+v", diffs)
	}
	if col.Class != Modified {
		t.Fatalf("expected modified, got %s", col.Class)
	}
	if ch, ok := findChange(col.Changes, "type"); !ok || ch.From != "text" || ch.To != "character varying(100)" {
		t.Errorf("type change wrong: %+v", col.Changes)
	}
	if ch, ok := findChange(col.Changes, "nullability"); !ok || ch.From != "not null" || ch.To != "nullable" {
		t.Errorf("nullability change wrong: %+v", col.Changes)
	}
}

func TestCompareModifiedConstraint(t *testing.T) {
	ref := usersSchema("tenant_template")
	ten := usersSchema("tenant_x")
	ten.Tables["users"].Constraints["users_pkey"].Definition = "PRIMARY KEY (id, display_name)"

	diffs := Compare(ref, ten, model.Options{})
	var con *report.ObjectDiff
	for i := range diffs {
		if diffs[i].Type == model.KindConstraint {
			con = &diffs[i]
		}
	}
	if con == nil || con.Class != Modified {
		t.Fatalf("expected modified constraint, got %+v", diffs)
	}
	if ch, ok := findChange(con.Changes, "definition"); !ok || ch.To != "PRIMARY KEY (id, display_name)" {
		t.Errorf("definition change wrong: %+v", con.Changes)
	}
}

func TestCompareMissingAndExtra(t *testing.T) {
	ref := usersSchema("tenant_template")
	ten := usersSchema("tenant_087")
	delete(ten.Tables["users"].Indexes, "users_name_idx")                                                                  // missing
	ten.Tables["audit"] = &model.Table{Name: "audit", Columns: []*model.Column{{Name: "id", Position: 1, Type: "bigint"}}} // extra

	diffs := Compare(ref, ten, model.Options{})
	classes := map[string]string{}
	for _, d := range diffs {
		classes[d.Type+":"+d.Name] = d.Class
	}
	if classes["index:users_name_idx"] != Missing {
		t.Errorf("expected missing index, got %q", classes["index:users_name_idx"])
	}
	if classes["table:audit"] != Extra {
		t.Errorf("expected extra table, got %q", classes["table:audit"])
	}
}

func TestCompareNoDiff(t *testing.T) {
	if diffs := Compare(usersSchema("a"), usersSchema("b"), model.Options{}); len(diffs) != 0 {
		t.Fatalf("identical schemas should not diff, got %+v", diffs)
	}
}

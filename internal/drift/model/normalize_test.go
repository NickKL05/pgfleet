package model

import "testing"

func TestNormalizeType(t *testing.T) {
	cases := map[string]string{
		"varchar":                  "character varying",
		"varchar(100)":             "character varying(100)",
		"int4":                     "integer",
		"int8":                     "bigint",
		"bool":                     "boolean",
		"timestamptz":              "timestamp with time zone",
		"numeric(10,2)":            "numeric(10,2)",
		"text":                     "text",
		"varchar[]":                "character varying[]",
		"character varying(255)":   "character varying(255)",
		"timestamp with time zone": "timestamp with time zone",
	}
	for in, want := range cases {
		if got := NormalizeType(in); got != want {
			t.Errorf("NormalizeType(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestNormalizeDefault(t *testing.T) {
	cases := map[string]string{
		"'x'::text":                         "'x'",
		"'active'::character varying":       "'active'",
		"'y'::text ":                        "'y'",
		"now()":                             "now()",
		"nextval('users_id_seq'::regclass)": "nextval('users_id_seq'::regclass)",
		"  0  ":                             "0",
		"":                                  "",
	}
	for in, want := range cases {
		if got := NormalizeDefault(in); got != want {
			t.Errorf("NormalizeDefault(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestStripSchema(t *testing.T) {
	cases := []struct {
		def, schema, want string
	}{
		{"CREATE INDEX i ON tenant_42.users USING btree (created_at)", "tenant_42", "CREATE INDEX i ON users USING btree (created_at)"},
		{`CREATE INDEX i ON "tenant_42".users (a)`, "tenant_42", "CREATE INDEX i ON users (a)"},
		{"FOREIGN KEY (org_id) REFERENCES public.orgs(id)", "tenant_42", "FOREIGN KEY (org_id) REFERENCES public.orgs(id)"},
		{"REFERENCES tenant_42.parent(id)", "tenant_42", "REFERENCES parent(id)"},
		// A schema whose name is a prefix of another must not be stripped from it.
		{"tenant_420.users", "tenant_42", "tenant_420.users"},
		// A schema whose name is a suffix of another identifier must not be
		// stripped from it: "user." inside "power_user." stays intact.
		{"power_user.col", "user", "power_user.col"},
		{"REFERENCES power_user.col, user.x", "user", "REFERENCES power_user.col, x"},
		// Two qualifiers in one definition are both stripped.
		{"tenant_1.a = tenant_1.b", "tenant_1", "a = b"},
	}
	for _, c := range cases {
		if got := StripSchema(c.def, c.schema); got != c.want {
			t.Errorf("StripSchema(%q, %q) = %q, want %q", c.def, c.schema, got, c.want)
		}
	}
}

package pgutil

import "testing"

func TestQuoteIdent(t *testing.T) {
	cases := map[string]string{
		"tenant_42":      `"tenant_42"`,
		"public":         `"public"`,
		`weird"name`:     `"weird""name"`,
		"tenant_archive": `"tenant_archive"`,
	}
	for in, want := range cases {
		if got := QuoteIdent(in); got != want {
			t.Errorf("QuoteIdent(%q) = %q, want %q", in, got, want)
		}
	}
}

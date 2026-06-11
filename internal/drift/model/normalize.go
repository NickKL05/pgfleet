// Package model holds the normalized structural model of a schema and the rules
// that make two structurally identical tenant schemas compare equal regardless
// of their schema name, whitespace, or cast noise.
package model

import (
	"regexp"
	"strings"
)

// typeAliases maps PostgreSQL internal or shorthand type names to their
// canonical SQL spelling so that, for example, varchar and character varying
// hash equal (spec 5.5). format_type already returns canonical names for live
// catalogs; this is the safety net for hand-written or snapshot input.
var typeAliases = map[string]string{
	"varchar":     "character varying",
	"char":        "character",
	"bpchar":      "character",
	"int":         "integer",
	"int4":        "integer",
	"int2":        "smallint",
	"int8":        "bigint",
	"bool":        "boolean",
	"float4":      "real",
	"float8":      "double precision",
	"decimal":     "numeric",
	"timestamptz": "timestamp with time zone",
	"timetz":      "time with time zone",
}

var whitespaceRun = regexp.MustCompile(`\s+`)

// castAfterLiteral matches a redundant textual cast immediately following a
// single-quoted string literal, e.g. the ::text in 'x'::text. Only textual
// target types are stripped so meaningful casts such as ::regclass inside
// nextval(...) or ::jsonb are preserved. Longer type names come first so the
// alternation does not match a prefix.
var castAfterLiteral = regexp.MustCompile(`('(?:[^']|'')*')::(?:character varying|character|varchar|bpchar|char|text)(\(\d+\))?`)

// NormalizeWhitespace collapses runs of whitespace to a single space and trims.
func NormalizeWhitespace(s string) string {
	return strings.TrimSpace(whitespaceRun.ReplaceAllString(s, " "))
}

// NormalizeType canonicalizes a type name, preserving any length modifier and
// array suffix. "varchar(100)" becomes "character varying(100)".
func NormalizeType(t string) string {
	t = strings.TrimSpace(t)
	if t == "" {
		return t
	}

	// Peel off an array suffix such as "[]" or "[3]".
	arraySuffix := ""
	for strings.HasSuffix(t, "]") {
		if i := strings.LastIndex(t, "["); i >= 0 {
			arraySuffix = t[i:] + arraySuffix
			t = strings.TrimSpace(t[:i])
		} else {
			break
		}
	}

	// Peel off a length/precision modifier such as "(100)" or "(10,2)".
	mod := ""
	if i := strings.Index(t, "("); i >= 0 && strings.HasSuffix(t, ")") {
		mod = t[i:]
		t = strings.TrimSpace(t[:i])
	}

	if canonical, ok := typeAliases[strings.ToLower(t)]; ok {
		t = canonical
	}
	return t + mod + arraySuffix
}

// NormalizeDefault normalizes a default expression: whitespace is collapsed and
// redundant casts on string literals are removed so 'x'::text and 'x' compare
// equal (R5.2).
func NormalizeDefault(expr string) string {
	if expr == "" {
		return ""
	}
	expr = castAfterLiteral.ReplaceAllString(expr, "$1")
	return NormalizeWhitespace(expr)
}

// StripSchema removes the given schema name used as a qualifier from a
// definition so that tenant_42.users and tenant_template.users compare equal
// (R5.1). Both bare and double-quoted forms are handled. Only the named schema
// is stripped, so references to shared schemas such as public, or to schemas
// outside the tenant set, are preserved (spec 5.5).
func StripSchema(def, schema string) string {
	if schema == "" || def == "" {
		return def
	}
	def = strings.ReplaceAll(def, `"`+schema+`".`, "")
	def = strings.ReplaceAll(def, schema+".", "")
	return def
}

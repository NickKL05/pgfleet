package discovery

import (
	"reflect"
	"testing"
)

func TestApplyExcludes(t *testing.T) {
	names := []string{"tenant_1", "tenant_2", "tenant_template", "tenant_archive_2024", "tenant_archive_old"}
	excludes := []string{"tenant_template", "tenant_archive_%"}

	got := applyExcludes(names, excludes)
	want := []string{"tenant_1", "tenant_2"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("applyExcludes = %v, want %v", got, want)
	}
}

func TestFilterGlob(t *testing.T) {
	names := []string{"tenant_1", "tenant_10", "tenant_2", "tenant_template"}

	got, err := Filter(names, "tenant_1*")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"tenant_1", "tenant_10"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Filter = %v, want %v", got, want)
	}
}

func TestFilterEmptyReturnsAll(t *testing.T) {
	names := []string{"a", "b"}
	got, err := Filter(names, "")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, names) {
		t.Fatalf("Filter with empty glob = %v, want %v", got, names)
	}
}

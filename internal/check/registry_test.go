package check

import (
	"slices"
	"testing"
)

func TestRegistryAll(t *testing.T) {
	r := NewRegistry(stubCheck{name: "a"}, stubCheck{name: "b"})
	if len(r.All()) != 2 {
		t.Errorf("len(All()) = %d, want 2", len(r.All()))
	}
}

func TestRegistryGet(t *testing.T) {
	r := NewRegistry(stubCheck{name: "a"})

	if _, ok := r.Get("a"); !ok {
		t.Error(`Get("a") ok = false, want true`)
	}
	if _, ok := r.Get("missing"); ok {
		t.Error(`Get("missing") ok = true, want false`)
	}
}

func TestRegistryNames(t *testing.T) {
	r := NewRegistry(stubCheck{name: "b"}, stubCheck{name: "a"})
	if got := r.Names(); !slices.Equal(got, []string{"b", "a"}) {
		t.Errorf("Names() = %v, want [b a]", got)
	}
}

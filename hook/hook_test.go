package hook

import (
	"errors"
	"fmt"
	"testing"

	"ontology/snapshot"
)

func allow(*snapshot.Snapshot) (bool, string)      { return true, "" }
func deny(reason string) Check {
	return func(*snapshot.Snapshot) (bool, string) { return false, reason }
}

func reg(t *testing.T, r *Registry, name, typ string, phase Phase, check Check) {
	t.Helper()
	h, err := New(name, typ, phase, 0, check)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Register(h); err != nil {
		t.Fatal(err)
	}
}

func TestNewErrors(t *testing.T) {
	cases := []struct {
		name      string
		hname     string
		applies   string
		phase     Phase
		check     Check
		wantError error
	}{
		{"empty name", "", "task", PhasePre, allow, ErrEmptyName},
		{"nil check", "h", "task", PhasePre, nil, ErrNilCheck},
		{"bad phase", "h", "task", Phase(9), allow, nil},
		{"valid any", "h", "", PhasePost, allow, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h, err := New(tc.hname, tc.applies, tc.phase, 0, tc.check)
			if tc.wantError != nil {
				if !errors.Is(err, tc.wantError) {
					t.Fatalf("err = %v, want %v", err, tc.wantError)
				}
				return
			}
			if tc.name == "bad phase" {
				if err == nil {
					t.Fatal("expected invalid phase error")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tc.name == "valid any" && h.AppliesTo() != ApplyAny {
				t.Fatalf("empty appliesTo defaulted to %q, want %q", h.AppliesTo(), ApplyAny)
			}
		})
	}
}

func TestRegisterAndMatching(t *testing.T) {
	r := NewRegistry()
	reg(t, r, "g", ApplyAny, PhasePre, deny("g"))
	for _, spec := range []struct {
		typ    string
		nHooks int
	}{
		{"task", 2},
		{"job", 1},
	} {
		for i := 0; i < spec.nHooks; i++ {
			reg(t, r, fmt.Sprintf("%s-%d", spec.typ, i), spec.typ, PhasePre, deny("x"))
		}
	}

	cases := []struct {
		typ        string
		wantHooks  int
		wantVisits int
	}{
		{"task", 3, 3}, // 2 in task bucket + 1 in ApplyAny
		{"job", 2, 2}, // 1 in job bucket + 1 in ApplyAny
		{"other", 1, 1}, // ApplyAny only
	}
	for _, tc := range cases {
		got := r.Matching(tc.typ)
		if len(got) != tc.wantHooks {
			t.Fatalf("%s: matched %d hooks, want %d", tc.typ, len(got), tc.wantHooks)
		}
		if r.LookupVisits() != tc.wantVisits {
			t.Fatalf("%s: visits = %d, want %d", tc.typ, r.LookupVisits(), tc.wantVisits)
		}
	}

	// Duplicate names are rejected.
	dup, _ := New("g", ApplyAny, PhasePre, 0, allow)
	if err := r.Register(dup); !errors.Is(err, ErrDupName) {
		t.Fatalf("dup register err = %v, want ErrDupName", err)
	}

	// A hook returns its check verdict.
	snap := snapshot.Freeze("task", "t1", nil)
	if pass, _ := r.Matching("task")[0].Run(snap); pass {
		t.Fatal("deny hook unexpectedly passed")
	}
}

func TestMatchingVisitsIndependentOfRegistrySize(t *testing.T) {
	measure := func(total int) int {
		r := NewRegistry()
		for i := 0; i < total; i++ {
			bucket := "noise"
			if i < 3 {
				bucket = "task"
			}
			r.MustRegister(fmt.Sprintf("h%d", i), bucket, PhasePre, 0, allow)
		}
		r.Matching("task")
		return r.LookupVisits()
	}
	v100 := measure(100)
	v10000 := measure(10000)
	if v100 != 3 || v10000 != 3 {
		t.Fatalf("visits at 100=%d, at 10000=%d, want 3 at both", v100, v10000)
	}
}

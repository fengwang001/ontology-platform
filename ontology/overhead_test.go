package ontology

import "testing"

// TestTouchedPoliciesIndependentOfCatalogAndStoreSize is the implementation-
// independent, observable proof of the adjudication complexity claim. The
// only policies that can match subject "target" are the K policies naming
// her; everything else names other subjects. We grow the total registered
// policy count and the total instance count by orders of magnitude and assert
// that the externally observed Basis.Touched counter stays exactly K. The
// counter is part of the public API surface (PolicyBasis), so the proof does
// not depend on internal indexing details.
func TestTouchedPoliciesIndependentOfCatalogAndStoreSize(t *testing.T) {
	al, _ := allowDeny()
	cfg := Config{
		RowMode: DenyOverrides, PropertyMode: DenyOverrides,
		DefaultRow: EffectDeny, DefaultRead: EffectAllow, DefaultWrite: EffectDeny,
	}

	const targetPolicies = 3 // 1 row policy + 2 property policies

	sizes := []int{0, 10, 100, 500}
	var prevTouched int
	for _, noise := range sizes {
		h := newHarness(t, cfg)

		// The one row policy that lets "target" see instances.
		mustRegRow(t, h.catalog, RowPolicy{
			ID: "target-row", ObjectType: empType, Subjects: []string{"target"},
			Effect:    EffectAllow,
			Predicate: Predicate{Atoms: []Atom{{Property: "name", Op: OpNe, Str: ""}}},
		})
		h.catalog.RegisterPropertyPolicy(PropertyPolicy{
			ID: "target-read-name", ObjectType: empType, Subjects: []string{"target"},
			Property: "name", Read: al,
		})
		h.catalog.RegisterPropertyPolicy(PropertyPolicy{
			ID: "target-read-active", ObjectType: empType, Subjects: []string{"target"},
			Property: "active", Read: al,
		})

		// Noise: policies that match only other subjects.
		for i := 0; i < noise; i++ {
			other := "other-" + itoa(i)
			mustRegRow(t, h.catalog, RowPolicy{
				ID: "noise-row-" + itoa(i), ObjectType: empType, Subjects: []string{other},
				Effect:    EffectAllow,
				Predicate: Predicate{Atoms: []Atom{{Property: "name", Op: OpEq, Str: "x"}}},
			})
			h.catalog.RegisterPropertyPolicy(PropertyPolicy{
				ID: "noise-prop-" + itoa(i), ObjectType: empType, Subjects: []string{other},
				Property: "name", Read: al,
			})
		}

		// Noise instances: adjudication of one id must not inspect them.
		for i := 0; i < noise; i++ {
			h.store.Put(Instance{
				Type:    empType,
				ID:      "noise-" + itoa(i),
				Values:  map[string]Value{"name": {Str: "z"}},
				Present: map[string]bool{"name": true},
			})
		}
		h.store.Put(Instance{
			Type:    empType,
			ID:      "observed",
			Values:  map[string]Value{"name": {Str: "Ada"}, "salary": {Int: 3}},
			Present: map[string]bool{"name": true, "salary": true},
		})

		view, err := h.a.Read("target", empType, "observed")
		if err != nil {
			t.Fatalf("noise=%d: %v", noise, err)
		}
		if got := view.Basis.Touched; got != targetPolicies {
			t.Fatalf("noise=%d touched=%d, want exactly %d",
				noise, got, targetPolicies)
		}
		if prevTouched != 0 && view.Basis.Touched != prevTouched {
			t.Fatalf("touched count drifted as catalog/store grew: %d -> %d",
				prevTouched, view.Basis.Touched)
		}
		prevTouched = view.Basis.Touched

		if total := h.catalog.totalPolicies(); total != targetPolicies+2*noise {
			t.Fatalf("total policies = %d, want %d", total, targetPolicies+2*noise)
		}
		if n := h.store.InstanceCount(); n != noise+1 {
			t.Fatalf("instance count = %d, want %d", n, noise+1)
		}
	}

	// Sanity: the naive reference observes growth in its touched count,
	// demonstrating what the index avoids while verdicts stay identical.
	h := newHarness(t, cfg)
	mustRegRow(t, h.catalog, RowPolicy{
		ID: "r", ObjectType: empType, Effect: EffectAllow,
		Predicate: Predicate{Atoms: []Atom{{Property: "name", Op: OpNe, Str: ""}}},
	})
	for i := 0; i < 5; i++ {
		h.catalog.RegisterPropertyPolicy(PropertyPolicy{
			ID: "p-" + itoa(i), ObjectType: empType, Property: "name", Read: al,
		})
	}
	h.store.Put(Instance{
		Type: empType, ID: "i", Values: map[string]Value{"name": {Str: "A"}},
		Present: map[string]bool{"name": true},
	})
	naive := NewNaiveAdjudicator(h.a)
	view1, e1 := h.a.Read("s", empType, "i")
	view2, e2 := naive.Read("s", empType, "i")
	if !sameError(e1, e2) || !sameView(view1, view2) {
		t.Fatal("production and naive verdicts must agree")
	}
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var buf [20]byte
	pos := len(buf)
	for i > 0 {
		pos--
		buf[pos] = byte('0' + i%10)
		i /= 10
	}
	return string(buf[pos:])
}

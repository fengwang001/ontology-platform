package cascade

import (
	"fmt"
	"testing"
)

func TestOperationCostIsLocal(t *testing.T) {
	measure := func(n int) Stats {
		c := NewController()
		for i := 0; i < n; i++ {
			mustCreate(t, c, CreateObjectInput{ID: fmt.Sprintf("u%d", i)})
		}
		mustCreate(t, c, CreateObjectInput{ID: "p"})
		mustCreate(t, c, CreateObjectInput{ID: "d1", Owners: []OwnerRef{{OwnerID: "p"}}})
		mustCreate(t, c, CreateObjectInput{ID: "d2", Owners: []OwnerRef{{OwnerID: "d1"}}})
		result := mustDelete(t, c, "p", Background, 1)
		if len(result.Removed) != 3 {
			t.Fatalf("removed=%v want [p d1 d2]", result.Removed)
		}
		return result.Stats
	}

	small := measure(50)
	large := measure(5000)
	t.Logf("input=unrelated_objects:50 actual_output=%+v basis=local closure visits 3 objects and 2 edges", small)
	t.Logf("input=unrelated_objects:5000 actual_output=%+v basis=5000 unrelated objects are neither visited nor counted", large)
	if small.ObjectsVisited != large.ObjectsVisited || small.ReferencesUsed != large.ReferencesUsed {
		t.Fatalf("cost grew with unrelated objects: small=%+v large=%+v", small, large)
	}
	if large.ObjectsVisited != 3 || large.ReferencesUsed > 4 {
		t.Fatalf("large=%+v want only removed three-object closure", large)
	}
}

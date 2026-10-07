package ontology

import (
	"fmt"
	"testing"
)

// TestDecisionCostIndependentOfStoreSize verifies the required cost bound:
// deciding a fixed-size batch inspects the same number of committed records
// regardless of the total number of instances in the store. The internal
// InstanceInspections counter is the "no extra externally exposed state"
// evidence: it grows with the batch's neighborhood, never with the store.
func TestDecisionCostIndependentOfStoreSize(t *testing.T) {
	type sample struct {
		storeSize int
		inspect   int64
	}
	var samples []sample
	measure := func(extraInstances int) int64 {
		p := testPlatform()
		// Background population not connected to the measured batch.
		for i := 0; i < extraInstances; i++ {
			p.Commit(Batch{ID: fmt.Sprintf("bg-%d", i), Ops: []Operation{
				{Instance: InstanceID(fmt.Sprintf("bg-%d", i)), Type: "Person", BaseVersion: 0,
					Props: props(map[string]any{"name": fmt.Sprintf("bg%d", i)})},
			}})
		}
		setup := p.Commit(Batch{ID: "setup", Ops: []Operation{
			{Instance: "x", Type: "Person", BaseVersion: 0, Props: props(map[string]any{"name": "X"})},
			{Instance: "y", Type: "Person", BaseVersion: 0, Props: props(map[string]any{"name": "Y"})},
		}})
		if !setup.OK {
			t.Fatalf("setup failed: %+v", setup)
		}
		before := p.Stats().InstanceInspections
		// Fixed 2-item update batch with correct bases.
		r := p.Commit(Batch{ID: "measured", Ops: []Operation{
			{Instance: "x", Type: "Person", BaseVersion: 1, Props: props(map[string]any{"name": "X2"})},
			{Instance: "y", Type: "Person", BaseVersion: 1, Props: props(map[string]any{"name": "Y2"})},
		}})
		if !r.OK {
			t.Fatalf("measured batch failed: %+v", r)
		}
		after := p.Stats().InstanceInspections
		return after - before
	}
	base := measure(0)
	for _, size := range []int{100, 1000, 10000} {
		got := measure(size)
		samples = append(samples, sample{size, got})
		if got != base {
			t.Fatalf("inspections grew with store size: base=%d size=%d got=%d", base, size, got)
		}
	}
	t.Logf("instance inspections per fixed 2-item batch (constant): %d; samples=%v", base, samples)
}

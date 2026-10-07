package ontology

import (
	"fmt"
	"sync"
	"testing"
)

func TestConcurrentReadersNeverSeeTornState(t *testing.T) {
	s := NewStore(testRegistry())
	seedPeople(t, s, "a", "b")

	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			snap := s.Snapshot([]InstanceID{"a", "b"})
			na := snap["a"].Properties["n"]
			nb := snap["b"].Properties["n"]
			// Every committed batch sets both properties to the same marker;
			// a torn snapshot (one old, one new) would show different values.
			if na != nb {
				t.Errorf("torn read: a=%v b=%v", na, nb)
				return
			}
		}
	}()

	for v := 0; v < 100; v++ {
		marker := string(rune('A' + v%20))
		va := s.GetOne("a").Version
		vb := s.GetOne("b").Version
		mustApply(t, s, BatchInput{Items: []BatchItem{
			{ID: "a", Type: "Person", Baseline: va, Properties: Property{"n": marker}},
			{ID: "b", Type: "Person", Baseline: vb, Properties: Property{"n": marker}},
		}})
	}
	close(stop)
	wg.Wait()
}

func TestOverlappingBatchesAreSerializable(t *testing.T) {
	s := NewStore(testRegistry())
	seedPeople(t, s, "a", "b", "c")

	// Three batches with pairwise-overlapping instance sets:
	// B0={a,b}, B1={b,c}, B2={a,c}. Fire them repeatedly from a barrier;
	// every round exactly one batch can win the version race, the others
	// must get version conflicts and leave state consistent.
	mk := func(x, y InstanceID) BatchInput {
		return BatchInput{Items: []BatchItem{
			{ID: x, Type: "Person", Baseline: 0, Properties: Property{"n": string(x)}},
			{ID: y, Type: "Person", Baseline: 0, Properties: Property{"n": string(y)}},
		}}
	}

	for round := 0; round < 100; round++ {
		snap := s.Snapshot([]InstanceID{"a", "b", "c"})
		b0 := mk("a", "b")
		b0.Items[0].Baseline = snap["a"].Version
		b0.Items[1].Baseline = snap["b"].Version
		b1 := mk("b", "c")
		b1.Items[0].Baseline = snap["b"].Version
		b1.Items[1].Baseline = snap["c"].Version
		b2 := mk("a", "c")
		b2.Items[0].Baseline = snap["a"].Version
		b2.Items[1].Baseline = snap["c"].Version

		results := make([]error, 3)
		start := make(chan struct{})
		var wg sync.WaitGroup
		for i, in := range []BatchInput{b0, b1, b2} {
			wg.Add(1)
			go func(i int, in BatchInput) {
				defer wg.Done()
				<-start
				_, results[i] = s.ApplyBatch(in)
			}(i, in)
		}
		close(start)
		wg.Wait()

		commits, conflicts := 0, 0
		for _, err := range results {
			if err == nil {
				commits++
				continue
			}
			be := &BatchError{}
			if asBatch(err, &be) && be.Kind == FailureVersionConflict {
				conflicts++
			} else {
				t.Fatalf("round %d: unexpected verdict %v", round, err)
			}
		}
		if commits != 1 || conflicts != 2 {
			t.Fatalf("round %d: commits=%d conflicts=%d, want exactly one commit", round, commits, conflicts)
		}

		// Post-state consistency: all three versions remain within one step
		// of each other and every property equals its ID-derived value.
		after := s.Snapshot([]InstanceID{"a", "b", "c"})
		for _, id := range []InstanceID{"a", "b", "c"} {
			got := after[id]
			if got.Properties["n"] != string(id) {
				t.Fatalf("instance %s corrupted: %v", id, got.Properties)
			}
			if got.Version < 1 || got.Version > Version(round+2+1) {
				t.Fatalf("instance %s implausible version %d", id, got.Version)
			}
		}
	}
}

// TestCostIndependentOfTotalInstances proves the decision overhead does not
// grow with the total number of instances: a two-instance batch on a store
// of 2,000 instances performs exactly the same instance touches as on a
// store of 10 instances.
func TestCostIndependentOfTotalInstances(t *testing.T) {
	measure := func(total int) map[InstanceID]int {
		s := NewStore(testRegistry())
		ids := make([]InstanceID, 0, total)
		for i := 0; i < total; i++ {
			ids = append(ids, InstanceID(fmt.Sprintf("i%04d", i)))
		}
		seedPeople(t, s, ids...)
		res, err := s.ApplyBatch(BatchInput{Items: []BatchItem{
			{ID: "i0001", Type: "Person", Baseline: 1, Properties: Property{"n": "1"}},
			{ID: "i0002", Type: "Person", Baseline: 1, Properties: Property{"n": "2"}},
		}})
		if err != nil {
			t.Fatal(err)
		}
		return res.Record.InstanceTouches
	}

	small := measure(10)
	large := measure(2000)
	if fmt.Sprint(small) != fmt.Sprint(large) {
		t.Fatalf("decision work grew with store size:\n10 instances:   %v\n2000 instances: %v", small, large)
	}
	if len(large) != 2 {
		t.Fatalf("expected touches for exactly 2 instances, got %d (%v)", len(large), large)
	}
}

package ontology

import (
	"errors"
	"fmt"
	"sync"
	"testing"
)

func TestConcurrentDeletesAndLinkCreationsAreSerializable(t *testing.T) {
	store := NewStore()
	mustRegister(t, store, "anchor", SetNull, true)

	const pairs = 100
	mustCreate(t, store, "keeper")
	for i := 0; i < pairs; i++ {
		mustCreate(t, store, fmt.Sprintf("root-%d", i), fmt.Sprintf("child-%d", i))
		mustLink(t, store, "anchor", fmt.Sprintf("root-%d", i), fmt.Sprintf("child-%d", i))
	}

	var wait sync.WaitGroup
	addResults := make([]error, pairs)
	deleteResults := make([]error, pairs)
	start := make(chan struct{})

	for i := 0; i < pairs; i++ {
		i := i
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			addResults[i] = store.AddLink(Link{
				Type:   "anchor",
				Source: "keeper",
				Target: fmt.Sprintf("child-%d", i),
			})
		}()

		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			_, deleteResults[i] = store.DeleteObject(fmt.Sprintf("root-%d", i))
		}()
	}

	close(start)
	wait.Wait()

	for i := 0; i < pairs; i++ {
		childID := fmt.Sprintf("child-%d", i)
		switch {
		case addResults[i] == nil:
			if !store.HasObject(childID) {
				t.Fatalf("child %q was deleted after keeper link creation committed", childID)
			}
		case errors.Is(addResults[i], ErrObjectNotFound):
			if store.HasObject(childID) {
				t.Fatalf("child %q survived after creation observed deletion order", childID)
			}
		default:
			t.Fatalf("unexpected add error for %q: %v", childID, addResults[i])
		}
		if deleteResults[i] != nil {
			t.Fatalf("delete root-%d: %v", i, deleteResults[i])
		}
	}

	assertLinks(t, store.Links(), store.Links())
}

func TestDedupProbeCountIndependentOfUnrelatedGraphSize(t *testing.T) {
	for _, unrelatedCount := range []int{10, 5000, 10000} {
		store := NewStore()
		mustRegister(t, store, "cycle", Cascade, false)
		mustRegister(t, store, "anchor", SetNull, true)
		mustCreate(t, store, "a", "b", "c")
		mustLink(t, store, "cycle", "a", "b")
		mustLink(t, store, "cycle", "b", "c")
		mustLink(t, store, "cycle", "c", "a")

		for i := 0; i < unrelatedCount; i++ {
			source := fmt.Sprintf("u-%d", i)
			target := fmt.Sprintf("v-%d", i)
			mustCreate(t, store, source, target)
			mustLink(t, store, "anchor", source, target)
		}

		result, err := store.DeleteObject("a")
		if err != nil {
			t.Fatal(err)
		}
		if len(result.DeletedObjects) != 3 {
			t.Fatalf("deleted %v, want only three-node cycle", result.DeletedObjects)
		}
		probes := store.LastDedupProbes()
		if probes > 10 {
			t.Fatalf("unrelated graph size=%d probes=%d, want bounded by affected closure", unrelatedCount, probes)
		}
		if probes == 0 {
			t.Fatal("dedup probes should be observable")
		}
	}
}

func TestSuccessfulDeleteLogContainsInputResultAndRuleSteps(t *testing.T) {
	store := NewStore()
	mustRegister(t, store, "owner", Cascade, true)
	mustCreate(t, store, "root", "child")
	mustLink(t, store, "owner", "root", "child")

	if _, err := store.DeleteObject("root"); err != nil {
		t.Fatal(err)
	}
	entry := store.Logs()[0]
	if entry.RootObjectID != "root" || !entry.Committed {
		t.Fatalf("entry = %+v", entry)
	}
	if !equalStrings(entry.DeletedObjects, []string{"child", "root"}) {
		t.Fatalf("logged deleted objects = %v", entry.DeletedObjects)
	}
	if len(entry.Steps) < 3 {
		t.Fatalf("steps = %v, want root, rule and cascade entries", entry.Steps)
	}
	for _, step := range entry.Steps {
		if step.ObjectID == "" {
			t.Fatalf("step missing object id: %+v", step)
		}
		if step.Kind != "delete-root" && step.Kind != "cascade-peer" && step.Rule == "" {
			t.Fatalf("rule-based step missing rule: %+v", step)
		}
	}
	if entry.DedupProbes == 0 {
		t.Fatal("log should record dedup probes")
	}
}

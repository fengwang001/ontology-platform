package ontology

import (
	"reflect"
	"strconv"
	"sync"
	"testing"
)

func TestConcurrentPushNoLossOrDuplicates(t *testing.T) {
	const workers = 16
	const perWorker = 200
	const k = 31

	selector, _ := NewSelector(k, Desc)
	var group sync.WaitGroup
	for worker := 0; worker < workers; worker++ {
		group.Add(1)
		go func(worker int) {
			defer group.Done()
			for item := 0; item < perWorker; item++ {
				score := float64(worker*perWorker + item)
				selector.Push("id-"+strconv.Itoa(worker)+"-"+strconv.Itoa(item), score)
			}
		}(worker)
	}
	group.Wait()

	got := selector.Snapshot()
	if len(got) != k {
		t.Fatalf("snapshot len = %d, want %d", len(got), k)
	}
	seen := map[string]bool{}
	var previous Element
	for index, element := range got {
		if seen[element.ID] {
			t.Fatalf("duplicate ID retained: %q", element.ID)
		}
		seen[element.ID] = true
		if index > 0 && betterInRank(element, previous, Desc) {
			t.Fatalf("snapshot is not rank-ordered at %d: %v before %v", index, previous, element)
		}
		previous = element
	}

	wantFirst := float64(workers*perWorker - 1)
	if got[0].Score != wantFirst {
		t.Fatalf("top score = %v, want %v", got[0].Score, wantFirst)
	}
}

func TestConcurrentSnapshotDoesNotObservePartialUpdate(t *testing.T) {
	const k = 8
	selector, _ := NewSelector(k, Desc)
	for i := 0; i < k; i++ {
		selector.Push("seed-"+strconv.Itoa(i), float64(i))
	}

	stop := make(chan struct{})
	var group sync.WaitGroup
	group.Add(1)
	go func() {
		defer group.Done()
		for {
			select {
			case <-stop:
				return
			default:
				snapshot := selector.Snapshot()
				if len(snapshot) > k {
					t.Errorf("snapshot len = %d, exceeds %d", len(snapshot), k)
					return
				}
				if !orderedAndUnique(snapshot, Desc) {
					t.Errorf("invalid snapshot: %v", snapshot)
					return
				}
			}
		}
	}()

	for i := 0; i < 1000; i++ {
		selector.Push("new-"+strconv.Itoa(i), float64(i+k))
	}
	close(stop)
	group.Wait()
}

func TestConcurrentUpdatesToSameIDRemainUnique(t *testing.T) {
	const workers = 32
	selector, _ := NewSelector(10, Desc)
	var group sync.WaitGroup
	for worker := 0; worker < workers; worker++ {
		group.Add(1)
		go func(score float64) {
			defer group.Done()
			selector.Push("same-id", score)
		}(float64(worker))
	}
	group.Wait()

	count := 0
	for _, element := range selector.Snapshot() {
		if element.ID == "same-id" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("same ID appears %d times, want 1", count)
	}
}

func TestSnapshotIsIndependentOfInternalState(t *testing.T) {
	selector, _ := NewSelector(2, Desc)
	selector.Push("a", 1)
	selector.Push("b", 2)
	first := selector.Snapshot()
	first[0] = Element{"mutated", 999}
	second := selector.Snapshot()

	want := []Element{{"b", 2}, {"a", 1}}
	if !reflect.DeepEqual(second, want) {
		t.Fatalf("internal state changed through snapshot: %v", second)
	}
}

func orderedAndUnique(elements []Element, direction Direction) bool {
	seen := map[string]bool{}
	for index, element := range elements {
		if seen[element.ID] {
			return false
		}
		seen[element.ID] = true
		if index > 0 && betterInRank(element, elements[index-1], direction) {
			return false
		}
	}
	return true
}

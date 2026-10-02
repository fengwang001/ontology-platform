package ontology

import (
	"sync"
	"testing"
)

func TestConcurrentRequestsFailuresAndQueries(t *testing.T) {
	const readers = 8
	a := newTestAllocator(t, readers, 4)
	splits := make([]int64, 0, 800)
	for split := int64(0); split < 800; split++ {
		splits = append(splits, split)
	}
	if err := a.AddSplits(splits); err != nil {
		t.Fatal(err)
	}

	var workers sync.WaitGroup
	for reader := 0; reader < readers; reader++ {
		workers.Add(1)
		go func(reader int) {
			defer workers.Done()
			lastAssigned := int64(-1)
			for iteration := 0; iteration < 200; iteration++ {
				if result, err := a.RequestSplit(reader); err == nil && result.Kind == RequestAssigned {
					lastAssigned = result.Split
					if iteration%3 == 0 {
						_ = a.Finished(reader, lastAssigned)
					}
					continue
				}

				if _, err := a.ReaderFailed(reader); err == nil {
					_ = a.ReaderRestarted(reader)
					lastAssigned = -1
					continue
				}
				_ = a.ReaderRestarted(reader)
				_ = a.Quarantined()
				_ = a.Counts()
			}
		}(reader)
	}
	workers.Wait()

	counts := a.Counts()
	total := 0
	for _, count := range counts {
		total += count
	}
	if total != len(splits) {
		t.Fatalf("counts total = %d, want %d (%v)", total, len(splits), counts)
	}
	for split := int64(0); split < 800; split++ {
		info, err := a.State(split)
		if err != nil {
			t.Fatalf("state(%d): %v", split, err)
		}
		switch info.State {
		case Unassigned, Quarantined:
			if info.Owner != -1 {
				t.Fatalf("split %d owner = %d, want none", split, info.Owner)
			}
		case Assigned, Finished:
			if info.Owner < 0 || info.Owner >= readers {
				t.Fatalf("split %d owner = %d, want valid reader", split, info.Owner)
			}
		default:
			t.Fatalf("split %d has invalid state %v", split, info.State)
		}
	}
}

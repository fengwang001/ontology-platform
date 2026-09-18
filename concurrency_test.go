package instance

import (
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
)

// TestConcurrentUpdateSuccessCountEqualsVersionIncrement is the core OCC
// invariant: with N goroutines repeatedly doing Get-then-Update on one key,
// the number of successful updates must equal finalVersion-1 exactly, every
// failure must be a version conflict, and observed versions must have no gaps
// or duplicates. Run under -race.
func TestConcurrentUpdateSuccessCountEqualsVersionIncrement(t *testing.T) {
	const goroutines = 16
	const attemptsPerGoroutine = 200

	store, _ := testStore()
	if _, err := store.Create("Counter", "c", map[string]any{"n": 0}); err != nil {
		t.Fatal(err)
	}

	var successCount, conflictCount, otherFailures int64
	var mu sync.Mutex
	seenVersions := map[int64]int{}

	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for attempt := 0; attempt < attemptsPerGoroutine; attempt++ {
				snap, err := store.Get("Counter", "c")
				if err != nil {
					atomic.AddInt64(&otherFailures, 1)
					return
				}
				updated, err := store.Update("Counter", "c", snap.Version,
					map[string]any{"n": attempt, "by": id})
				if err == nil {
					atomic.AddInt64(&successCount, 1)
					mu.Lock()
					seenVersions[updated.Version]++
					mu.Unlock()
					continue
				}
				we, ok := AsWriteError(err)
				if !ok || we.Reason != ReasonVersionConflict {
					atomic.AddInt64(&otherFailures, 1)
					continue
				}
				atomic.AddInt64(&conflictCount, 1)
			}
		}(g)
	}
	wg.Wait()

	if otherFailures != 0 {
		t.Fatalf("got %d failures that were not version conflicts", otherFailures)
	}

	final, err := store.Get("Counter", "c")
	if err != nil {
		t.Fatal(err)
	}
	if got := atomic.LoadInt64(&successCount); got != final.Version-1 {
		t.Fatalf("successes = %d, final version - 1 = %d", got, final.Version-1)
	}
	if atomic.LoadInt64(&conflictCount) == 0 {
		t.Fatal("expected some contention; test setup produced no conflicts")
	}

	// Every version 2..final must appear exactly once: no skips, no dupes.
	for v := int64(2); v <= final.Version; v++ {
		switch seenVersions[v] {
		case 0:
			t.Fatalf("version %d missing: version numbers skipped", v)
		case 1:
		default:
			t.Fatalf("version %d observed %d times: duplicated", v, seenVersions[v])
		}
	}
}

// TestFailingBatchInvisibleToConcurrentReaders drives batches that always
// fail validation while readers observe a stable key: no reader may ever see a
// partial application, and the version must remain pinned at 1.
func TestFailingBatchInvisibleToConcurrentReaders(t *testing.T) {
	store, _ := testStore()
	_, _ = store.Create("Person", "stable", map[string]any{"v": "zero"})

	var sawIntermediate int64
	var wg sync.WaitGroup

	for r := 0; r < 8; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 500; i++ {
				got, err := store.Get("Person", "stable")
				if err != nil {
					atomic.AddInt64(&sawIntermediate, 1)
					return
				}
				if got.Version != 1 || got.Attributes["v"] != "zero" {
					atomic.AddInt64(&sawIntermediate, 1)
					return
				}
				if _, err := store.Get("Person", "ghostNew"); err == nil {
					atomic.AddInt64(&sawIntermediate, 1)
					return
				}
			}
		}()
	}

	for i := 0; i < 500; i++ {
		_, _ = store.BatchWrite([]Op{
			{Kind: OpUpdate, ObjectType: "Person", Key: "stable", ExpectedVersion: 1,
				Attributes: map[string]any{"v": "partial"}},
			{Kind: OpCreate, ObjectType: "Person", Key: "ghostNew"},
			{Kind: OpDelete, ObjectType: "Person", Key: "stable", ExpectedVersion: 999},
		})
	}
	wg.Wait()

	if atomic.LoadInt64(&sawIntermediate) != 0 {
		t.Fatal("a reader observed an intermediate state of a failing batch")
	}
}

func TestConcurrentSingleWritesAndBatchesStayConsistent(t *testing.T) {
	store, _ := testStore()
	_, _ = store.Create("Mix", "k", map[string]any{"tag": "init"})

	var wg sync.WaitGroup
	var badReads int64

	// Readers: every snapshot must be internally coherent.
	for r := 0; r < 4; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 300; i++ {
				got, err := store.Get("Mix", "k")
				if err != nil {
					continue // deleted-then-recreated windows surface as Deleted
				}
				if got.Version < 1 || got.Attributes["tag"] == nil {
					atomic.AddInt64(&badReads, 1)
				}
			}
		}()
	}

	// Writers: single Update with Get-retry, and resurrecting batches.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 200; i++ {
			for {
				snap, err := store.Get("Mix", "k")
				if err != nil {
					return
				}
				if _, err := store.Update("Mix", "k", snap.Version,
					map[string]any{"tag": fmt.Sprintf("u%d", i)}); err == nil {
					break
				}
			}
		}
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 50; i++ {
			snap, err := store.Get("Mix", "k")
			if err != nil {
				continue
			}
			if _, err := store.BatchWrite([]Op{
				{Kind: OpDelete, ObjectType: "Mix", Key: "k", ExpectedVersion: snap.Version},
			}); err != nil {
				continue
			}
			if _, err := store.Create("Mix", "k", map[string]any{"tag": "revived"}); err != nil {
				t.Errorf("resurrect: %v", err)
				return
			}
		}
	}()

	wg.Wait()
	if atomic.LoadInt64(&badReads) != 0 {
		t.Fatal("readers observed incoherent snapshots under concurrent writes")
	}
}

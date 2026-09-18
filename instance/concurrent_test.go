package instance

import (
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
)

// TestConcurrentUpdateOCC runs N goroutines, each repeatedly doing
// Get -> Update with the version it read. The number of successful
// updates must equal the final version minus the initial version,
// every failure must be a version conflict, and versions advance one
// at a time with no gaps or duplicates. Run under -race.
func TestConcurrentUpdateOCC(t *testing.T) {
	s := NewStore(personSpec())

	const writers = 64
	if _, err := s.Create("person", "hot", map[string]any{"name": "v0"}); err != nil {
		t.Fatal(err)
	}

	var successes atomic.Int64
	var conflicts atomic.Int64
	var otherErrs atomic.Int64

	var wg sync.WaitGroup
	for g := 0; g < writers; g++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for {
				got, err := s.Get("person", "hot")
				if err != nil {
					otherErrs.Add(1)
					return
				}
				if got.Version > int64(writers) {
					return
				}
				_, err = s.Update("person", "hot", got.Version,
					map[string]any{"name": fmt.Sprintf("g%d-v%d", id, got.Version)})
				if err == nil {
					successes.Add(1)
					continue
				}
				var ce *Error
				if errors.As(err, &ce) && ce.Kind == KindConflict {
					conflicts.Add(1)
					continue
				}
				otherErrs.Add(1)
				return
			}
		}(g)
	}
	wg.Wait()

	if otherErrs.Load() != 0 {
		t.Fatalf("saw %d non-conflict errors", otherErrs.Load())
	}
	final, err := s.Get("person", "hot")
	if err != nil {
		t.Fatal(err)
	}
	// Started at v1; exactly one success per version step, so successes
	// must equal the total version increment. Since each Update adds
	// exactly one, this also proves no gaps and no duplicates.
	if successes.Load() != final.Version-1 {
		t.Fatalf("successes = %d, version increment = %d", successes.Load(), final.Version-1)
	}
	if conflicts.Load() == 0 {
		t.Log("warning: no conflicts observed (harmless)")
	}
}

// TestConcurrentBatchAtomicity interleaves batches that write a shared
// round label to three keys with singleton writes to a fourth key.
// Observers must never see a partial batch: the three labels read at
// any instant must all be equal.
func TestConcurrentBatchAtomicity(t *testing.T) {
	s := NewStore(personSpec())

	const rounds = 100

	for i := 0; i < 3; i++ {
		if _, err := s.Create("person", fmt.Sprintf("k%d", i), map[string]any{"name": "round-0"}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.Create("person", "single", map[string]any{"name": "s0"}); err != nil {
		t.Fatal(err)
	}

	stop := make(chan struct{})
	var wg sync.WaitGroup
	var violations atomic.Int64

	// Observers: a partial batch would surface as differing labels.
	for o := 0; o < 4; o++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				labels := make([]any, 3)
				for i := range labels {
					inst, err := s.Get("person", fmt.Sprintf("k%d", i))
					if err != nil {
						violations.Add(1)
						return
					}
					labels[i] = inst.Properties["name"]
				}
				if labels[0] != labels[1] || labels[1] != labels[2] {
					violations.Add(1)
					return
				}
			}
		}()
	}

	// Batch writers retry on conflicts until all rounds are applied.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for r := 1; r <= rounds; r++ {
			for {
				snaps := make([]*Instance, 3)
				for i := range snaps {
					inst, err := s.Get("person", fmt.Sprintf("k%d", i))
					if err != nil {
						violations.Add(1)
						return
					}
					snaps[i] = inst
				}
				ops := make([]Op, 3)
				label := fmt.Sprintf("round-%d", r)
				for i := range ops {
					ops[i] = Op{
						Kind:            OpUpdate,
						ObjectType:      "person",
						Key:             fmt.Sprintf("k%d", i),
						ExpectedVersion: snaps[i].Version,
						Properties:      map[string]any{"name": label},
					}
				}
				if err := s.BatchWrite(ops); err == nil {
					break
				}
			}
		}
	}()

	// Concurrent singleton writer to an unrelated key.
	wg.Add(1)
	go func() {
		defer wg.Done()
		var v int64
		for {
			select {
			case <-stop:
				return
			default:
			}
			inst, err := s.Get("person", "single")
			if err != nil {
				violations.Add(1)
				return
			}
			if _, err := s.Update("person", "single", inst.Version,
				map[string]any{"name": fmt.Sprintf("s%d", v)}); err == nil {
				v++
			}
		}
	}()

	// The batch goroutine is the one that ends naturally; poll the
	// final round label, then stop the observers and singleton writer.
	for {
		inst, err := s.Get("person", "k0")
		if err == nil && inst.Properties["name"] == fmt.Sprintf("round-%d", rounds) {
			break
		}
	}
	close(stop)
	wg.Wait()

	if violations.Load() != 0 {
		t.Fatalf("observed %d batch atomicity violations", violations.Load())
	}

	// All three keys must end at the same version (batches are the only
	// writer touching them).
	var finalVersion int64
	for i := 0; i < 3; i++ {
		inst, err := s.Get("person", fmt.Sprintf("k%d", i))
		if err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			finalVersion = inst.Version
		} else if inst.Version != finalVersion {
			t.Fatalf("batch keys diverged: k0=%d k%d=%d", finalVersion, i, inst.Version)
		}
		if inst.Properties["name"] != fmt.Sprintf("round-%d", rounds) {
			t.Fatalf("final label mismatch: %v", inst.Properties["name"])
		}
	}
}

package ontology

import (
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
)

// Concurrent writes to the same entity must all eventually succeed (with
// caller-side retry on regression) and leave a strictly increasing,
// duplicate-free transaction time series.
func TestConcurrentSameEntityStrictlyIncreasing(t *testing.T) {
	s := NewStore()
	const writers = 32
	var seq atomic.Int64
	var wg sync.WaitGroup
	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				tx := ts(1000 + seq.Add(1))
				err := s.Write("e", "p", "v", ts(0), ts(100), tx)
				if errors.Is(err, ErrTxRegression) {
					continue
				}
				if err != nil {
					t.Errorf("unexpected write error: %v", err)
				}
				return
			}
		}()
	}
	wg.Wait()

	corr, err := s.Corrections("e", "p", ts(50))
	if err != nil {
		t.Fatalf("corrections: %v", err)
	}
	if len(corr) != writers {
		t.Fatalf("want %d corrections, got %d", writers, len(corr))
	}
	for i := 1; i < len(corr); i++ {
		if !corr[i].TxFrom.After(corr[i-1].TxFrom) {
			t.Fatalf("tx series not strictly increasing at %d: %v then %v",
				i, corr[i-1].TxFrom, corr[i].TxFrom)
		}
	}
}

// While writers overwrite a fact, concurrent readers must never observe a
// half-applied carve (old fact closed but successor not yet visible), and
// historical reads must stay repeatable.
func TestConcurrentReadersSeeConsistentState(t *testing.T) {
	s := NewStore()
	mustWrite(t, s, "e", "p", "v0", ts(0), ts(1000), ts(1))

	var seq atomic.Int64
	seq.Store(1)
	stop := make(chan struct{})
	var writers, readers sync.WaitGroup

	for w := 0; w < 4; w++ {
		writers.Add(1)
		go func() {
			defer writers.Done()
			for i := 0; i < 200; i++ {
				tx := ts(1000 + seq.Add(1))
				err := s.Write("e", "p", "vx", ts(0), ts(1000), tx)
				if err != nil && !errors.Is(err, ErrTxRegression) {
					t.Errorf("write: %v", err)
					return
				}
				if errors.Is(err, ErrTxRegression) {
					i--
				}
			}
		}()
	}

	for r := 0; r < 4; r++ {
		readers.Add(1)
		go func() {
			defer readers.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				// Far-future txAt always sees exactly one current fact.
				if _, err := s.AsOf("e", "p", ts(500), ts(1<<40)); err != nil {
					t.Errorf("read observed half-applied state: %v", err)
					return
				}
				// The original fact stays readable at its tx time forever.
				f, err := s.AsOf("e", "p", ts(500), ts(1))
				if err != nil || f.Value != "v0" {
					t.Errorf("repeatable read broken: %v %v", f, err)
					return
				}
			}
		}()
	}

	writers.Wait()
	close(stop)
	readers.Wait()
}

// Writes to different entities proceed independently: identical tx values
// per entity are all accepted, and nothing races.
func TestConcurrentDifferentEntities(t *testing.T) {
	s := NewStore()
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			ent := fmt.Sprintf("entity-%d", i)
			for j := 0; j < 50; j++ {
				if err := s.Write(ent, "p", j, ts(0), ts(10), ts(int64(1+j))); err != nil {
					t.Errorf("%s write %d: %v", ent, j, err)
				}
			}
		}(i)
	}
	wg.Wait()

	for i := 0; i < 16; i++ {
		corr, err := s.Corrections(fmt.Sprintf("entity-%d", i), "p", ts(5))
		if err != nil || len(corr) != 50 {
			t.Errorf("entity-%d: want 50 corrections, got %d (%v)", i, len(corr), err)
		}
	}
}

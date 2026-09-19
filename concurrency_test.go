package ontology

import (
	"errors"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
)

func TestConcurrentWritesDifferentEntities(t *testing.T) {
	s := NewStore()
	var wg sync.WaitGroup
	// Identical transaction times across different entities must all land;
	// their locks are independent.
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			if err := s.Put("entity-"+strconv.Itoa(n), "a", "v", ts(0), ts(10), ts(1)); err != nil {
				t.Errorf("entity %d: %v", n, err)
			}
		}(i)
	}
	wg.Wait()
}

func TestConcurrentWritesSameEntityStrictlyAdvancing(t *testing.T) {
	s := NewStore()
	if err := s.Put("e", "a", "v0", ts(0), ts(100), ts(0+1)); err != nil {
		t.Fatal(err)
	}

	var counter int64 = 1
	var wg sync.WaitGroup

	// Readers must never observe a half-done clipping: once a fact exists,
	// an AsOf well after all transaction times must always resolve a value.
	stop := make(chan struct{})
	var readWG sync.WaitGroup
	for r := 0; r < 8; r++ {
		readWG.Add(1)
		go func() {
			defer readWG.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				got := s.AsOf("e", "a", ts(50), ts(1_000_000))
				if got.Status != StatusFound || got.Value == "" {
					t.Errorf("half-done state observed: %+v", got)
					return
				}
			}
		}()
	}

	for w := 0; w < 16; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for k := 0; k < 50; k++ {
				for {
					n := atomic.AddInt64(&counter, 1)
					err := s.Put("e", "a", "v"+strconv.FormatInt(n, 10), ts(0), ts(100), ts(n))
					if err == nil {
						break
					}
					if !errors.Is(err, ErrTxNotAdvancing) {
						t.Errorf("put at %d: %v", n, err)
						return
					}
				}
			}
		}()
	}
	wg.Wait()
	close(stop)
	readWG.Wait()

	tr := s.Trajectory("e", "a", ts(50))
	if len(tr) != 801 {
		t.Fatalf("trajectory len = %d, want 801 versions", len(tr))
	}
	for i := 1; i < len(tr); i++ {
		if !tr[i].Tx.From.After(tr[i-1].Tx.From) {
			t.Fatalf("tx times not strictly increasing at %d: %v %v", i, tr[i-1].Tx.From, tr[i].Tx.From)
		}
	}

	// A late tx time sees exactly the final corrected value.
	final := "v" + strconv.FormatInt(tr[len(tr)-1].Tx.From.Unix(), 10)
	if got := s.AsOf("e", "a", ts(50), ts(1_000_000)); got.Value != final {
		t.Fatalf("final value = %q, want %q", got.Value, final)
	}
}

package pipeline

import (
	"sync"
	"sync/atomic"
	"testing"

	"ontology/sink"
)

// Concurrent writers, advancers, checkpointing and readers: the invariant
// Accepted == Confirmed + Pending must hold at every observation.
func TestConcurrentIdentity(t *testing.T) {
	p := mustNew(t, testCfg(), &sink.ShortWriter{Max: 5})
	var bad atomic.Bool
	var wg sync.WaitGroup
	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 100; i++ {
				if _, err := p.Write([]byte("12345678")); err != nil {
					bad.Store(true)
				}
			}
		}()
	}
	for w := 0; w < 2; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 500; i++ {
				_, _ = p.Advance()
				st := p.Stats()
				if st.Accepted != st.Confirmed+st.Pending {
					bad.Store(true)
				}
			}
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 50; i++ {
			cp := p.Checkpoint()
			if cp.Accepted != cp.Confirmed+cp.Pending() {
				bad.Store(true)
			}
		}
	}()
	wg.Wait()
	if err := p.Close(); err != nil {
		t.Fatal(err)
	}
	drain(t, p)
	if bad.Load() {
		t.Fatal("identity violated under concurrency")
	}
	st := p.Stats()
	if st.Accepted != st.Confirmed+st.Pending || st.Pending != 0 {
		t.Fatalf("final identity broken: %+v", st)
	}
}

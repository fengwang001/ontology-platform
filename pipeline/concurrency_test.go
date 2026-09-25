package pipeline

import (
	"sync"
	"sync/atomic"
	"testing"

	"ontology/sink"
)

// Concurrent writers, pumpers and readers: must stay race-clean, deadlock-free
// and keep accepted = confirmed + buffered at every observed instant.
func TestConcurrentInvariant(t *testing.T) {
	cfg := testCfg()
	cfg.MaxBuffer = 1 << 20
	s := &sink.Scripted{Quota: 1 << 20, MaxAccept: 7, CutAfter: -1, FailAt: -1}
	p, _ := New(s, &testClock{}, cfg)

	const writers, perWriter = 6, 200
	var wgW, wgP, wgR sync.WaitGroup
	stop := make(chan struct{})
	var badInvariant int32

	for w := 0; w < writers; w++ {
		wgW.Add(1)
		go func() {
			defer wgW.Done()
			b := []byte("0123456789abcdef")
			for i := 0; i < perWriter; i++ {
				if _, err := p.Write(b); err != nil && err != sink.ErrBackpressure {
					panic(err)
				}
			}
		}()
	}

	for i := 0; i < 3; i++ {
		wgP.Add(1)
		go func() {
			defer wgP.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				err := p.Pump()
				if err != nil && err != sink.ErrBackpressure {
					panic(err)
				}
			}
		}()
	}

	for i := 0; i < 2; i++ {
		wgR.Add(1)
		go func() {
			defer wgR.Done()
			for j := 0; j < 20000; j++ {
				st := p.Stats()
				if st.Accepted != st.Confirmed+st.Buffered {
					atomic.StoreInt32(&badInvariant, 1)
				}
				if p.ScanBytes() < 0 {
					atomic.StoreInt32(&badInvariant, 1)
				}
			}
		}()
	}

	wgW.Wait()
	close(stop)
	wgP.Wait()
	wgR.Wait()

	if atomic.LoadInt32(&badInvariant) != 0 {
		t.Fatal("invariant violated during concurrent run")
	}
	if err := p.Close(); err != nil {
		t.Fatal(err)
	}
	st := p.Stats()
	want := int64(writers * perWriter * 16)
	if st.Accepted != want || st.Confirmed != want || st.Buffered != 0 {
		t.Fatalf("final stats = %+v, want accepted %d", st, want)
	}
}

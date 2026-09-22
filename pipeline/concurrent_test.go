package pipeline

import (
	"errors"
	"sync"
	"testing"
)

func TestConcurrentIngest(t *testing.T) {
	dir := t.TempDir()
	p, err := Open(Config{Dir: dir, MemoryByte: 4096})
	if err != nil {
		t.Fatal(err)
	}
	const writers, per = 12, 500
	var wg sync.WaitGroup
	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < per; i++ {
				if _, err := p.Ingest(keyFixed(w*per+i), []byte{byte(w)}); err != nil {
					t.Errorf("ingest: %v", err)
					return
				}
			}
		}(w)
	}
	wg.Wait()
	if err := p.Close(); err != nil {
		t.Fatal(err)
	}
	st := checkOutputStats(t, dir, writers*per)
	if st.PeakResident > st.Limit {
		t.Fatalf("peak %d > limit %d under concurrency", st.PeakResident, st.Limit)
	}
}

func TestConcurrentIngestVsClose(t *testing.T) {
	dir := t.TempDir()
	p, err := Open(Config{Dir: dir, MemoryByte: 2048})
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 2000; i++ {
				_, err := p.Ingest("k", []byte{1, 2, 3})
				if err != nil && !errors.Is(err, ErrClosed) {
					t.Errorf("unexpected ingest error: %v", err)
					return
				}
			}
		}()
	}
	// Race Close against writers; every post-close Ingest must be the
	// definitive ErrClosed, never a panic or a silent drop.
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			if _, err := p.Ingest("k", nil); errors.Is(err, ErrClosed) {
				return
			}
		}
	}()
	if err := p.Close(); err != nil {
		t.Fatal(err)
	}
	wg.Wait()
	<-done
}

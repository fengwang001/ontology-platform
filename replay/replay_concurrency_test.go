package replay

import (
	"sync"
	"testing"
)

func TestConcurrentAppendReplay(t *testing.T) {
	l, err := Open(t.TempDir(), 8, 64)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	const total = 2000
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < total; i++ {
			if _, err := l.Append([]byte{byte(i)}); err != nil {
				t.Errorf("append: %v", err)
				return
			}
		}
	}()
	stop := make(chan struct{})
	var readers sync.WaitGroup
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
				evs, _, err := l.Replay(0, ^uint64(0))
				if err != nil {
					t.Errorf("replay: %v", err)
					return
				}
				for i, e := range evs {
					if e.Seq != uint64(i) {
						t.Errorf("gap at %d: seq %d", i, e.Seq)
						return
					}
				}
			}
		}()
	}
	wg.Wait()
	close(stop)
	readers.Wait()
	evs, _, err := l.Replay(0, ^uint64(0))
	if err != nil {
		t.Fatal(err)
	}
	if len(evs) != total {
		t.Fatalf("final replay = %d events, want %d", len(evs), total)
	}
	for i, e := range evs {
		if e.Seq != uint64(i) {
			t.Fatalf("final gap at %d: seq %d", i, e.Seq)
		}
	}
}

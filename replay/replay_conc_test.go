package replay

import (
	"fmt"
	"math"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"ontology/event"
	"ontology/segment"
)

// 并发追加 + 持续回放：每次回放必须看到连续无缺口的一致前缀。
func TestConcurrentAppendReplay(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, fmt.Sprintf("%020d.seg", 0))
	w, err := segment.Create(path, 0)
	if err != nil {
		t.Fatal(err)
	}
	const total = 5000
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := uint64(0); i < total; i++ {
			if err := w.Append(event.Event{Seq: i, Payload: []byte("payload")}); err != nil {
				t.Error(err)
				return
			}
		}
	}()
	deadline := time.Now().Add(30 * time.Second)
	for {
		var seqs []uint64
		_, err := ReplayPrefix(dir, 0, math.MaxUint64, func(e event.Event) error {
			seqs = append(seqs, e.Seq)
			return nil
		})
		if err != nil {
			t.Fatalf("prefix replay: %v", err)
		}
		for i, s := range seqs {
			if s != uint64(i) {
				t.Fatalf("gap at %d: got seq %d (prefix not contiguous)", i, s)
			}
		}
		if uint64(len(seqs)) == total {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("timeout: saw %d/%d", len(seqs), total)
		}
	}
	wg.Wait()
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
}

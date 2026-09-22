package segment

import (
	"strconv"
	"sync"
	"testing"
)

func TestConcurrentAppendNoInterleave(t *testing.T) {
	dev := NewDevice()
	seg := New(dev)
	const writers = 8
	const perWriter = 50
	var wg sync.WaitGroup
	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for i := 0; i < perWriter; i++ {
				tag := "w" + strconv.Itoa(id) + "-" + strconv.Itoa(i)
				seg.Append([]byte(tag))
			}
		}(w)
	}
	wg.Wait()
	got, rep := collect(dev, dev.Len())
	if rep.Reason != StopEOF {
		t.Fatalf("reason = %v, want StopEOF (interleaved records?)", rep.Reason)
	}
	if len(got) != writers*perWriter {
		t.Fatalf("replayed %d records, want %d", len(got), writers*perWriter)
	}
	seen := make(map[string]bool)
	for _, p := range got {
		if seen[string(p)] {
			t.Fatalf("duplicate payload %q", p)
		}
		seen[string(p)] = true
	}
	if len(seen) != writers*perWriter {
		t.Fatalf("distinct payloads = %d, want %d", len(seen), writers*perWriter)
	}
}

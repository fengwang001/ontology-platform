package pagecache

import (
	"sync"
	"testing"
)

func TestNoRescans(t *testing.T) {
	c, _ := New(1000)
	mustSet(t, c, "A", 1000)
	mustMap(t, c, 0, "A", "f", Page{"X", 10})
	_ = c.Used("A")
	_ = c.Logical("A")
	_ = c.Over("A")
	_ = c.Total()
	if c.rescans != 0 {
		t.Fatalf("queries triggered %d rescans", c.rescans)
	}
}

// Concurrent callers must observe a serializable history: after the storm the
// cache still satisfies sum(used) == total and every page invariants hold.
func TestConcurrentOperations(t *testing.T) {
	c, _ := New(1_000_000)
	if err := c.SetLimit("A", 1_000_000); err != nil {
		t.Fatal(err)
	}
	if err := c.SetLimit("B", 1_000_000); err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				name := string(rune('a'+w)) + itoa(i)
				cg := "A"
				if w%2 == 0 {
					cg = "B"
				}
				pages := []Page{{ID: "p" + itoa(i%20), Size: int64(1 + i%50)}}
				_ = c.Map(int64(i), cg, name, pages)
				_, _ = c.Total(), c.Used("A")
				_ = c.Unmap(int64(i), cg, name)
			}
		}(w)
	}
	wg.Wait()

	c.mu.RLock()
	defer c.mu.RUnlock()
	var usedSum int64
	for _, cg := range c.cgroups {
		usedSum += cg.used
		for name, mp := range cg.mappings {
			if mp == nil {
				t.Fatalf("nil mapping %q", name)
			}
		}
	}
	if usedSum != c.total {
		t.Fatalf("sum(used)=%d != total=%d", usedSum, c.total)
	}
	for id, pe := range c.pages {
		if pe.holders[pe.owner] == nil {
			t.Fatalf("page %q owner %q not a holder", id, pe.owner)
		}
	}
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var buf [20]byte
	pos := len(buf)
	for i > 0 {
		pos--
		buf[pos] = byte('0' + i%10)
		i /= 10
	}
	return string(buf[pos:])
}

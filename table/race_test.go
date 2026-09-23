package table_test

import (
	"sync"
	"testing"

	"ontology/name"
	"ontology/table"
)

// TestConcurrentRefs: N goroutines run the same read-only mix; every
// reference point must observe bit-identical (declaration, depth).
func TestConcurrentRefs(t *testing.T) {
	tb := table.New(10, 10)
	_ = tb.Declare("x", name.Plain, 0)
	_ = tb.Declare("f", name.Forward, 8)
	_ = tb.Enter()
	_ = tb.Declare("y", name.Forward, 5)
	type result struct {
		decl  name.Decl
		depth int
	}
	want := make(map[string]result)
	for _, ref := range []struct {
		n   string
		pos int
	}{{"x", 3}, {"y", 1}, {"f", 2}} {
		d, depth, err := tb.Ref(ref.n, ref.pos)
		if err != nil {
			t.Fatalf("setup Ref: %v", err)
		}
		want[ref.n] = result{d, depth}
	}
	const goroutines, iters = 8, 300
	var wg sync.WaitGroup
	failures := make(chan string, goroutines*iters)
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < iters; i++ {
				for n, w := range want {
					d, depth, err := tb.Ref(n, 1)
					if err != nil || d != w.decl || depth != w.depth {
						failures <- n
					}
				}
				if tb.SelfCheck() != nil || len(tb.Captures()) != 2 {
					failures <- "selfcheck/captures"
				}
			}
		}()
	}
	wg.Wait()
	close(failures)
	for f := range failures {
		t.Fatalf("concurrent mismatch at %q", f)
	}
}

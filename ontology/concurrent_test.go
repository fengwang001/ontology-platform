package ontology

import (
	"fmt"
	"sync"
	"testing"
)

// TestConcurrentAccess hammers every method of one replica from many
// goroutines. Under -race it validates internal locking; afterwards the
// structural invariants (|D| == c - minAck, |D| <= Cap, monotonic state)
// show the aggregate result is one achievable by some serial order.
func TestConcurrentAccess(t *testing.T) {
	r, err := New("srv", []string{"p1", "p2", "p3"}, 8)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	var wg sync.WaitGroup

	// Inflating writers; buffer-full rejections are expected and leave all
	// state untouched.
	for g := 0; g < 4; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i <= 2000; i++ {
				key := fmt.Sprintf("g%d", g)
				_, _ = r.Apply(key, uint64(i+1))
			}
		}(g)
	}

	// Remote receivers; merging never creates D entries.
	for _, p := range []string{"p1", "p2", "p3"} {
		wg.Add(1)
		go func(p string) {
			defer wg.Done()
			for i := 0; i < 2000; i++ {
				_, _ = r.Receive(p, i, i+1, DeltaGroup{"remote": uint64(i + 1)})
			}
		}(p)
	}

	// Delta readers mutate every returned group to prove there is no aliasing.
	for _, p := range []string{"p1", "p2", "p3"} {
		wg.Add(1)
		go func(p string) {
			defer wg.Done()
			for i := 0; i < 2000; i++ {
				msg, derr := r.DeltaTo(p)
				if derr != nil {
					t.Errorf("DeltaTo: %v", derr)
					return
				}
				for k := range msg.Group {
					msg.Group[k] = 0
					_ = k
				}
			}
		}(p)
	}

	// Ackers drive reclamation, never acking beyond what has been seen.
	for _, p := range []string{"p1", "p2", "p3"} {
		wg.Add(1)
		go func(p string) {
			defer wg.Done()
			for i := 0; i < 2000; i++ {
				seen, _ := r.SeenOf(p)
				_ = r.Ack(p, seen)
			}
		}(p)
	}

	wg.Wait()

	minAck := r.Cursor()
	for _, p := range []string{"p1", "p2", "p3"} {
		a, _ := r.AckOf(p)
		if a < minAck {
			minAck = a
		}
	}
	judge(t, "concurrent |D| invariant", "|D| == c-minAck",
		r.BufferLen(), r.Cursor()-minAck,
		"任意并发交错后回收不变量仍成立，等价于某个串行顺序")
	if r.BufferLen() > 8 {
		t.Fatalf("buffer exceeded Cap: %d", r.BufferLen())
	}
	snap := r.Snapshot()
	v, ok := snap["remote"]
	judge(t, "concurrent remote merged", "S[remote] == 2000",
		fmt.Sprintf("%d/%v", v, ok), "2000/true",
		"三个对端交错接收后 remote 最终为最大序号 2000")
}

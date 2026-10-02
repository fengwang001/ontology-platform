package consistenthash_test

import (
	"errors"
	"sync"
	"testing"

	"ontology/consistenthash"
)

// TestValidationOrderingAndNoMutation covers rejection ordering and proves
// failed operations do not mutate nodes, points, keys, loads or seq.
func TestValidationOrderingAndNoMutation(t *testing.T) {
	if _, err := consistenthash.New(0, 1); !errors.Is(err, consistenthash.ErrInvalidConfig) {
		t.Fatalf("New(0,1)=%v", err)
	}
	if _, err := consistenthash.New(3, 4); !errors.Is(err, consistenthash.ErrInvalidConfig) {
		t.Fatalf("New(3,4)=%v", err)
	}
	if _, err := consistenthash.New(1_000_001, 1); !errors.Is(err, consistenthash.ErrInvalidConfig) {
		t.Fatalf("New(too big)=%v", err)
	}

	r, _ := consistenthash.New(2, 1)

	// Empty key is reported before no-node.
	if err := r.Put("", 0); !errors.Is(err, consistenthash.ErrInvalidKey) {
		t.Fatalf("empty key err=%v", err)
	}
	if err := r.Put("x", 0); !errors.Is(err, consistenthash.ErrNoNode) {
		t.Fatalf("no node err=%v", err)
	}

	if err := r.AddNode(0, []uint64{10}); !errors.Is(err, consistenthash.ErrInvalidNode) {
		t.Fatalf("id 0 err=%v", err)
	}
	if err := r.AddNode(1, nil); !errors.Is(err, consistenthash.ErrInvalidNode) {
		t.Fatalf("empty points err=%v", err)
	}
	if err := r.AddNode(1, []uint64{10, 10}); !errors.Is(err, consistenthash.ErrInvalidNode) {
		t.Fatalf("dup points err=%v", err)
	}
	big := make([]uint64, 65)
	for i := range big {
		big[i] = uint64(i + 1)
	}
	if err := r.AddNode(1, big); !errors.Is(err, consistenthash.ErrInvalidNode) {
		t.Fatalf("65 points err=%v", err)
	}

	mustAdd(t, r, 1, []uint64{10, 50})
	if err := r.AddNode(1, []uint64{99}); !errors.Is(err, consistenthash.ErrNodeExists) {
		t.Fatalf("duplicate node err=%v", err)
	}
	if err := r.AddNode(2, []uint64{10}); !errors.Is(err, consistenthash.ErrPointConflict) {
		t.Fatalf("point conflict err=%v", err)
	}
	mustAdd(t, r, 2, []uint64{30})

	if err := r.Put("dup", 0); err != nil {
		t.Fatal(err)
	}
	seq1, _ := r.Seq("dup")
	if err := r.Put("dup", 9); !errors.Is(err, consistenthash.ErrKeyExists) {
		t.Fatalf("dup key err=%v", err)
	}
	if err := r.Put("", 0); !errors.Is(err, consistenthash.ErrInvalidKey) {
		t.Fatalf("empty key2 err=%v", err)
	}
	if err := r.Put("next", 0); err != nil {
		t.Fatal(err)
	}
	if seq, _ := r.Seq("next"); seq != seq1+1 {
		t.Fatalf("failed Put consumed a seq: next.seq=%d want %d", seq, seq1+1)
	}

	if err := r.RemoveNode(99); !errors.Is(err, consistenthash.ErrNodeNotFound) {
		t.Fatalf("remove missing err=%v", err)
	}
	if err := r.RemoveNode(2); err != nil {
		t.Fatalf("remove node2: %v", err)
	}

	// Last node with keys is refused; removing it empty is allowed.
	if err := r.RemoveNode(1); !errors.Is(err, consistenthash.ErrLastNodeBusy) {
		t.Fatalf("last busy err=%v", err)
	}
	if _, err := r.Lookup(""); !errors.Is(err, consistenthash.ErrInvalidKey) {
		t.Fatalf("lookup empty err=%v", err)
	}
	if err := r.Delete("dup"); err != nil {
		t.Fatal(err)
	}
	if err := r.Delete("next"); err != nil {
		t.Fatal(err)
	}
	if err := r.RemoveNode(1); err != nil {
		t.Fatalf("remove empty last node: %v", err)
	}

	if err := r.Put("z", 0); !errors.Is(err, consistenthash.ErrNoNode) {
		t.Fatalf("put after all nodes removed err=%v", err)
	}
	if _, _, err := r.Rebalance(1); !errors.Is(err, consistenthash.ErrNoNode) {
		t.Fatalf("rebalance no node err=%v", err)
	}

	r2, _ := consistenthash.New(1, 1)
	mustAdd(t, r2, 1, []uint64{10})
	if _, _, err := r2.Rebalance(-1); !errors.Is(err, consistenthash.ErrInvalidLimit) {
		t.Fatalf("limit -1 err=%v", err)
	}
	if _, _, err := r2.Rebalance(1_000_000_001); !errors.Is(err, consistenthash.ErrInvalidLimit) {
		t.Fatalf("limit huge err=%v", err)
	}
}

// TestDeterministicReplay runs the same operation stream twice and checks
// owners, loads and migration lists are byte-identical.
func TestDeterministicReplay(t *testing.T) {
	script := func(r *consistenthash.Ring) (map[string]int64, map[int64]int, [][]consistenthash.Migration) {
		mustAdd(t, r, 3, []uint64{90, 25})
		mustAdd(t, r, 1, []uint64{10, 50})
		mustAdd(t, r, 2, []uint64{30, 70})
		for i, pos := range []uint64{5, 8, 12, 14, 95, 92, 91, 3, 60, 71} {
			if err := r.Put(string(rune('a'+i)), pos); err != nil {
				t.Fatal(err)
			}
		}
		m1, _, _ := r.Rebalance(2)
		if err := r.RemoveNode(2); err != nil {
			t.Fatal(err)
		}
		mustAdd(t, r, 4, []uint64{60})
		m2, _, _ := r.Rebalance(10)
		owners := map[string]int64{}
		for i := 0; i < 10; i++ {
			id, err := r.Lookup(string(rune('a' + i)))
			if err != nil {
				t.Fatal(err)
			}
			owners[string(rune('a'+i))] = id
		}
		return owners, r.Load(), [][]consistenthash.Migration{m1, m2}
	}

	r1, _ := consistenthash.New(5, 4)
	o1, l1, m1 := script(r1)
	r2, _ := consistenthash.New(5, 4)
	o2, l2, m2 := script(r2)

	for k := range o1 {
		if o1[k] != o2[k] {
			t.Fatalf("owner mismatch for %s: %d vs %d", k, o1[k], o2[k])
		}
	}
	for id := range l1 {
		if l1[id] != l2[id] {
			t.Fatalf("load mismatch node %d: %d vs %d", id, l1[id], l2[id])
		}
	}
	for i := range m1 {
		if len(m1[i]) != len(m2[i]) {
			t.Fatalf("migration set %d length %d vs %d", i, len(m1[i]), len(m2[i]))
		}
		for j := range m1[i] {
			if m1[i][j] != m2[i][j] {
				t.Fatalf("migration %d/%d %+v vs %+v", i, j, m1[i][j], m2[i][j])
			}
		}
	}
}

// TestConcurrentCalls hammers the ring from many goroutines with -race in
// mind, and checks the load invariant at the end.
func TestConcurrentCalls(t *testing.T) {
	r, _ := consistenthash.New(3, 2)
	mustAdd(t, r, 1, []uint64{10})
	mustAdd(t, r, 2, []uint64{20})
	mustAdd(t, r, 3, []uint64{30})

	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 100; i++ {
				key := string(rune('A'+g)) + "-" + itoa(i)
				_ = r.Put(key, uint64((g*7+i*13)%100))
				_, _ = r.Lookup(key)
				_, _, _ = r.Rebalance(3)
				if i%5 == 0 {
					_ = r.Delete(key)
				}
			}
		}(g)
	}
	wg.Wait()

	total := 0
	for _, l := range r.Load() {
		total += l
	}
	count := 0
	for g := 0; g < 8; g++ {
		for i := 0; i < 100; i++ {
			key := string(rune('A'+g)) + "-" + itoa(i)
			if _, err := r.Lookup(key); err == nil {
				count++
			}
		}
	}
	if total != count {
		t.Fatalf("load sum %d != live keys %d", total, count)
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}

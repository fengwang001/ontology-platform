package ring

import (
	"math"
	"sort"
	"sync"
	"testing"

	"ontology/hashk"
)

// naiveRef is an independent sorted + linear-scan reference (with wrap).
func naiveRef(v int, nodes []uint32, key uint32) uint32 {
	type slot struct{ pos, node uint32 }
	ss := make([]slot, 0, v*len(nodes))
	for _, id := range nodes {
		for i := 0; i < v; i++ {
			ss = append(ss, slot{hashk.VNodePos(id, i), id})
		}
	}
	sort.Slice(ss, func(a, b int) bool { return ss[a].pos < ss[b].pos })
	h := hashk.H(key)
	for _, s := range ss {
		if s.pos >= h {
			return s.node
		}
	}
	return ss[0].node
}

func TestOwnershipDetermined(t *testing.T) {
	cases := []struct {
		v     int
		nodes []uint32
	}{{2, []uint32{1, 2, 3}}, {3, []uint32{7, 11, 42, 99, 1000}}}
	for _, c := range cases {
		r := New(c.v)
		member := map[uint32]bool{}
		for _, id := range c.nodes {
			if !r.Add(id) {
				t.Fatalf("add %d rejected", id)
			}
			member[id] = true
		}
		for k := uint32(0); k < 2000; k++ {
			got, ok := r.Get(k)
			if !ok || !member[got] {
				t.Fatalf("key %d -> %d ok=%v, want a real member", k, got, ok)
			}
		}
	}
}

func TestGetMatchesNaive(t *testing.T) {
	cases := []struct {
		v     int
		nodes []uint32
	}{{2, []uint32{1, 2, 3}}, {4, []uint32{1, 5, 50, 500, 9999}}}
	for _, c := range cases {
		r := New(c.v)
		for _, id := range c.nodes {
			r.Add(id)
		}
		keys := []uint32{10, 20, 30, 40, 50, 60, 70, 80, 256}
		for k := uint32(0); k < 1000; k++ {
			keys = append(keys, k*65537+12345)
		}
		for _, k := range keys {
			if got, _ := r.Get(k); got != naiveRef(c.v, c.nodes, k) {
				t.Fatalf("v=%d key=%d -> %d, naive %d", c.v, k, got, naiveRef(c.v, c.nodes, k))
			}
		}
		// key 256 hashes exactly to node1's vnode 0x3779b100: >= must hold.
		if got, _ := r.Get(256); got != 1 {
			t.Fatalf("exact-position key 256 -> %d, want 1", got)
		}
	}
}

func TestRemoveConsistency(t *testing.T) {
	r := New(2)
	for _, id := range []uint32{1, 2, 3} {
		r.Add(id)
	}
	if !r.Remove(3) || r.NodeCount() != 2 {
		t.Fatalf("remove failed: count %d", r.NodeCount())
	}
	for k := uint32(0); k < 5000; k++ {
		if got, _ := r.Get(k); got == 3 {
			t.Fatalf("key %d still owned by removed node 3", k)
		}
	}
	for k, want := range map[uint32]uint32{30: 1, 70: 2, 80: 1} {
		if got, _ := r.Get(k); got != want {
			t.Fatalf("key %d -> %d, want %d after remove", k, got, want)
		}
	}
}

func TestGetCheckCountLogarithmic(t *testing.T) {
	for _, m := range []int{100, 1000, 5000, 10000} {
		r := New(1) // m vnodes = m nodes
		for id := uint32(1); id <= uint32(m); id++ {
			r.Add(id)
		}
		bound := int(math.Ceil(math.Log2(float64(m)))) + 2
		for _, k := range []uint32{0, 1, 50, 123456789, 4000000000} {
			r.Get(k)
			if got := int(r.lastChecks.Load()); got > bound {
				t.Fatalf("m=%d key=%d checks=%d > bound %d", m, k, got, bound)
			}
		}
	}
}

func TestConcurrentGetConsistent(t *testing.T) {
	v, nodes := 3, []uint32{1, 2, 3, 4, 5, 6, 7, 8}
	r := New(v)
	for _, id := range nodes {
		r.Add(id)
	}
	keys := make([]uint32, 256)
	for i := range keys {
		keys[i] = uint32(i)*16000001 + 7
	}
	want := make([]uint32, len(keys))
	for i, k := range keys {
		want[i] = naiveRef(v, nodes, k)
	}
	const n = 16
	var wg sync.WaitGroup
	for g := 0; g < n; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i, k := range keys {
				if got, ok := r.Get(k); !ok || got != want[i] {
					t.Errorf("key=%d got=%d ok=%v want=%d", k, got, ok, want[i])
					return
				}
			}
		}()
	}
	wg.Wait()
}

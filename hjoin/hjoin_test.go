package hjoin

import (
	"reflect"
	"sort"
	"sync"
	"testing"

	"ontology/part"
)

func lcg(seed, n, mod int) []part.Key {
	out := make([]part.Key, n)
	x := seed
	for i := range out {
		x = (x*1103515245 + 12345) & 0x7fffffff
		out[i] = part.Key(x % mod)
	}
	return out
}

func sorted(ps []Pair) []Pair {
	sort.Slice(ps, func(i, j int) bool {
		if ps[i].P != ps[j].P {
			return ps[i].P < ps[j].P
		}
		return ps[i].B < ps[j].B
	})
	if ps == nil {
		return []Pair{}
	}
	return ps
}

// naivePairs is the reference nested-loop join: one pair per equal (p,b).
func naivePairs(b, p []part.Key) []Pair {
	out := []Pair{}
	for _, pk := range p {
		for _, bk := range b {
			if pk == bk {
				out = append(out, Pair{P: pk, B: bk})
			}
		}
	}
	return sorted(out)
}

// Invariant 1: Probe multiset equals naive nested loop.
func TestNaiveEquivalence(t *testing.T) {
	cases := []struct {
		name string
		n, m int
		b, p []part.Key
	}{
		{"empty", 2, 1, nil, []part.Key{1}},
		{"canonical", 4, 2, []part.Key{1, 5, 2, 6, 9, 3}, []part.Key{5, 9, 3, 2, 1, 7}},
		{"dup-product", 2, 10, []part.Key{2, 2, 2}, []part.Key{2, 2}},
	}
	for sz := 1; sz <= 64; sz *= 2 {
		cases = append(cases, struct {
			name string
			n, m int
			b, p []part.Key
		}{"random", 2 + sz%5, 1 + sz%3, lcg(sz, sz*3, 17), lcg(sz+99, sz*2, 23)})
	}
	for _, c := range cases {
		j := New(c.n, c.m)
		j.Build(c.b)
		if got := sorted(j.Probe(c.p)); !reflect.DeepEqual(got, naivePairs(c.b, c.p)) {
			t.Fatalf("%s: got %v want naive %v", c.name, got, naivePairs(c.b, c.p))
		}
	}
}

// Invariant 2: every build key lands in the partition part.H assigns, and
// probe locates that same partition.
func TestPartitionConsistency(t *testing.T) {
	for _, n := range []int{2, 3, 4, 5, 16} {
		keys := lcg(n*7+1, 50, n*4)
		j := New(n, 1000)
		j.Build(keys)
		for _, k := range keys {
			p := part.H(k, n)
			found := j.parts[p].mem[k] + j.parts[p].spill[k]
			if found == 0 {
				t.Fatalf("key %d not in its h-partition %d (n=%d)", k, p, n)
			}
		}
	}
}

// Invariant 3: a spilling threshold and a threshold that never spills give
// the identical multiset, both equal to naive.
func TestSpillDoesNotChangeResult(t *testing.T) {
	// Deterministic spill: n=4, m=2 puts [1,5,9] together (3 > 2).
	sp := New(4, 2)
	sp.Build([]part.Key{1, 5, 2, 6, 9, 3})
	if !sp.parts[1].spilled {
		t.Fatal("partition 1 must spill: 3 build tuples > m=2")
	}
	for sz := 1; sz <= 128; sz *= 2 {
		b, p := lcg(sz, sz*3, 11), lcg(sz+7, sz*2, 13)
		lo, hi := New(5, 1), New(5, 1_000_000)
		lo.Build(b)
		hi.Build(b)
		gl, gh := sorted(lo.Probe(p)), sorted(hi.Probe(p))
		if !reflect.DeepEqual(gl, gh) || !reflect.DeepEqual(gl, naivePairs(b, p)) {
			t.Fatalf("sz=%d spill/no-spill/naive differ", sz)
		}
	}
}

// Complexity: locating one probe key checks exactly 1 partition regardless
// of how many nonempty partitions exist (direct h-location, never a scan).
func TestPartitionLocationIsDirect(t *testing.T) {
	for _, m := range []int{100, 1000, 10000} {
		keys := make([]part.Key, m)
		for i := range keys {
			keys[i] = part.Key(i)
		}
		j := New(m, m)
		j.Build(keys)
		if got := j.Probe([]part.Key{0}); len(got) != 1 {
			t.Fatalf("m=%d: got %d matches, want 1", m, len(got))
		}
		if c := j.partitionsChecked.Load(); c != 1 {
			t.Fatalf("m=%d: checked %d partitions, want 1", m, c)
		}
	}
}

// Concurrency: many goroutines probing one built instance read-only must
// all get the identical multiset (synchronization only, no sleeps).
func TestConcurrentProbes(t *testing.T) {
	b, p := lcg(3, 300, 19), lcg(5, 200, 23)
	j := New(7, 2)
	j.Build(b)
	want := sorted(j.Probe(p))
	var wg sync.WaitGroup
	for g := 0; g < 32; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if got := sorted(j.Probe(p)); !reflect.DeepEqual(got, want) {
				t.Errorf("concurrent probe got %v want %v", got, want)
			}
		}()
	}
	wg.Wait()
}

package extendhash

import (
	"errors"
	"fmt"
	"math/rand"
	"sort"
	"strings"
	"sync"
	"testing"
)

// hashN uses only the low n bits, simulating arbitrary bit-width hashes.
func hashN(n int) HashFunc[int] {
	mask := uint64(1)<<n - 1
	return func(k int) uint64 { return uint64(k) & mask }
}

// TestRandomParity replays random ops against a naive map and validates the
// index invariants and contents after every single step.
func TestRandomParity(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	idx, _ := New[int, int](Config[int]{BucketCap: 3, HashBits: 12, Hash: hashN(12)})
	naive := map[int]int{}
	rejected := map[string]int{}
	for step := 0; step < 4000; step++ {
		k := rng.Intn(64)
		switch rng.Intn(3) {
		case 0, 1:
			v := rng.Intn(1_000_000)
			err := idx.Insert(k, v)
			if _, exists := naive[k]; exists {
				if !errors.Is(err, ErrDuplicateKey) {
					t.Fatalf("step %d insert existing %d: %v", step, k, err)
				}
			} else if errors.Is(err, ErrOverflow) || errors.Is(err, ErrTooManyBuckets) {
				rejected[err.Error()]++
			} else if err != nil {
				t.Fatalf("step %d insert %d: %v", step, k, err)
			} else {
				naive[k] = v
			}
		case 2:
			got, err := idx.Delete(k)
			want, exists := naive[k]
			if !exists {
				if !errors.Is(err, ErrNotFound) {
					t.Fatalf("step %d delete missing %d: %v", step, k, err)
				}
			} else if err != nil || got != want {
				t.Fatalf("step %d delete %d = (%d,%v), want %d", step, k, got, err, want)
			} else {
				delete(naive, k)
			}
		}
		checkInvariants(t, idx, fmt.Sprintf("step %d", step), naive)
	}
	t.Logf("judge: 4000 random steps match naive map; structural rejections=%v counters=%+v",
		rejected, idx.Counters())
}

// TestDeterministicReplay runs the same operation sequence twice and requires
// item-by-item identical snapshots at every step.
func TestDeterministicReplay(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	type op struct {
		del bool
		k   int
		v   int
	}
	var ops []op
	for i := 0; i < 500; i++ {
		ops = append(ops, op{del: rng.Intn(3) == 0, k: rng.Intn(40), v: i})
	}
	run := func() []string {
		idx, _ := New[int, int](Config[int]{BucketCap: 2, HashBits: 10, Hash: hashN(10)})
		var log []string
		for _, o := range ops {
			if o.del {
				_, _ = idx.Delete(o.k)
			} else {
				_ = idx.Insert(o.k, o.v)
			}
			s := idx.Snapshot()
			log = append(log, fmt.Sprintf("G=%d|%v|%s|%+v",
				s.GlobalDepth, s.Directory, bucketsString(s), idx.Counters()))
		}
		return log
	}
	a, b := run(), run()
	if len(a) != len(b) {
		t.Fatal("length mismatch")
	}
	for i := range a {
		if a[i] != b[i] {
			t.Fatalf("step %d nondeterministic:\n%s\n%s", i, a[i], b[i])
		}
	}
	t.Logf("judge: deterministic replay identical across %d steps", len(ops))
}

// TestConcurrent hammers the index with concurrent readers and writers under
// the race detector; readers must never observe a half-split/half-merged state.
func TestConcurrent(t *testing.T) {
	idx, _ := New[int, int](Config[int]{BucketCap: 4, HashBits: 16, Hash: hashN(16)})
	var wg sync.WaitGroup
	stop := make(chan struct{})
	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func(seed int64) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(seed))
			for {
				select {
				case <-stop:
					return
				default:
				}
				k := rng.Intn(200)
				if rng.Intn(2) == 0 {
					_ = idx.Insert(k, int(seed))
				} else {
					_, _ = idx.Delete(k)
				}
			}
		}(int64(w))
	}
	for r := 0; r < 4; r++ {
		wg.Add(1)
		go func(seed int64) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(seed + 100))
			for {
				select {
				case <-stop:
					return
				default:
				}
				k := rng.Intn(200)
				v, err := idx.Lookup(k)
				if err == nil && v < 0 {
					t.Errorf("impossible value %d", v)
				}
			}
		}(int64(r))
	}
	for i := 0; i < 200; i++ {
		s := idx.Snapshot()
		G := s.GlobalDepth
		if len(s.Directory) != 1<<G {
			t.Fatalf("reader observed torn directory: %d vs %d", len(s.Directory), 1<<G)
		}
		refs := map[int]int{}
		for _, bid := range s.Directory {
			refs[bid]++
		}
		for bid, b := range s.Buckets {
			if refs[bid] != 1<<(G-b.LocalDepth) {
				t.Fatalf("reader observed torn bucket %d refs", bid)
			}
		}
	}
	close(stop)
	wg.Wait()
	t.Logf("judge: concurrent run finished with consistent snapshots, counters=%+v", idx.Counters())
}

// checkInvariants validates every structural invariant and fails with a
// human-readable reason when something is wrong. It also cross-checks the
// index against a naive map when one is supplied.
func checkInvariants(t *testing.T, idx *Index[int, int], label string, want map[int]int) {
	t.Helper()
	idx.mu.RLock()
	defer idx.mu.RUnlock()

	G := idx.globalDepth
	if len(idx.directory) != 1<<G {
		t.Fatalf("%s: directory size %d != 2^%d", label, len(idx.directory), G)
	}
	refs := map[int]int{}
	for i, bid := range idx.directory {
		b, ok := idx.buckets[bid]
		if !ok {
			t.Fatalf("%s: directory[%d] -> missing bucket %d", label, i, bid)
		}
		if b.localDepth > G {
			t.Fatalf("%s: bucket %d localDepth %d > global %d", label, bid, b.localDepth, G)
		}
		refs[bid]++
	}
	totalKeys := 0
	for bid, b := range idx.buckets {
		if len(b.entries) > idx.cfg.BucketCap {
			t.Fatalf("%s: bucket %d has %d entries > cap %d", label, bid, len(b.entries), idx.cfg.BucketCap)
		}
		exp := 1 << (G - b.localDepth)
		if refs[bid] != exp {
			t.Fatalf("%s: bucket %d depth %d referenced %d times, want %d",
				label, bid, b.localDepth, refs[bid], exp)
		}
		seen := map[int]bool{}
		for _, e := range b.entries {
			if seen[e.Key] {
				t.Fatalf("%s: duplicate key %v in bucket %d", label, e.Key, bid)
			}
			seen[e.Key] = true
			totalKeys++
			if idx.directory[prefix(idx.cfg.Hash(e.Key), G)] != bid {
				t.Fatalf("%s: key %v lives in bucket %d but its prefix routes elsewhere", label, e.Key, bid)
			}
		}
	}
	if want != nil && totalKeys != len(want) {
		t.Fatalf("%s: index holds %d keys, naive map holds %d", label, totalKeys, len(want))
	}
	for k, v := range want {
		got, err := lookupLocked(idx, k)
		if err != nil || got != v {
			t.Fatalf("%s: lookup(%v) = (%v,%v), want (%v,nil)", label, k, got, err, v)
		}
	}
}

func lookupLocked(idx *Index[int, int], key int) (int, error) {
	b := idx.buckets[idx.directory[prefix(idx.cfg.Hash(key), idx.globalDepth)]]
	for _, e := range b.entries {
		if e.Key == key {
			return e.Value, nil
		}
	}
	return 0, ErrNotFound
}

func mustInsert(t *testing.T, idx *Index[int, int], k, v int) {
	t.Helper()
	if err := idx.Insert(k, v); err != nil {
		t.Fatalf("insert %d: %v", k, err)
	}
}

// snapEqual compares snapshots field-by-field (map contents included).
func snapEqual(a, b Snapshot[int, int]) bool {
	if a.GlobalDepth != b.GlobalDepth || len(a.Directory) != len(b.Directory) {
		return false
	}
	for i := range a.Directory {
		if a.Directory[i] != b.Directory[i] {
			return false
		}
	}
	if len(a.Buckets) != len(b.Buckets) {
		return false
	}
	for id, ba := range a.Buckets {
		bb, ok := b.Buckets[id]
		if !ok || ba.LocalDepth != bb.LocalDepth || len(ba.Entries) != len(bb.Entries) {
			return false
		}
		for i := range ba.Entries {
			if ba.Entries[i] != bb.Entries[i] {
				return false
			}
		}
	}
	return true
}

func bucketsString(s Snapshot[int, int]) string {
	ids := make([]int, 0, len(s.Buckets))
	for id := range s.Buckets {
		ids = append(ids, id)
	}
	sort.Ints(ids)
	var parts []string
	for _, id := range ids {
		b := s.Buckets[id]
		parts = append(parts, fmt.Sprintf("%d(d%d)%v", id, b.LocalDepth, b.Entries))
	}
	return strings.Join(parts, ";")
}

// TestLowBitsPileup forces repeated splits when many keys share low bits.
func TestLowBitsPileup(t *testing.T) {
	idx, err := New[int, int](Config[int]{BucketCap: 2, HashBits: 10, Hash: hashN(10)})
	if err != nil {
		t.Fatal(err)
	}
	naive := map[int]int{}
	keys := []int{0, 4, 8, 12, 16, 20, 24, 28}
	for _, k := range keys {
		err := idx.Insert(k, k)
		s := idx.Snapshot()
		t.Logf("input=Insert(%d) output=%v judge: G=%d dir=%v counters=%+v",
			k, err, s.GlobalDepth, s.Directory, idx.Counters())
		if err != nil {
			t.Fatalf("insert %d: %v", k, err)
		}
		naive[k] = k
		checkInvariants(t, idx, fmt.Sprintf("after insert %d", k), naive)
	}
	c := idx.Counters()
	// Insert(8) alone needs three consecutive splits+doubles to find a
	// separating bit; later keys add two more. The structural invariant
	// (validated after every step) is the real judge; these numbers assert
	// the multi-split path is actually exercised.
	if c.Doubles != 4 || c.Splits != 5 {
		t.Fatalf("counters = %+v, want Doubles=4 Splits=5", c)
	}
	t.Logf("judge: pileup settled with %+v", c)
}

// TestOverflowAndLimitRollback verifies rejected structural inserts undo
// every split/double performed within the rejected operation.
func TestOverflowAndLimitRollback(t *testing.T) {
	t.Run("overflow", func(t *testing.T) {
		idx, _ := New[int, int](Config[int]{BucketCap: 2, HashBits: 3, Hash: hashN(3)})
		mustInsert(t, idx, 0, 0)
		mustInsert(t, idx, 8, 8) // 8 mod 8 == 0
		before, beforeC := idx.Snapshot(), idx.Counters()
		err := idx.Insert(16, 16) // 16 mod 8 == 0 as well
		t.Logf("input=Insert(16) output=%v judge: overflow at depth 3, counters=%+v unchanged=%v",
			err, idx.Counters(), idx.Counters() == beforeC)
		if !errors.Is(err, ErrOverflow) {
			t.Fatalf("want ErrOverflow, got %v", err)
		}
		if idx.Counters() != beforeC {
			t.Fatal("counters changed despite rejection")
		}
		if !snapEqual(before, idx.Snapshot()) {
			t.Fatal("snapshot changed after overflow rollback")
		}
		checkInvariants(t, idx, "after overflow", map[int]int{0: 0, 8: 8})
	})

	t.Run("too many buckets", func(t *testing.T) {
		idx, _ := New[int, int](Config[int]{BucketCap: 1, HashBits: 10, MaxBuckets: 2, Hash: hashN(10)})
		mustInsert(t, idx, 0, 0) // one double + one split -> two buckets
		before, beforeC := idx.Snapshot(), idx.Counters()
		err := idx.Insert(2, 2)
		t.Logf("input=Insert(2) output=%v judge: third bucket forbidden, counters=%+v unchanged=%v",
			err, idx.Counters(), idx.Counters() == beforeC)
		if !errors.Is(err, ErrTooManyBuckets) {
			t.Fatalf("want ErrTooManyBuckets, got %v", err)
		}
		if idx.Counters() != beforeC || !snapEqual(before, idx.Snapshot()) {
			t.Fatal("index changed despite MaxBuckets rejection")
		}
		checkInvariants(t, idx, "after limit reject", map[int]int{0: 0})
	})
}

// TestMergeAndShrink deletes keys in an order that triggers cascading
// merges and consecutive directory halvings.
func TestMergeAndShrink(t *testing.T) {
	idx, _ := New[int, int](Config[int]{BucketCap: 2, HashBits: 10, Hash: hashN(10)})
	keys := []int{0, 4, 8, 12, 16}
	naive := map[int]int{}
	for _, k := range keys {
		mustInsert(t, idx, k, k)
		naive[k] = k
	}
	checkInvariants(t, idx, "built", naive)

	for _, k := range []int{16, 12, 8, 4} {
		v, err := idx.Delete(k)
		if err != nil || v != k {
			t.Fatalf("delete %d = (%d,%v)", k, v, err)
		}
		delete(naive, k)
		s := idx.Snapshot()
		t.Logf("input=Delete(%d) output=%d judge: G=%d dir=%v counters=%+v",
			k, v, s.GlobalDepth, s.Directory, idx.Counters())
		checkInvariants(t, idx, fmt.Sprintf("after delete %d", k), naive)
	}
	s := idx.Snapshot()
	if s.GlobalDepth != 0 || len(s.Directory) != 1 || len(s.Buckets) != 1 {
		t.Fatalf("did not fully shrink: G=%d dir=%v buckets=%d", s.GlobalDepth, s.Directory, len(s.Buckets))
	}
	c := idx.Counters()
	if c.Splits == 0 || c.Merges != c.Splits || c.Doubles != c.Shrinks || c.Shrinks == 0 {
		t.Fatalf("split/merge or double/shrink mismatch: %+v", c)
	}
	t.Logf("judge: fully merged and shrunk back, %+v", c)
}

// TestDuplicateAndMissing covers the other distinguishable rejection reasons.
func TestDuplicateAndMissing(t *testing.T) {
	idx, _ := New[int, int](Config[int]{BucketCap: 4, HashBits: 8, Hash: hashN(8)})
	mustInsert(t, idx, 1, 100)
	before := idx.Snapshot()
	err := idx.Insert(1, 200)
	t.Logf("input=Insert(1,200) output=%v judge: duplicate key must be rejected distinctly", err)
	if !errors.Is(err, ErrDuplicateKey) {
		t.Fatalf("duplicate: want ErrDuplicateKey, got %v", err)
	}
	if _, err := idx.Lookup(2); !errors.Is(err, ErrNotFound) {
		t.Fatalf("lookup missing: want ErrNotFound, got %v", err)
	}
	if _, err := idx.Delete(2); !errors.Is(err, ErrNotFound) {
		t.Fatalf("delete missing: want ErrNotFound, got %v", err)
	}
	t.Logf("input=Lookup(2)/Delete(2) output=ErrNotFound judge: missing keys distinguished")
	if !snapEqual(before, idx.Snapshot()) {
		t.Fatal("rejected ops changed the index")
	}
}

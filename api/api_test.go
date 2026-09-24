package api

import (
	"fmt"
	"maps"
	"math/rand"
	"slices"
	"sync"
	"sync/atomic"
	"testing"

	"ontology/group"
)

func TestSevenBatchScenario(t *testing.T) {
	a, want, cum := New(7, 8), []int{0, 3, 2, 1, 2, 1, 2}, 0
	for i, b := range selfBatches {
		m, err := a.Rebalance(b...)
		if err != nil || m != want[i] {
			t.Fatalf("batch %d: mig=%d err=%v", i, m, err)
		}
		cum += m
	}
	if cum != 11 || fmt.Sprint(a.Assignment()) != "map[b:[0 1 3] d:[2 5] e:[4 6]]" {
		t.Fatalf("cum=%d final=%v", cum, a.Assignment())
	}
}

// randSeq builds random valid batches by choosing each post-batch member
// set first; the batch is the set difference, so every id appears at most once.
func randSeq(seed int64) [][]Change {
	r, pool := rand.New(rand.NewSource(seed)), []string{"a", "b", "c", "d"}
	var cur []string
	var batches [][]Change
	for range 10 {
		next := slices.Clone(cur)
		for i := 0; i < len(next); { // maybe leave current members
			if r.Intn(3) == 0 {
				next = append(next[:i], next[i+1:]...)
			} else {
				i++
			}
		}
		for _, id := range pool { // maybe join absent members
			if !slices.Contains(next, id) && len(next) < 4 && r.Intn(2) == 0 {
				next = append(next, id)
			}
		}
		ch := []Change{}
		for _, id := range cur {
			if !slices.Contains(next, id) {
				ch = append(ch, Leaving(id))
			}
		}
		for _, id := range next {
			if !slices.Contains(cur, id) {
				ch = append(ch, Joining(id))
			}
		}
		cur = next
		if len(ch) > 0 {
			batches = append(batches, ch)
		}
	}
	return batches
}

func replay(seed int64, rev bool) ([]map[string][]int, []int) {
	a := New(7, 8)
	var snaps []map[string][]int
	var migs []int
	for _, b0 := range randSeq(seed) {
		b := slices.Clone(b0)
		if rev {
			slices.Reverse(b)
		}
		m, err := a.Rebalance(b...)
		if err != nil {
			panic(err)
		}
		snaps, migs = append(snaps, a.Assignment()), append(migs, m)
	}
	return snaps, migs
}

func checkSeed(t *testing.T, seed int64) {
	t.Helper()
	snaps, migs := replay(seed, false)
	prev := map[int]string{}
	for i, snap := range snaps {
		if err := checkBalanced(7, snap); err != nil {
			t.Fatalf("seed %d batch %d: %v", seed, i, err)
		}
		if w := bruteMin(7, slices.Sorted(maps.Keys(snap)), prev); migs[i] != w {
			t.Fatalf("seed %d batch %d: %d != min %d", seed, i, migs[i], w)
		}
		prev = map[int]string{}
		for id, ps := range snap {
			for _, p := range ps {
				prev[p] = id
			}
		}
	}
}

func TestBruteForceMinimal(t *testing.T) {
	for _, s := range []int64{1, 2, 3, 4, 5} {
		checkSeed(t, s)
	}
}

func TestBalancedComplete(t *testing.T) {
	for _, s := range []int64{6, 7, 8, 12, 13} {
		checkSeed(t, s)
	}
}

func TestDeterministic(t *testing.T) {
	for _, s := range []int64{9, 10, 11} {
		s1, m1 := replay(s, false)
		s2, m2 := replay(s, true)
		if fmt.Sprint(s1, m1) != fmt.Sprint(s2, m2) {
			t.Fatalf("seed %d: in-batch order changed result", s)
		}
	}
}

var sentinelCases = []struct {
	ch   []Change
	want error
}{
	{[]Change{Joining("")}, group.ErrEmptyID},
	{[]Change{Joining("x")}, group.ErrDuplicate},
	{[]Change{Joining("z"), Leaving("z")}, group.ErrDuplicate},
	{[]Change{Leaving("ghost")}, group.ErrAbsent},
	{[]Change{Joining("z"), Joining("w")}, group.ErrTooManyMember},
}

func TestSentinelErrorsDistinct(t *testing.T) {
	a := New(4, 2)
	a.Rebalance(Joining("x"), Joining("y"))
	for i, c := range sentinelCases {
		if _, err := a.Rebalance(c.ch...); err != c.want {
			t.Errorf("case %d: err=%v want %v", i, err, c.want)
		}
	}
}

func TestRejectedBatchNoTrace(t *testing.T) {
	a := New(4, 2)
	a.Rebalance(Joining("x"), Joining("y"))
	before, gen := fmt.Sprint(a.Assignment()), a.g.Generation()
	for _, c := range sentinelCases {
		if _, err := a.Rebalance(c.ch...); err == nil {
			t.Fatalf("invalid batch %v accepted", c.ch)
		}
	}
	if fmt.Sprint(a.Assignment()) != before || a.g.Generation() != gen {
		t.Fatal("rejected batch mutated state or generation")
	}
	if _, err := a.Rebalance(Leaving("x")); err != nil { // still usable
		t.Fatal(err)
	}
}

func TestConcurrentJoins(t *testing.T) {
	const n = 8
	a := New(n, n)
	var done atomic.Bool
	var wg sync.WaitGroup
	wg.Add(1)
	go func() { // reader: invariant 2 must hold for every observed snapshot
		defer wg.Done()
		for !done.Load() {
			snap := a.Assignment()
			if err := checkBalanced(n, snap); err != nil {
				t.Error(err)
				return
			}
		}
	}()
	var jwg sync.WaitGroup
	for i := range n {
		jwg.Add(1)
		go func(i int) {
			defer jwg.Done()
			if _, err := a.Rebalance(Joining(fmt.Sprint(i))); err != nil {
				t.Error(err)
			}
		}(i)
	}
	jwg.Wait()
	done.Store(true)
	wg.Wait()
	got := a.Assignment()
	if len(got) != n {
		t.Fatalf("members=%d want %d", len(got), n)
	}
	for id, ps := range got {
		if len(ps) != 1 {
			t.Fatalf("%s holds %v, want exactly one partition", id, ps)
		}
	}
}

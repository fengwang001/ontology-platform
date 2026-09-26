package api_test

import (
	"errors"
	"math/rand"
	"sort"
	"sync"
	"testing"

	"ontology/api"
)

type elem [2]int64

// naive is the deliberately simple O(m log m) reference definition.
func naive(es []elem) int64 {
	s := append([]elem(nil), es...)
	sort.Slice(s, func(i, j int) bool { return s[i][0] < s[j][0] })
	var w, p int64
	for _, e := range s {
		w += e[1]
	}
	for _, e := range s {
		if p += e[1]; 2*p >= w {
			return e[0]
		}
	}
	return s[len(s)-1][0]
}

// build inserts n distinct values in random order and returns the pairs.
func build(r *rand.Rand, n int) (*api.Container, []elem) {
	c, els := api.New(), make([]elem, 0, n)
	for _, v := range r.Perm(n) { // permuted values unique, weights > 0
		e := elem{int64(v), int64(1 + r.Intn(9))}
		c.Insert(e[0], e[1])
		els = append(els, e)
	}
	return c, els
}

// conformance builds random sets and asserts every median invariant
// (naive agreement, two-sided weight bounds, minimality). Each named test
// drives it with its own seed and size ladder, so each pins its invariant.
func conformance(t *testing.T, seed int64, sizes []int) {
	t.Helper()
	r := rand.New(rand.NewSource(seed))
	for _, n := range sizes {
		c, es := build(r, n)
		m, err := c.Median()
		if err != nil {
			t.Fatal(err)
		}
		var below, above int64
		for _, e := range es {
			if e[0] < m {
				below += e[1]
			} else if e[0] > m {
				above += e[1]
			}
		}
		if m != naive(es) {
			t.Fatalf("n=%d median %d != naive %d", n, m, naive(es))
		}
		if w := c.Total(); 2*below > w || 2*above > w {
			t.Fatalf("n=%d side bounds below=%d above=%d W=%d", n, below, above, w)
		}
		if 2*below >= c.Total() {
			t.Fatalf("n=%d not minimal below=%d W=%d", n, below, c.Total())
		}
	}
}

func TestNaiveAgreement(t *testing.T)   { conformance(t, 42, []int{1, 2, 3, 7, 50, 200}) }
func TestSideWeightBounds(t *testing.T) { conformance(t, 7, []int{1, 2, 5, 60, 300}) }
func TestMinimality(t *testing.T)       { conformance(t, 99, []int{1, 2, 8, 100}) }

func TestSentinelErrors(t *testing.T) {
	c := api.New()
	_, eEmpty := c.Median()
	eWeight := c.Insert(1, 0)
	c.Insert(1, 1)
	eDup := c.Insert(1, 1)
	if !errors.Is(eEmpty, api.ErrEmpty) || !errors.Is(eWeight, api.ErrNonPositiveWeight) || !errors.Is(eDup, api.ErrDuplicateValue) {
		t.Fatalf("errors not identifiable: %v %v %v", eEmpty, eWeight, eDup)
	}
	if eWeight == eDup || eWeight == eEmpty || eDup == eEmpty {
		t.Fatal("the three sentinel errors must be distinct")
	}
	if err := c.SelfCheck(); err != nil {
		t.Fatalf("SelfCheck: %v", err)
	}
}

func TestRejectedOpsNoTrace(t *testing.T) {
	c := api.New()
	c.Insert(5, 4)
	c.Insert(1, 2)
	w0 := c.Total()
	m0, _ := c.Median()
	for _, e := range []elem{{5, 9}, {6, 0}, {6, -2}, {1, 1}} {
		if c.Insert(e[0], e[1]) == nil {
			t.Fatalf("insert %v should have been rejected", e)
		}
	}
	if c.Total() != w0 {
		t.Fatalf("Total changed %d -> %d after rejects", w0, c.Total())
	}
	if m, _ := c.Median(); m != m0 {
		t.Fatalf("Median changed %d -> %d after rejects", m0, m)
	}
	if err := c.Insert(9, 3); err != nil { // still usable afterwards
		t.Fatalf("valid insert after rejects failed: %v", err)
	}
}

func TestConcurrentMedians(t *testing.T) {
	r := rand.New(rand.NewSource(3))
	c, _ := build(r, 3000)
	want, err := c.Median()
	if err != nil {
		t.Fatal(err)
	}
	const N = 64
	var wg sync.WaitGroup
	res := make([]int64, N)
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			res[i], _ = c.Median()
		}(i)
	}
	wg.Wait()
	for i, m := range res {
		if m != want {
			t.Fatalf("goroutine %d got %d want %d", i, m, want)
		}
	}
}

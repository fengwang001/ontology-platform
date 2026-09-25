package check_test

import (
	"errors"
	"fmt"
	"slices"
	"sync"
	"testing"

	"ontology/bloom"
	"ontology/check"
)

func vals(tag string, n int) [][]byte {
	v := make([][]byte, n)
	for i := range v {
		v[i] = []byte(fmt.Sprintf("%s-%d", tag, i))
	}
	return v
}

func filled(t *testing.T, n uint64, p float64, tag string) (*bloom.Filter, *check.Set) {
	f, _ := bloom.New(n, p, 42)
	ref := check.NewSet()
	for _, v := range vals(tag, int(n)) {
		f.Add(v)
		ref.Add(v)
	}
	return f, ref
}

func want(t *testing.T, ok bool, msg string) {
	if !ok {
		t.Fatal(msg)
	}
}

func count(has func([]byte) bool, vs [][]byte) (n int) {
	for _, v := range vs {
		if has(v) {
			n++
		}
	}
	return n
}

// mkK1 builds the buggy one-hash variant inline, for comparison only.
func mkK1(m uint64) (add func([]byte), has func([]byte) bool) {
	w := make([]uint64, (m+63)/64)
	pos := func(b []byte) (h uint64) {
		h = 14695981039346656037
		for _, c := range b {
			h = (h ^ uint64(c)) * 1099511628211
		}
		return h % m
	}
	return func(b []byte) { i := pos(b); w[i/64] |= 1 << (i % 64) },
		func(b []byte) bool { i := pos(b); return w[i/64]&(1<<(i%64)) != 0 }
}

func TestBloom(t *testing.T) {
	f, ref := filled(t, 1000, 0.01, "in")
	g, _ := filled(t, 1000, 0.01, "in")
	var z bloom.Filter
	added := vals("in", 1000)
	both := func(v []byte) bool { return f.MaybeContains(v) && ref.Contains(v) }
	diff := func(v []byte) bool { return f.MaybeContains(v) != g.MaybeContains(v) }
	bad := func(n uint64, p float64) bool { _, err := bloom.New(n, p, 1); return errors.Is(err, bloom.ErrBadParam) }
	cases := []testing.InternalTest{
		{"BadParam", func(t *testing.T) { want(t, bad(0, .5) && bad(1, 0) && bad(1, 1) && bad(1, 1.5), "bad param accepted") }},
		{"NoFalseNegative", func(t *testing.T) { want(t, count(both, added) == 1000, "added value judged missing") }},
		{"Deterministic", func(t *testing.T) { want(t, count(diff, added) == 0, "same seed differs") }},
		{"EmptySafe", func(t *testing.T) { want(t, !z.MaybeContains([]byte("x")), "empty filter said true") }},
		{"ReadsEqualK", func(t *testing.T) { f.MaybeContains([]byte("p")); want(t, f.Reads() == f.K(), "reads != k") }},
		{"ConcurrentRead", func(t *testing.T) {
			res := make([]bool, 16)
			var wg sync.WaitGroup
			for i := range res {
				wg.Go(func() { res[i] = count(both, added) == 1000 })
			}
			wg.Wait()
			want(t, !slices.Contains(res, false), "concurrent read mismatch")
		}},
	}
	for _, c := range cases {
		t.Run(c.Name, c.F)
	}
}

func TestFalsePositiveRate(t *testing.T) {
	f, _ := filled(t, 10000, 0.01, "add")
	add, has := mkK1(f.M())
	for _, v := range vals("add", 10000) {
		add(v)
	}
	opt, one := count(f.MaybeContains, vals("miss", 10000)), count(has, vals("miss", 10000))
	want(t, float64(opt)/10000 <= 0.02, "optimal-k false positive rate > 0.02")
	want(t, one > opt, "k=1 false positives not worse than optimal k")
}

package dedup_test

import (
	"errors"
	"math/rand"
	"ontology/api"
	"ontology/dedup"
	"ontology/rec"
	"reflect"
	"sync"
	"testing"
)

// naive is an independent reference replaying [max-w+1,max] eviction from scratch.
func naive(w int, log []dedup.Event) map[int][]dedup.Entry {
	type st struct {
		max  int64
		vals map[int64]string
	}
	m := map[int]*st{}
	for _, e := range log {
		s := m[e.Partition]
		if s == nil {
			s = &st{vals: map[int64]string{}}
			m[e.Partition] = s
		}
		if e.Offset > s.max {
			s.max = e.Offset
			s.vals[e.Offset] = e.Value
			for off := range s.vals {
				if off < e.Offset-int64(w)+1 {
					delete(s.vals, off)
				}
			}
		}
	}
	out := map[int][]dedup.Entry{}
	for p, s := range m {
		for off := s.max - int64(w) + 1; off <= s.max; off++ {
			if v, ok := s.vals[off]; ok {
				out[p] = append(out[p], dedup.Entry{Offset: off, Value: v})
			}
		}
	}
	return out
}
func must(t *testing.T, cond bool, msg string, args ...any) {
	t.Helper()
	if !cond {
		t.Fatalf(msg, args...)
	}
}

// TestInvariantNaiveReference: after random sequences View equals the independent naive rebuild.
func TestInvariantNaiveReference(t *testing.T) {
	for _, w := range []int{1, 4, 9} {
		rng := rand.New(rand.NewSource(int64(w)))
		d, _ := dedup.New(w)
		next := map[int]int64{0: 1, 1: 1, 2: 1}
		var applied []dedup.Event
		valOf := func(off int64) string { return "v" + string(rune('a'+off%26)) }
		for step := 0; step < 300; step++ {
			p := rng.Intn(3)
			mx, lo := next[p]-1, next[p]-int64(w)
			if lo < 1 {
				lo = 1
			}
			x, varE := rng.Intn(5), dedup.Event{}
			switch {
			case x < 3 || mx < lo:
				varE = dedup.Event{Partition: p, Offset: next[p], Value: valOf(next[p])}
			case x == 3:
				off := lo + rng.Int63n(mx-lo+1)
				varE = dedup.Event{Partition: p, Offset: off, Value: valOf(off)}
			case mx >= int64(w) && rng.Intn(2) == 0:
				varE = dedup.Event{Partition: p, Offset: mx - int64(w), Value: "old"}
			case mx >= 1:
				off := lo + rng.Int63n(mx-lo+1)
				varE = dedup.Event{Partition: p, Offset: off, Value: "X"}
			default:
				varE = dedup.Event{Partition: p, Offset: -1, Value: "X"}
			}
			if r, err := d.Apply(varE); err == nil && r.Kind == dedup.Applied {
				next[p]++
				applied = append(applied, varE)
			}
		}
		must(t, reflect.DeepEqual(d.View(), naive(w, applied)) && d.SelfCheck() == nil, "w=%d", w)
	}
}

// TestBatchAtomic: rejected event voids batch; four sentinels differ; usable
// afterwards; also exercises the api facade end to end.
func TestBatchAtomic(t *testing.T) {
	_, err := dedup.New(0)
	must(t, errors.Is(err, dedup.ErrInvalidWindow), "New(0): %v", err)
	s1, s2, s3, s4 := dedup.ErrInvalidWindow, rec.ErrNegativeOffset, rec.ErrConflict, rec.ErrRewound
	must(t, !errors.Is(s1, s2) && !errors.Is(s1, s3) && !errors.Is(s1, s4) &&
		!errors.Is(s2, s3) && !errors.Is(s2, s4) && !errors.Is(s3, s4), "sentinels not distinct")
	ev := func(off int64, v string) dedup.Event { return dedup.Event{Partition: 0, Offset: off, Value: v} }
	ns := []int64{1, 2, 5}
	bads := []dedup.Event{ev(-1, "x"), ev(2, "z"), ev(1, "a")}
	wants := []error{rec.ErrNegativeOffset, rec.ErrConflict, rec.ErrRewound}
	for i := range ns {
		d, _ := dedup.New(4)
		for off := int64(1); off <= ns[i]; off++ {
			_, e := d.Apply(ev(off, "v"))
			must(t, e == nil, "seed: %v", e)
		}
		before := d.View()
		good := ev(ns[i]+1, "ok")
		_, e := d.ApplyBatch([]dedup.Event{good, bads[i]})
		must(t, errors.Is(e, wants[i]), "case %d: %v", i, e)
		must(t, reflect.DeepEqual(d.View(), before), "case %d left a trace", i)
		r, _ := d.Apply(good)
		must(t, r.Kind == dedup.Applied, "case %d unusable after reject", i)
	}
	x, _ := api.New(4)
	rs, ferr := x.Apply([]api.Event{{Partition: 0, Offset: 1, Value: "a"}})
	must(t, ferr == nil && rs[0].Kind == api.Applied, "facade apply")
	rs, ferr = x.Apply([]api.Event{{Partition: 0, Offset: 1, Value: "a"}, {Partition: 0, Offset: 1, Value: "b"}})
	must(t, errors.Is(ferr, api.ErrConflict) && rs == nil && x.View()[0][0].Value == "a" &&
		x.SelfCheck() == nil, "facade failed batch corrupted state")
}

// TestConcurrentView: N goroutines read one fed instance; all views field-identical. No sleeps; -race clean.
func TestConcurrentView(t *testing.T) {
	d, _ := dedup.New(4)
	var seed []dedup.Event
	for off := int64(1); off <= 48; off++ {
		seed = append(seed, dedup.Event{Partition: int((off - 1) / 12), Offset: (off-1)%12 + 1, Value: "v"})
	}
	_, err := d.ApplyBatch(seed)
	must(t, err == nil, "seed: %v", err)
	const n = 32
	var wg sync.WaitGroup
	views := make([]map[int][]dedup.Entry, n)
	errs := make([]error, n)
	wg.Add(n)
	for i := 0; i < n; i++ {
		i := i
		go func() { defer wg.Done(); views[i], errs[i] = d.View(), d.SelfCheck() }()
	}
	wg.Wait()
	for i := 1; i < n; i++ {
		must(t, errs[i] == nil && reflect.DeepEqual(views[i], views[0]), "reader %d differs", i)
	}
}

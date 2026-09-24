package api_test

import (
	"errors"
	"fmt"
	"math/rand"
	"sync"
	"sync/atomic"
	"testing"

	"ontology/api"
)

type spec struct {
	n string
	d []string
	f func(...int64) int64
}

func sumv(x ...int64) (t int64) {
	for _, v := range x {
		t += v
	}
	return
}
func dblv(x ...int64) int64 { return x[0] * 2 }
func must(t *testing.T, e error) {
	t.Helper()
	if e != nil {
		t.Fatal(e)
	}
}

func batch(vs []spec, base map[string]int64) map[string]int64 {
	out := map[string]int64{}
	for _, v := range vs {
		if v.f == nil {
			out[v.n] = base[v.n]
			continue
		}
		a := make([]int64, len(v.d))
		for i, d := range v.d {
			a[i] = out[d]
		}
		out[v.n] = v.f(a...)
	}
	return out
}

func TestRandomConsistency(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	for iter := 0; iter < 40; iter++ {
		n := rng.Intn(8) + 3
		r := api.New()
		vs, bases, baseV := []spec{}, []string{}, map[string]int64{}
		for i := 0; i < n; i++ {
			nm := fmt.Sprintf("v%d", i)
			var ds []string
			if i > 0 && rng.Intn(4) > 0 {
				for _, j := range rng.Perm(i)[:1+rng.Intn(i)] {
					ds = append(ds, fmt.Sprintf("v%d", j))
				}
			}
			f := sumv
			if len(ds) == 0 {
				f, bases, baseV[nm] = nil, append(bases, nm), 0
			}
			vs = append(vs, spec{nm, ds, f})
			must(t, r.AddView(nm, ds, f))
		}
		for _, bn := range bases {
			must(t, r.Set(bn, 0))
		}
		must(t, r.Recompute())
		for step := 0; step < 25; step++ {
			bn, val := bases[rng.Intn(len(bases))], rng.Int63n(30)-15
			baseV[bn] = val
			must(t, r.Set(bn, val))
			must(t, r.Recompute())
			for nm, w := range batch(vs, baseV) {
				if g, has, e := r.Get(nm); e != nil || !has || g != w {
					t.Fatalf("iter %d step %d %s=%d want %d", iter, step, nm, g, w)
				}
			}
		}
	}
}

func TestErrorsRejectionsAndSelfCheck(t *testing.T) {
	r := api.New()
	must(t, r.AddView("A", nil, nil))
	must(t, r.AddView("M", []string{"N"}, nil)) // forward reference allowed
	cases := []struct {
		op   func() error
		want error
	}{
		{func() error { return r.AddView("", nil, nil) }, api.ErrEmptyName},
		{func() error { return r.AddView("A", nil, nil) }, api.ErrExists},
		{func() error { return r.AddView("N", []string{"M"}, nil) }, api.ErrCycle},
		{func() error { return r.Set("z", 1) }, api.ErrUnresolved},
	}
	for i, c := range cases {
		if !errors.Is(c.op(), c.want) {
			t.Errorf("case %d: want %v", i, c.want)
		}
	}
	if _, _, e := r.Get("N"); !errors.Is(e, api.ErrUnresolved) {
		t.Error("rejected N left a trace")
	}
	must(t, r.Set("A", 5))
	must(t, r.Recompute())
	must(t, api.New().SelfCheck())
}

func TestConcurrentReads(t *testing.T) {
	r := api.New()
	must(t, r.AddView("x", nil, nil))
	must(t, r.AddView("y", []string{"x"}, dblv))
	const rounds, readersN = 400, 8
	var stop, bad atomic.Bool
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer stop.Store(true)
		for i := int64(0); i < rounds; i++ {
			if r.Set("x", i) != nil || r.Recompute() != nil {
				bad.Store(true)
			}
		}
	}()
	for k := 0; k < readersN; k++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for !stop.Load() {
				if y, _, _ := r.Get("y"); y%2 != 0 || y > 2*(rounds-1) {
					bad.Store(true)
				}
			}
		}()
	}
	wg.Wait()
	if bad.Load() {
		t.Fatal("illegal/intermediate value observed")
	}
	if y, _, _ := r.Get("y"); y != 2*(rounds-1) {
		t.Fatalf("final y=%d want %d", y, 2*(rounds-1))
	}
}

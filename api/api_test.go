package api_test

import (
	"errors"
	"math/rand/v2"
	"sync"
	"sync/atomic"
	"testing"

	"ontology/api"
)

var allKeys = []string{"K1", "K2", "K3", "K4", "KX"}

type w struct {
	seq int64
	k   string
	v   int
}

func build(seed uint64, n int) (*api.Store, []w) {
	rng := rand.New(rand.NewPCG(seed, 1))
	s := api.New()
	ws := make([]w, 0, n)
	for i := 0; i < n; i++ {
		k, v := allKeys[rng.IntN(4)], int(rng.IntN(1000))
		seq, _ := s.Append(k, v)
		ws = append(ws, w{seq, k, v})
	}
	return s, ws
}
func batch(ws []w, hi int64, k string) (v int, ok bool) {
	for _, x := range ws {
		if x.seq < hi && x.k == k {
			v, ok = x.v, true
		}
	}
	return
}
func TestBatchReference(t *testing.T) {
	s, ws := build(1, 24)
	r := rand.New(rand.NewPCG(1, 7))
	lo := int64(1 + r.IntN(20))
	hi := lo + int64(1+r.IntN(int(25-lo)))
	s.Compact(lo, hi)
	for at := lo; at < hi; at++ {
		for _, k := range allKeys {
			gv, gok, _ := s.Read(at, k)
			rv, rok := batch(ws, hi, k)
			if gv != rv || gok != rok {
				t.Fatalf("at=%d k=%s", at, k)
			}
		}
	}
}
func TestOutsideUnchanged(t *testing.T) {
	s, _ := build(1, 20)
	lo, hi := int64(5), int64(15)
	rd := func(at int64, k string) [2]any { v, ok, _ := s.Read(at, k); return [2]any{v, ok} }
	before := map[[2]any][2]any{}
	for at := int64(1); at < lo; at++ {
		for _, k := range allKeys {
			before[[2]any{at, k}] = rd(at, k)
		}
	}
	s.Compact(lo, hi)
	for q, want := range before {
		if rd(q[0].(int64), q[1].(string)) != want {
			t.Fatalf("%v changed", q)
		}
	}
}
func TestSitesAddressable(t *testing.T) {
	s, ws := build(2, 30)
	for _, r := range [][2]int64{{2, 6}, {2, 6}, {6, 12}, {20, 25}} {
		s.Compact(r[0], r[1])
	}
	for at := int64(1); at <= int64(len(ws)); at++ {
		for _, k := range allKeys {
			if _, _, e := s.Read(at, k); e != nil {
				t.Fatalf("site=%d k=%s", at, k)
			}
		}
	}
}
func TestRejectedOpsNoTrace(t *testing.T) {
	if errors.Is(api.ErrEmptyKey, api.ErrSeqOutOfRange) || errors.Is(api.ErrEmptyKey, api.ErrBadRange) || errors.Is(api.ErrSeqOutOfRange, api.ErrBadRange) {
		t.Fatal("sentinel errors must be mutually distinct")
	}
	s := api.New()
	s.Append("K1", 1)
	snap := func() [2]any { v, ok, _ := s.Read(1, "K1"); return [2]any{v, ok} }
	before := snap()
	if _, e := s.Append("", 9); !errors.Is(e, api.ErrEmptyKey) {
		t.Fatal("empty key")
	}
	for _, q := range []int64{0, 2} {
		if _, _, e := s.Read(q, "K1"); !errors.Is(e, api.ErrSeqOutOfRange) {
			t.Fatalf("seq=%d", q)
		}
	}
	for _, r := range [][2]int64{{0, 2}, {1, 9}, {2, 2}} {
		if !errors.Is(s.Compact(r[0], r[1]), api.ErrBadRange) {
			t.Fatalf("range=%v", r)
		}
	}
	if snap() != before {
		t.Fatal("state changed after rejection")
	}
	if _, err := s.Append("K2", 2); err != nil {
		t.Fatal("still usable")
	}
}
func TestConcurrentReads(t *testing.T) {
	s := api.New()
	const R = int64(10)
	for i := int64(0); i < R; i++ {
		s.Append("K", int(i))
	}
	start := make(chan struct{})
	var stop atomic.Bool
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for !stop.Load() {
				for at := int64(1); at <= R; at++ {
					if v, ok, _ := s.Read(at, "K"); !ok || v != int(at-1) {
						t.Errorf("at=%d", at)
						stop.Store(true)
						return
					}
				}
			}
		}()
	}
	close(start)
	n := R // compacts start at lo>R, so sites 1..R stay unchanged
	for lo := R + 2; lo < 60; lo += 2 {
		for n < lo+2 {
			s.Append("K", int(n))
			n++
		}
		s.Compact(lo, lo+2)
	}
	stop.Store(true)
	wg.Wait()
}

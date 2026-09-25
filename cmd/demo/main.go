package main

import (
	"errors"
	"fmt"
	"math"
	"os"
	"sync"

	"ontology/api"
	"ontology/hll"
	"ontology/hsh"
)

var failed bool

func check(name string, ok bool) {
	if !ok {
		failed = true
		fmt.Println("FAIL", name)
		return
	}
	fmt.Println("OK  ", name)
}

func approx(a, b float64) bool { return math.Abs(a-b) < 5e-5 }

func fill(add func(string) error, pre string, n int) {
	for i := 0; i < n; i++ {
		add(fmt.Sprintf("%s-%d", pre, i))
	}
}

// eight-step table derived in NOTES.md (p=4, m=16, alpha=0.673)
var (
	keys   = "abcdefgh"
	hashes = []uint64{0x02c0bdbf481420f8, 0x3e35b21bfb9b6405, 0x7b8855250f3cde02, 0x57905f59af4e3d5d,
		0xafca0c33e25677df, 0xd8a688545ff41755, 0x73b5d188cc5fb094, 0xc90036f097882105}
	js   = []uint32{8, 5, 2, 13, 15, 5, 4, 5}
	rhos = []uint8{7, 3, 2, 2, 1, 1, 2, 1}
)

func main() {
	ok := true
	reg := make([]uint8, 16)
	sk, _ := hll.New(4)
	for i := 0; i < 8; i++ {
		h := hsh.Hash(string(keys[i]))
		j, rho := hsh.Bucket(h, 4)
		ok = ok && h == hashes[i] && j == js[i] && rho == rhos[i]
		if rho > reg[j] {
			reg[j] = rho
		}
		sk.Add(string(keys[i]))
	}
	check("eight keys: per-step hash/bucket/rank match NOTES table", ok)
	z, v := 0.0, 0
	for _, r := range reg {
		z += math.Ldexp(1, -int(r))
		if r == 0 {
			v++
		}
	}
	raw := 0.673 * 16 * 16 / z
	est, _ := sk.Estimate()
	check(fmt.Sprintf("eight keys: raw=%.4f, after LC=%.4f (want 15.1358/7.5201)", raw, est),
		approx(raw, 15.1358) && approx(est, 7.5201) && approx(16*math.Log(16/float64(v)), 7.5201))
	new8 := func() *hll.Sketch { s, _ := hll.New(8); return s }
	a, b, all, mg := new8(), new8(), new8(), new8()
	for i := 0; i < 5000; i++ {
		k := fmt.Sprintf("key-%d", i)
		all.Add(k)
		if i%2 == 0 {
			a.Add(k)
		} else {
			b.Add(k)
		}
	}
	mg.Merge(a)
	mg.Merge(b)
	check("merge equals sequential adds (bucket-wise)", mg.Equal(all))
	big, _ := hll.New(12)
	fill(big.Add, "bound", 20000)
	est, _ = big.Estimate()
	check(fmt.Sprintf("estimate within 3sigma (est=%.0f n=20000)", est),
		math.Abs(est-20000)/20000 <= 3*1.04/math.Sqrt(4096))
	huge, _ := hll.New(16)
	fill(huge.Add, "o1", 100000)
	huge.Estimate()
	check("Estimate reads 0 registers at m=65536", huge.EstimateIsO1())
	s1, _ := api.New(10)
	s2, _ := api.New(11)
	zero := &api.Sketch{}
	_, eP := api.New(3)
	eM := s1.Merge(s2)
	eZ := zero.Add("x")
	check("three distinguishable sentinel errors",
		errors.Is(eP, api.ErrPrecision) && errors.Is(eM, api.ErrMismatch) && errors.Is(eZ, api.ErrNotInit) &&
			!errors.Is(eP, api.ErrMismatch) && !errors.Is(eM, api.ErrNotInit) && !errors.Is(eZ, api.ErrPrecision))

	before, _ := s1.Estimate()
	s1.Merge(s2)
	after, _ := s1.Estimate()
	s1.Add("still-usable")
	later, _ := s1.Estimate()
	check("rejected ops leave state unchanged, sketch still usable", before == after && later >= before)

	sc, _ := api.New(10)
	check("SelfCheck passes", sc.SelfCheck() == nil)

	con, _ := api.New(12)
	const G, K = 8, 4000
	done := make(chan struct{})
	var adders, reader sync.WaitGroup
	mono := true
	reader.Add(1)
	go func() {
		defer reader.Done()
		prev := 0.0
		for {
			select {
			case <-done:
				return
			default:
				if e, _ := con.Estimate(); e < prev {
					mono = false
				} else {
					prev = e
				}
			}
		}
	}()
	for g := 0; g < G; g++ {
		adders.Add(1)
		go func() {
			defer adders.Done()
			fill(con.Add, "conc", K)
		}()
	}
	adders.Wait()
	close(done)
	reader.Wait()
	est, _ = con.Estimate()
	check(fmt.Sprintf("concurrent add: est=%.0f in 3sigma of %d, monotonic", est, K),
		mono && math.Abs(est-K)/K <= 3*1.04/math.Sqrt(4096))

	if failed {
		os.Exit(1)
	}
}

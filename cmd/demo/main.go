// Command demo exercises the weighted random sampler end to end.
package main

import (
	"errors"
	"fmt"
	"math"
	"os"
	"sync"

	"ontology/api"
	"ontology/wrs"
	"ontology/wsampler"
)

var failed bool

func check(name string, ok bool) {
	if !ok {
		failed = true
	}
	fmt.Printf("%s %s\n", map[bool]string{true: "OK  ", false: "FAIL"}[ok], name)
}

func rng(i int) float64 { return float64((i*37)%101+1) / 102.0 }

func vals(s []api.Item) string {
	out := ""
	for _, it := range s {
		out += it.Val
	}
	return out
}

// offline is the naive reference: collect all, key each, keep top-k.
func offline(items []api.Item, k int) string {
	type keyed struct {
		val string
		key float64
	}
	ks := make([]keyed, len(items))
	for i, it := range items {
		ks[i] = keyed{it.Val, wrs.Key(rng(i+1), it.Weight)}
	}
	for i := 0; i < len(ks); i++ {
		for j := i + 1; j < len(ks); j++ {
			if ks[j].key > ks[i].key {
				ks[i], ks[j] = ks[j], ks[i]
			}
		}
	}
	out := ""
	for i := 0; i < k && i < len(ks); i++ {
		out += ks[i].val
	}
	return out
}

func gen(n int) []api.Item {
	items := make([]api.Item, n)
	for i := range items {
		items[i] = api.Item{Val: fmt.Sprintf("v%05d", i), Weight: float64(i%7 + 1)}
	}
	return items
}

func main() {
	keys := []float64{wrs.Key(0.9, 1), wrs.Key(0.64, 2), wrs.Key(0.75, 1), wrs.Key(0.343, 3), wrs.Key(0.1, 1)}
	ok := math.Abs(keys[0]-0.9) < 1e-9 && math.Abs(keys[1]-0.8) < 1e-9 &&
		math.Abs(keys[2]-0.75) < 1e-9 && math.Abs(keys[3]-0.7) < 1e-9 && math.Abs(keys[4]-0.1) < 1e-9
	check("wrs keys 0.9 0.8 0.75 0.7 0.1", ok)

	us := []float64{0.9, 0.64, 0.75, 0.343, 0.1}
	steps := []wrs.Item{{Val: "A", Weight: 1}, {Val: "B", Weight: 2}, {Val: "C", Weight: 1}, {Val: "D", Weight: 3}, {Val: "E", Weight: 1}}
	ws, err := wsampler.New(2, func(i int) float64 { return us[i-1] })
	trace := ""
	for _, it := range steps {
		if err != nil || ws.Feed([]wrs.Item{it}) != nil {
			break
		}
		trace += "[" + vals(ws.Sample()) + "]"
	}
	check("trace "+trace, err == nil && trace == "[A][AB][AB][AB][AB]")

	small, _ := api.New(5, rng)
	_ = small.Feed(gen(3))
	check("size=min(k,N) & N<=k all kept", small.Size() == 3 && vals(small.Sample()) != "")

	big, _ := api.New(4, rng)
	items := gen(50)
	_ = big.Feed(items)
	check("size==k after N>k", big.Size() == 4)
	check("online == offline reference", vals(big.Sample()) == offline(items, 4))

	e1, e2 := error(nil), error(nil)
	_, e1 = api.New(0, rng)
	_, e2 = api.New(1, nil)
	e3 := big.Feed([]api.Item{{Val: "", Weight: 1}})
	e4 := big.Feed([]api.Item{{Val: "x", Weight: 0}})
	badU, _ := api.New(1, func(int) float64 { return 1.0 })
	e5 := badU.Feed([]api.Item{{Val: "x", Weight: 1}})
	errs := []error{e1, e2, e3, e4, e5}
	distinct := errs[0] != nil
	for i := range errs {
		distinct = distinct && errors.Is(errs[i], []error{api.ErrBadCapacity, api.ErrNilRNG, api.ErrEmptyVal, api.ErrBadWeight, api.ErrBadU}[i])
		for j := i + 1; j < len(errs); j++ {
			distinct = distinct && errs[i] != errs[j]
		}
	}
	check("four error classes distinct", distinct)

	before := vals(big.Sample())
	sz := big.Size()
	_ = big.Feed(append(gen(2), api.Item{Val: "bad", Weight: -1}))
	check("rejected batch leaves state untouched", vals(big.Sample()) == before && big.Size() == sz)

	huge, _ := api.New(10, rng)
	constOK := true
	for _, m := range []int{100, 1000, 10000} {
		h, _ := api.New(10, rng)
		_ = h.Feed(gen(m))
		constOK = constOK && h.Size() == 10
	}
	check("retained count == k for m up to 10000", constOK && huge != nil)

	full, _ := api.New(7, rng)
	_ = full.Feed(gen(200))
	want := vals(full.Sample())
	var wg sync.WaitGroup
	mismatch := make(chan bool, 16)
	for g := 0; g < 16; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if vals(full.Sample()) != want || full.Size() != 7 || api.SelfCheck() != nil {
				mismatch <- true
			}
		}()
	}
	wg.Wait()
	close(mismatch)
	_, badRead := <-mismatch
	check("concurrent readers identical", !badRead)
	check("SelfCheck", api.SelfCheck() == nil)

	if failed {
		os.Exit(1)
	}
}

// Command demo exercises the consistent hash ring end to end and prints
// one OK/FAIL verdict line per property, plus a final summary.
package main

import (
	"errors"
	"fmt"
	"math"

	"ontology"
)

const nKeys = 100000

var passed, total int

func check(ok bool, format string, args ...any) {
	total++
	mark := "FAIL"
	if ok {
		mark = "OK"
		passed++
	}
	fmt.Printf("%s %s\n", mark, fmt.Sprintf(format, args...))
}

func nodeIDs(n int) []string {
	ids := make([]string, n)
	for i := range ids {
		ids[i] = fmt.Sprintf("node-%d", i)
	}
	return ids
}

func build(ids []string, vn int) *ontology.Ring {
	r := ontology.New()
	for _, id := range ids {
		if err := r.Add(id, vn); err != nil {
			panic(err)
		}
	}
	return r
}

func owners(r *ontology.Ring, keys []string) []string {
	out := make([]string, len(keys))
	for i, k := range keys {
		o, err := r.Locate(k)
		if err != nil {
			panic(err)
		}
		out[i] = o
	}
	return out
}

func maxMinRatio(counts map[string]int, ids []string) float64 {
	hi, lo := 0, math.MaxInt
	for _, id := range ids {
		if c := counts[id]; c > hi {
			hi = c
		} else if c < lo {
			lo = c
		}
	}
	if lo == 0 {
		return math.Inf(1)
	}
	return float64(hi) / float64(lo)
}

func dist(ids []string, vn int) map[string]int {
	counts := map[string]int{}
	for _, o := range owners(build(ids, vn), ontology.Keys(nKeys)) {
		counts[o]++
	}
	return counts
}

func main() {
	keys := ontology.Keys(nKeys)
	ids := nodeIDs(10)

	r200 := maxMinRatio(dist(ids, 200), ids)
	check(r200 <= 2.0, "balance 10 nodes x 200 vnodes: max/min ratio %.2f (<= 2.0)", r200)

	r := build(ids, 200)
	before := owners(r, keys)
	if err := r.Add("node-10", 200); err != nil {
		panic(err)
	}
	after := owners(r, keys)
	moved := 0
	for i := range keys {
		if before[i] != after[i] {
			moved++
		}
	}
	ratio := float64(moved) / nKeys
	check(ratio >= 1.0/22 && ratio <= 3.0/22,
		"relocation after adding 11th node: %.4f (theory 1/11 = %.4f, bound [1/22, 3/22])", ratio, 1.0/11)

	if err := r.Remove("node-10"); err != nil {
		panic(err)
	}
	restored := owners(r, keys)
	unaffected := 0
	for i := range keys {
		if restored[i] == before[i] {
			unaffected++
		}
	}
	check(unaffected == nKeys, "removal restores owners: %d/%d keys back to original owner", unaffected, nKeys)

	victim := build(ids, 200)
	pre := owners(victim, keys)
	if err := victim.Remove("node-3"); err != nil {
		panic(err)
	}
	post := owners(victim, keys)
	kept := 0
	for i := range keys {
		if pre[i] != "node-3" && post[i] == pre[i] {
			kept++
		}
	}
	notOwned := 0
	for _, o := range pre {
		if o != "node-3" {
			notOwned++
		}
	}
	check(kept == notOwned, "removal of node-3: %d keys not owned by it, all %d unchanged", notOwned, kept)

	demoWrap()

	r1 := maxMinRatio(dist(ids, 1), ids)
	check(r1 > r200, "vnodes matter: max/min ratio vnodes=1 is %.2f vs vnodes=200 is %.2f", r1, r200)

	_, err := ontology.New().Locate("k")
	check(errors.Is(err, ontology.ErrEmptyRing), "empty ring Locate error: %v", err)

	dup := build(ids[:5], 100)
	preDup := owners(dup, keys[:5000])
	err = dup.Add(ids[2], 100)
	postDup := owners(dup, keys[:5000])
	unchanged := errors.Is(err, ontology.ErrNodeExists)
	for i := range preDup {
		unchanged = unchanged && preDup[i] == postDup[i]
	}
	check(unchanged, "duplicate add error: %v; ring unchanged for %d keys", err, len(preDup))

	fmt.Printf("SUMMARY: %d/%d checks OK\n", passed, total)
}

func demoWrap() {
	r := build([]string{"alpha", "beta"}, 1)
	pts := r.Points()
	lo, hi := pts[0], pts[1]
	var kb, km, ka string
	for i := 0; ; i++ {
		k := fmt.Sprintf("wrap-%d", i)
		h := ontology.HashKey(k)
		switch {
		case h < lo.Hash && kb == "":
			kb = k
		case h > lo.Hash && h < hi.Hash && km == "":
			km = k
		case h > hi.Hash && ka == "":
			ka = k
		}
		if kb != "" && km != "" && ka != "" {
			break
		}
	}
	ob, _ := r.Locate(kb)
	om, _ := r.Locate(km)
	oa, _ := r.Locate(ka)
	check(ob == lo.Node && om == hi.Node && oa == lo.Node,
		"wrap: below-min->%s between->%s past-max->%s (wraps to min)", ob, om, oa)
}

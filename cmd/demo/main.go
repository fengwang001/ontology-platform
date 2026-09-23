package main

import (
	"errors"
	"fmt"

	"ontology/hashfam"
	"ontology/sketch"
)

type check struct {
	name string
	ok   bool
}

func report(checks []check) bool {
	pass := true
	for _, c := range checks {
		tag := "OK"
		if !c.ok {
			tag, pass = "FAIL", false
		}
		fmt.Printf("%s  %s\n", tag, c.name)
	}
	return pass
}

func main() {
	var checks []check

	// hashfam: two independent constructions produce identical hashes.
	a, _ := hashfam.New(4)
	b, _ := hashfam.New(4)
	det := true
	for _, k := range []string{"alpha", "beta", "gamma"} {
		for i := range a {
			det = det && a[i](k) == b[i](k)
		}
	}
	checks = append(checks, check{"hash family deterministic", det})

	// sketch: insert keys; Estimate must never be below the true count.
	sk, _ := sketch.New(64, 4)
	keys := []string{"a", "b", "c", "a", "a", "b"}
	truth := map[string]int64{}
	for _, k := range keys {
		truth[k]++
		sk.Add(k, 1)
	}
	nounder := true
	for k, t := range truth {
		est, _ := sk.Estimate(k)
		nounder = nounder && est >= t
	}
	checks = append(checks, check{"estimate never underestimates", nounder})

	// determinism: two independent builds are cell-identical.
	build := func() *sketch.Sketch {
		s, _ := sketch.New(64, 4)
		for _, k := range keys {
			s.Add(k, 1)
		}
		return s
	}
	checks = append(checks, check{"independent builds identical",
		fmt.Sprint(build().Snapshot()) == fmt.Sprint(build().Snapshot())})

	// merge isomorphism: Merge(A,B) equals feeding the concatenated stream.
	a, _ := sketch.New(64, 4)
	b, _ := sketch.New(64, 4)
	c, _ := sketch.New(64, 4)
	for _, k := range keys[:3] {
		a.Add(k, 1)
		c.Add(k, 1)
	}
	for _, k := range keys[3:] {
		b.Add(k, 1)
		c.Add(k, 1)
	}
	a.Merge(b)
	checks = append(checks, check{"merge equals concatenated feed",
		fmt.Sprint(a.Snapshot()) == fmt.Sprint(c.Snapshot())})

	// cross-parameter merge is rejected; both sides stay unchanged.
	big, _ := sketch.New(128, 4)
	beforeA, beforeBig := fmt.Sprint(a.Snapshot()), fmt.Sprint(big.Snapshot())
	misErr := a.Merge(big)
	checks = append(checks, check{"cross-param merge rejected, sides unchanged",
		errors.Is(misErr, sketch.ErrMismatch) &&
			fmt.Sprint(a.Snapshot()) == beforeA && fmt.Sprint(big.Snapshot()) == beforeBig})

	// probe count is exactly depth, independent of width.
	probeOK := true
	for _, w := range []int{1000, 100000} {
		s, _ := sketch.New(w, 4)
		s.Add("k", 1)
		s.Estimate("k")
		probeOK = probeOK && s.CellCount() == 4
	}
	checks = append(checks, check{"probe count == d for w=1000,100000", probeOK})

	if !report(checks) {
		panic("demo failed")
	}
}

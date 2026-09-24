package main

import (
	"errors"
	"fmt"
	"math"

	"ontology/ring"
)

// demoWrap builds a two-vnode ring and locates keys hashing before the
// smallest position, between the two, and past the largest (wrap).
func demoWrap() {
	var r *ring.Ring
	var pts []ring.Point
	for i := 0; ; i++ {
		r = ring.New()
		a := fmt.Sprintf("wrap-a-%d", i)
		b := fmt.Sprintf("wrap-b-%d", i)
		if err := r.Add(a, 1); err != nil {
			check(fmt.Sprintf("wrap: %v", err), false)
			return
		}
		if err := r.Add(b, 1); err != nil {
			check(fmt.Sprintf("wrap: %v", err), false)
			return
		}
		pts = r.Points()
		if pts[0].Hash > 1<<40 && pts[1].Hash < math.MaxUint64-(1<<40) {
			break // positions comfortably interior, all three cases findable
		}
	}
	lo, hi := pts[0], pts[1]

	find := func(pred func(uint64) bool) string {
		for i := 0; ; i++ {
			k := fmt.Sprintf("wrap-key-%d", i)
			if pred(ring.HashKey(k)) {
				return k
			}
		}
	}
	cases := []struct {
		name string
		key  string
		want string
	}{
		{"before smallest", find(func(h uint64) bool { return h < lo.Hash }), lo.Node},
		{"between positions", find(func(h uint64) bool { return h > lo.Hash && h < hi.Hash }), hi.Node},
		{"past largest (wrap)", find(func(h uint64) bool { return h > hi.Hash }), lo.Node},
	}
	for _, c := range cases {
		got, err := r.Locate(c.key)
		check(fmt.Sprintf("wrap %s: key %q -> %s (want %s)", c.name, c.key, got, c.want),
			err == nil && got == c.want)
	}
}

// demoBalance contrasts the max/min key-share ratio of vnodes=1 with
// vnodes=200 over the same fixed key set.
func demoBalance(keys []string) {
	ratio := func(vn int) float64 {
		counts := owners(build(nodes, vn), keys)
		min, max := maxMin(counts)
		if min == 0 {
			return math.Inf(1)
		}
		return float64(max) / float64(min)
	}
	r1, r200 := ratio(1), ratio(vnodes)
	check(fmt.Sprintf("balance: max/min ratio vnodes=1 is %.2f, vnodes=%d is %.2f",
		r1, vnodes, r200), r1 > r200)
}

// demoErrors verifies the empty-ring error and that a duplicate Add
// fails with ErrNodeExists without disturbing any key's ownership.
func demoErrors(keys []string) {
	_, err := ring.New().Locate("k")
	check(fmt.Sprintf("empty ring: Locate error is ErrEmptyRing (%v)", err),
		errors.Is(err, ring.ErrEmptyRing))

	r := build(5, 100)
	sample := keys[:10000]
	before := make([]string, len(sample))
	for i, k := range sample {
		before[i], _ = r.Locate(k)
	}
	dupErr := r.Add(nodeID(2), 100)
	unchanged := true
	for i, k := range sample {
		now, _ := r.Locate(k)
		if now != before[i] {
			unchanged = false
		}
	}
	check(fmt.Sprintf("duplicate add: ErrNodeExists (%v), ring unchanged for %d keys",
		dupErr, len(sample)),
		errors.Is(dupErr, ring.ErrNodeExists) && unchanged)
}

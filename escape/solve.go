package escape

import "math/bits"

// bitset is a fixed-size set of object indices. Object count is bounded by
// 64 sites + 8 params + 64 call results = 136 < 192.
type bitset [3]uint64

func (b *bitset) add(i int) bool {
	w := uint(i) / 64
	m := uint64(1) << (uint(i) % 64)
	if b[w]&m != 0 {
		return false
	}
	b[w] |= m
	return true
}

func (b *bitset) union(o bitset) bool {
	changed := false
	for w := 0; w < 3; w++ {
		if n := b[w] | o[w]; n != b[w] {
			b[w] = n
			changed = true
		}
	}
	return changed
}

func (b bitset) has(i int) bool {
	return b[uint(i)/64]&(uint64(1)<<(uint(i)%64)) != 0
}

// forEach iterates over a copy, so callbacks may safely grow other sets.
func (b bitset) forEach(f func(int)) {
	for w := 0; w < 3; w++ {
		x := b[w]
		for x != 0 {
			t := bits.TrailingZeros64(x)
			f(w*64 + t)
			x &= x - 1
		}
	}
}

type analysis struct {
	summary Summary
	classes []Class
}

// analyze runs one round of the flow-insensitive points-to analysis and
// derives the classification and summary. sums[i] is the callee summary to
// use for statement i when it is a Call.
func analyze(k, v int, stmts []Stmt, sums []Summary) analysis {
	nSites := 0
	for _, s := range stmts {
		if s.Kind == New {
			nSites++
		}
	}
	// Object layout: [0,nSites) sites, [nSites,nSites+k) params, then X objects.
	xObj := make(map[int]int) // stmt index -> X object
	nObj := nSites + k
	for i, s := range stmts {
		if s.Kind == Call && s.D != -1 && sums[i].Fresh {
			xObj[i] = nObj
			nObj++
		}
	}

	pts := make([]bitset, v)
	heap := make([]bitset, nObj)
	var ret, seed bitset
	for i := 0; i < k; i++ {
		pts[i].add(nSites + i)
	}
	for _, x := range xObj {
		seed.add(x) // call-result objects are born globally marked
	}

	for changed := true; changed; {
		changed = false
		siteIdx := 0
		for i, s := range stmts {
			switch s.Kind {
			case New:
				if pts[s.D].add(siteIdx) {
					changed = true
				}
				siteIdx++
			case Copy:
				if pts[s.D].union(pts[s.S]) {
					changed = true
				}
			case Store:
				pts[s.D].forEach(func(o int) {
					if heap[o].union(pts[s.S]) {
						changed = true
					}
				})
			case Load:
				pts[s.S].forEach(func(o int) {
					if pts[s.D].union(heap[o]) {
						changed = true
					}
				})
			case Ret:
				if ret.union(pts[s.S]) {
					changed = true
				}
			case Global:
				pts[s.S].forEach(func(o int) {
					if seed.add(o) {
						changed = true
					}
				})
			case Call:
				sum := sums[i]
				for ai := range s.Args {
					if sum.Glob[ai] {
						pts[s.Args[ai]].forEach(func(o int) {
							if seed.add(o) {
								changed = true
							}
						})
					}
					if s.D != -1 && sum.Ret[ai] {
						if pts[s.D].union(pts[s.Args[ai]]) {
							changed = true
						}
					}
				}
				for ai := range s.Args {
					for aj := range s.Args {
						if !sum.E[ai][aj] {
							continue
						}
						pts[s.Args[ai]].forEach(func(o int) {
							if heap[o].union(pts[s.Args[aj]]) {
								changed = true
							}
						})
					}
				}
				if s.D != -1 && sum.Fresh {
					if pts[s.D].add(xObj[i]) {
						changed = true
					}
				}
			}
		}
	}

	glb := closure(seed, heap)
	rr := closure(ret, heap)
	var prSeed bitset
	for i := 0; i < k; i++ {
		prSeed.union(heap[nSites+i])
	}
	pr := closure(prSeed, heap)

	classes := make([]Class, nSites)
	for o := 0; o < nSites; o++ {
		switch {
		case glb.has(o):
			classes[o] = GlobalEscape
		case rr.has(o):
			classes[o] = ReturnEscape
		case pr.has(o):
			classes[o] = ParamEscape
		default:
			classes[o] = Stack
		}
	}

	sum := emptySummary(k)
	for i := 0; i < k; i++ {
		p := nSites + i
		sum.Ret[i] = rr.has(p)
		sum.Glob[i] = glb.has(p)
		for j := 0; j < k; j++ {
			sum.E[i][j] = heap[p].has(nSites + j)
		}
	}
	for o := 0; o < nSites; o++ {
		if ret.has(o) {
			sum.Fresh = true
		}
	}
	for _, x := range xObj {
		if ret.has(x) {
			sum.Fresh = true
		}
	}
	return analysis{summary: sum, classes: classes}
}

// closure returns seed plus everything reachable from it along heap edges.
func closure(seed bitset, heap []bitset) bitset {
	out := seed
	var wl []int
	seed.forEach(func(o int) { wl = append(wl, o) })
	for len(wl) > 0 {
		o := wl[len(wl)-1]
		wl = wl[:len(wl)-1]
		heap[o].forEach(func(n int) {
			if !out.has(n) {
				out.add(n)
				wl = append(wl, n)
			}
		})
	}
	return out
}

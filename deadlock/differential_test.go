package deadlock

import (
	"fmt"
	"math/rand"
	"sort"
	"testing"
)

// naiveState is an independent reference model. Deadlock is decided by
// enumerating ALL completion orders of blocked processes (DFS over
// permutations): a blocked process can be placed next only if one of its
// alternatives fits Work, which starts at avail plus every non-blocked
// process's holdings and gains the net addition alloc[p] when p finishes.
type naiveState struct {
	r, p, l int
	total   []int64
	cost    []int64
	avail   []int64
	alloc   [][]int64
	rb      []int
	alive   []bool
	blocked []bool
	alts    [][][]int64
	bseq    []int
	next    int
}

func newNaive(rng *rand.Rand) *naiveState {
	R := 1 + rng.Intn(3)
	P := 2 + rng.Intn(4)
	L := 1 + rng.Intn(3)
	n := &naiveState{
		r: R, p: P, l: L,
		total:   make([]int64, R),
		cost:    make([]int64, R),
		avail:   make([]int64, R),
		alloc:   make([][]int64, P),
		rb:      make([]int, P),
		alive:   make([]bool, P),
		blocked: make([]bool, P),
		alts:    make([][][]int64, P),
		bseq:    make([]int, P),
	}
	for i := range n.total {
		n.total[i] = int64(1 + rng.Intn(4))
		n.avail[i] = n.total[i]
		n.cost[i] = int64(1 + rng.Intn(5))
	}
	for p := range n.alloc {
		n.alloc[p] = make([]int64, R)
		n.alive[p] = true
	}
	return n
}

func nfits(vec, w []int64) bool {
	for r, v := range vec {
		if v > w[r] {
			return false
		}
	}
	return true
}

func (n *naiveState) validVec(v []int64) bool {
	if len(v) != n.r {
		return false
	}
	zero := true
	for _, x := range v {
		if x < 0 {
			return false
		}
		if x != 0 {
			zero = false
		}
	}
	return !zero
}

func (n *naiveState) request(p int, alts [][]int64) (int, bool, error) {
	if len(alts) < 1 || len(alts) > 3 {
		return 0, false, ErrInvalidArgs
	}
	for _, a := range alts {
		if !n.validVec(a) {
			return 0, false, ErrInvalidArgs
		}
	}
	if p < 0 || p >= n.p || !n.alive[p] {
		return 0, false, ErrProcessNotFound
	}
	if n.blocked[p] {
		return 0, false, ErrProcessBlocked
	}
	feasible := false
	for _, a := range alts {
		ok := true
		for r, v := range a {
			if n.alloc[p][r]+v > n.total[r] {
				ok = false
			}
		}
		if ok {
			feasible = true
		}
	}
	if !feasible {
		return 0, false, ErrRequestImpossible
	}
	for i, a := range alts {
		if nfits(a, n.avail) {
			for r, v := range a {
				n.avail[r] -= v
				n.alloc[p][r] += v
			}
			return i, true, nil
		}
	}
	n.next++
	n.blocked[p] = true
	n.alts[p] = alts
	n.bseq[p] = n.next
	return 0, false, nil
}

func (n *naiveState) fixpoint() []GrantItem {
	var out []GrantItem
	for {
		best, idx, seq := -1, 0, 0
		for p := 0; p < n.p; p++ {
			if !n.blocked[p] {
				continue
			}
			for ai, a := range n.alts[p] {
				if nfits(a, n.avail) {
					if best == -1 || n.bseq[p] < seq {
						best, idx, seq = p, ai, n.bseq[p]
					}
					break
				}
			}
		}
		if best == -1 {
			return out
		}
		for r, v := range n.alts[best][idx] {
			n.avail[r] -= v
			n.alloc[best][r] += v
		}
		n.blocked[best] = false
		n.alts[best] = nil
		out = append(out, GrantItem{best, idx})
	}
}

func (n *naiveState) release(p int, vec []int64) ([]GrantItem, error) {
	if !n.validVec(vec) {
		return nil, ErrInvalidArgs
	}
	if p < 0 || p >= n.p || !n.alive[p] {
		return nil, ErrProcessNotFound
	}
	if n.blocked[p] {
		return nil, ErrProcessBlocked
	}
	for r, v := range vec {
		if v > n.alloc[p][r] {
			return nil, ErrReleaseExceedsAlloc
		}
	}
	for r, v := range vec {
		n.alloc[p][r] -= v
		n.avail[r] += v
	}
	return n.fixpoint(), nil
}

// reachable enumerates every feasible completion PREFIX by DFS over subsets
// of blocked processes (i.e. all possible completion orders, partial and
// full). Every process that appears in at least one such prefix is marked
// reachable and is therefore not deadlocked; the complement is the deadlock
// set. This is the naive, order-enumerating reference for graph reduction.
func (n *naiveState) reachable(work []int64, used []bool, mark []bool) {
	for p := 0; p < n.p; p++ {
		if !n.blocked[p] || used[p] {
			continue
		}
		for _, a := range n.alts[p] {
			if !nfits(a, work) {
				continue
			}
			mark[p] = true
			w := append([]int64(nil), work...)
			for r, v := range n.alloc[p] {
				w[r] += v
			}
			u := append([]bool(nil), used...)
			u[p] = true
			n.reachable(w, u, mark)
			break
		}
	}
}

func (n *naiveState) detect() []int {
	work := append([]int64(nil), n.avail...)
	for p := 0; p < n.p; p++ {
		if n.alive[p] && !n.blocked[p] {
			for r, v := range n.alloc[p] {
				work[r] += v
			}
		}
	}
	var blocked []int
	for p := 0; p < n.p; p++ {
		if n.blocked[p] {
			blocked = append(blocked, p)
		}
	}
	finishable := map[int]bool{}
	mark := make([]bool, n.p)
	n.reachable(work, make([]bool, n.p), mark)
	for _, p := range blocked {
		finishable[p] = mark[p]
	}
	var dead []int
	for _, p := range blocked {
		if !finishable[p] {
			dead = append(dead, p)
		}
	}
	sort.Ints(dead)
	return dead
}

func (n *naiveState) resolve() []ResolveStep {
	var steps []ResolveStep
	for {
		dead := n.detect()
		if len(dead) == 0 {
			return steps
		}
		victim, best := -1, int64(0)
		for _, p := range dead {
			holds := false
			for _, v := range n.alloc[p] {
				if v > 0 {
					holds = true
				}
			}
			if !holds {
				continue
			}
			var c int64
			for r, v := range n.alloc[p] {
				c += v * n.cost[r]
			}
			c *= int64(1 + n.rb[p])
			if victim == -1 || c < best || (c == best && p < victim) {
				victim, best = p, c
			}
		}
		for r, v := range n.alloc[victim] {
			n.avail[r] += v
			n.alloc[victim][r] = 0
		}
		n.blocked[victim] = false
		n.alts[victim] = nil
		n.rb[victim]++
		perm := false
		if n.rb[victim] >= n.l {
			n.alive[victim] = false
			perm = true
		}
		steps = append(steps, ResolveStep{victim, best, n.fixpoint(), perm})
	}
}

func joinLog(log []string) string {
	out := ""
	for _, l := range log {
		out += "\n" + l
	}
	return out
}

func errKind(e error) string {
	if e == nil {
		return ""
	}
	return e.Error()
}

func randomVec(rng *rand.Rand, R int, max int64) []int64 {
	v := make([]int64, R)
	for i := range v {
		v[i] = rng.Int63n(max + 1)
	}
	return v
}

func randomAlts(rng *rand.Rand, R int) [][]int64 {
	k := 1 + rng.Intn(3)
	alts := make([][]int64, k)
	for i := range alts {
		for {
			v := randomVec(rng, R, 4)
			for _, x := range v {
				if x != 0 {
					alts[i] = v
					goto done
				}
			}
		}
	done:
	}
	return alts
}

// TestRandomDifferential replays 2000 random operation sequences against both
// the implementation and the permutation-enumerating oracle, comparing every
// grant list, rejection, deadlock set and resolution step.
func TestRandomDifferential(t *testing.T) {
	const runs, opCount = 2000, 14
	for iter := 0; iter < runs; iter++ {
		rng := rand.New(rand.NewSource(int64(iter) + 1))
		n := newNaive(rng)
		d, err := New(n.r, n.total, n.cost, n.p, n.l)
		if err != nil {
			t.Fatalf("iter %d New: %v", iter, err)
		}
		log := []string{fmt.Sprintf("iter=%d R=%d T=%v c=%v P=%d L=%d",
			iter, n.r, n.total, n.cost, n.p, n.l)}
		t.Logf("basis iter=%d: %s", iter, log[0])

		cmpDetect := func(where string) {
			t.Helper()
			got, want := d.Detect(), n.detect()
			if fmt.Sprint(got) != fmt.Sprint(want) {
				t.Fatalf("iter %d %s Detect mismatch: impl=%v naive(permutation)=%v%s",
					iter, where, got, want, joinLog(log))
			}
			log = append(log, fmt.Sprintf("  [%s] Detect -> %v (naive: permutation oracle)", where, got))
		}

		for step := 0; step < opCount; step++ {
			p := rng.Intn(n.p + 1) // occasionally out of range on purpose
			logStart := len(log)
			switch rng.Intn(4) {
			case 0, 1:
				alts := randomAlts(rng, n.r)
				if rng.Intn(6) == 0 {
					alts = [][]int64{{}} // deliberately invalid
				}
				log = append(log, fmt.Sprintf("Request(%d, %v)", p, alts))
				g, e1 := d.Request(p, alts)
				idx, granted, e2 := n.request(p, alts)
				if errKind(e1) != errKind(e2) {
					t.Fatalf("iter %d Request err impl=%v naive=%v%s", iter, e1, e2, joinLog(log))
				}
				if e1 == nil {
					implBlocked := d.blocked[p]
					if implBlocked == granted || (!granted && !implBlocked) ||
						(granted && !implBlocked && g.AltIndex != idx) {
						t.Fatalf("iter %d Request result impl=%+v naive(idx=%d,granted=%v)%s",
							iter, g, idx, granted, joinLog(log))
					}
					word := "Blocked"
					if granted {
						word = "Granted"
					}
					log = append(log, fmt.Sprintf("  -> %s %d", word, idx))
				} else {
					log = append(log, "  -> reject: "+e1.Error())
				}
			case 2:
				vec := randomVec(rng, n.r, 3)
				if rng.Intn(6) == 0 {
					vec = []int64{} // deliberately invalid
				}
				log = append(log, fmt.Sprintf("Release(%d, %v)", p, vec))
				g1, e1 := d.Release(p, vec)
				g2, e2 := n.release(p, vec)
				if errKind(e1) != errKind(e2) {
					t.Fatalf("iter %d Release err impl=%v naive=%v%s", iter, e1, e2, joinLog(log))
				}
				if e1 == nil && fmt.Sprint(g1) != fmt.Sprint(g2) {
					t.Fatalf("iter %d Release grants impl=%v naive=%v%s", iter, g1, g2, joinLog(log))
				}
				log = append(log, fmt.Sprintf("  -> grants=%v err=%v", g1, e1))
			case 3:
				log = append(log, "Resolve()")
				s1, s2 := d.Resolve(), n.resolve()
				if fmt.Sprint(s1) != fmt.Sprint(s2) {
					t.Fatalf("iter %d Resolve mismatch:\n impl=%+v\n naive=%+v%s",
						iter, s1, s2, joinLog(log))
				}
				log = append(log, fmt.Sprintf("  -> %+v", s1))
				cmpDetect("post-resolve")
			}
			if step%3 == 0 {
				cmpDetect("probe")
			}
			for _, l := range log[logStart:] {
				t.Log(l)
			}
			// Resource conservation, blocked-means-unfit and survivor rb.
			sum := append([]int64(nil), d.avail...)
			for q := 0; q < d.pnum; q++ {
				if !d.alive[q] {
					continue
				}
				for r, v := range d.alloc[q] {
					sum[r] += v
				}
				if d.rb[q] >= d.rollback {
					t.Fatalf("iter %d alive q%d rb=%d >= L%s", iter, q, d.rb[q], joinLog(log))
				}
			}
			for r, v := range sum {
				if v != d.total[r] {
					t.Fatalf("iter %d conservation broken r%d: %d!=%d%s",
						iter, r, v, d.total[r], joinLog(log))
				}
			}
			for q := 0; q < d.pnum; q++ {
				if !d.blocked[q] {
					continue
				}
				for _, a := range d.alts[q] {
					if nfits(a, d.avail) {
						t.Fatalf("iter %d blocked q%d has fitting alt %v, avail %v%s",
							iter, q, a, d.avail, joinLog(log))
					}
				}
			}
			if d.nextBSeq != n.next {
				t.Fatalf("iter %d bseq counter impl=%d naive=%d%s",
					iter, d.nextBSeq, n.next, joinLog(log))
			}
		}
	}
}

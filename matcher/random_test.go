package matcher

import (
	"fmt"
	"math/rand"
	"reflect"
	"sort"
	"strings"
	"testing"
)

type instance struct {
	appPrefs  map[int][]int
	progCap   map[int]int
	progPrefs map[int][]int
}

func (inst *instance) mutual(a, p int) bool {
	return containsInt(inst.appPrefs[a], p) && containsInt(inst.progPrefs[p], a)
}

func (inst *instance) applicantIDs() []int {
	ids := make([]int, 0, len(inst.appPrefs))
	for id := range inst.appPrefs {
		ids = append(ids, id)
	}
	sort.Ints(ids)
	return ids
}

func (inst *instance) programIDs() []int {
	ids := make([]int, 0, len(inst.progCap))
	for id := range inst.progCap {
		ids = append(ids, id)
	}
	sort.Ints(ids)
	return ids
}

func (inst *instance) String() string {
	var b strings.Builder
	b.WriteString("applicants={")
	for i, a := range inst.applicantIDs() {
		if i > 0 {
			b.WriteString(" ")
		}
		fmt.Fprintf(&b, "%d:%v", a, inst.appPrefs[a])
	}
	b.WriteString("} programs={")
	for i, p := range inst.programIDs() {
		if i > 0 {
			b.WriteString(" ")
		}
		fmt.Fprintf(&b, "%d:(cap=%d,prefs=%v)", p, inst.progCap[p], inst.progPrefs[p])
	}
	b.WriteString("}")
	return b.String()
}

// validAssign reports whether assign respects mutual acceptability and
// program capacities.
func (inst *instance) validAssign(assign map[int]int) bool {
	counts := map[int]int{}
	for a, p := range assign {
		if p == 0 {
			continue
		}
		if !inst.mutual(a, p) {
			return false
		}
		counts[p]++
		if counts[p] > inst.progCap[p] {
			return false
		}
	}
	return true
}

// blocking lists the blocking pairs of assign, implemented independently of
// Matcher.Verify.
func (inst *instance) blocking(assign map[int]int) []Pair {
	counts := map[int]int{}
	holders := map[int][]int{}
	for a, p := range assign {
		if p == 0 {
			continue
		}
		counts[p]++
		holders[p] = append(holders[p], a)
	}
	var pairs []Pair
	for _, a := range inst.applicantIDs() {
		arank := rankMap(inst.appPrefs[a])
		for _, p := range inst.programIDs() {
			if !inst.mutual(a, p) {
				continue
			}
			cur := assign[a]
			if cur == p {
				continue
			}
			if cur != 0 && arank[cur] < arank[p] {
				continue
			}
			if counts[p] >= inst.progCap[p] {
				prank := rankMap(inst.progPrefs[p])
				worst, worstRank := -1, -1
				for _, b := range holders[p] {
					if r := prank[b]; worst == -1 || r > worstRank {
						worst, worstRank = b, r
					}
				}
				if worst == -1 || prank[a] > worstRank {
					continue
				}
			}
			pairs = append(pairs, Pair{Applicant: a, Program: p})
		}
	}
	return pairs
}

// stableMatchings enumerates every valid assignment and keeps exactly the
// stable ones.
func (inst *instance) stableMatchings() []map[int]int {
	apps := inst.applicantIDs()
	choices := make([][]int, len(apps))
	for i, a := range apps {
		opts := []int{0}
		for _, p := range inst.appPrefs[a] {
			if inst.mutual(a, p) {
				opts = append(opts, p)
			}
		}
		choices[i] = opts
	}
	var stable []map[int]int
	assign := make(map[int]int, len(apps))
	counts := map[int]int{}
	var rec func(i int)
	rec = func(i int) {
		if i == len(apps) {
			if len(inst.blocking(assign)) == 0 {
				cp := make(map[int]int, len(assign))
				for k, v := range assign {
					cp[k] = v
				}
				stable = append(stable, cp)
			}
			return
		}
		a := apps[i]
		for _, p := range choices[i] {
			if p != 0 && counts[p] >= inst.progCap[p] {
				continue
			}
			assign[a] = p
			counts[p]++
			rec(i + 1)
			counts[p]--
		}
	}
	rec(0)
	return stable
}

func rankMap(prefs []int) map[int]int {
	rank := make(map[int]int, len(prefs))
	for i, id := range prefs {
		rank[id] = i
	}
	return rank
}

func indexOf(list []int, v int) int {
	for i, x := range list {
		if x == v {
			return i
		}
	}
	return -1
}

func randomSubset(rng *rand.Rand, n int) []int {
	var ids []int
	for i := 1; i <= n; i++ {
		if rng.Float64() < 0.6 {
			ids = append(ids, i)
		}
	}
	rng.Shuffle(len(ids), func(i, j int) { ids[i], ids[j] = ids[j], ids[i] })
	return ids
}

func randomInstance(rng *rand.Rand) *instance {
	nA := 1 + rng.Intn(5)
	nP := 1 + rng.Intn(5)
	inst := &instance{
		appPrefs:  make(map[int][]int, nA),
		progCap:   make(map[int]int, nP),
		progPrefs: make(map[int][]int, nP),
	}
	for a := 1; a <= nA; a++ {
		inst.appPrefs[a] = randomSubset(rng, nP)
	}
	for p := 1; p <= nP; p++ {
		inst.progCap[p] = 1 + rng.Intn(3)
		inst.progPrefs[p] = randomSubset(rng, nA)
	}
	return inst
}

func registerShuffled(t *testing.T, rng *rand.Rand, inst *instance) *Matcher {
	t.Helper()
	m := New()
	type reg struct {
		isProgram bool
		id        int
	}
	var regs []reg
	for _, a := range inst.applicantIDs() {
		regs = append(regs, reg{false, a})
	}
	for _, p := range inst.programIDs() {
		regs = append(regs, reg{true, p})
	}
	rng.Shuffle(len(regs), func(i, j int) { regs[i], regs[j] = regs[j], regs[i] })
	for _, r := range regs {
		var err error
		if r.isProgram {
			err = m.AddProgram(r.id, inst.progCap[r.id], inst.progPrefs[r.id])
		} else {
			err = m.AddApplicant(r.id, inst.appPrefs[r.id])
		}
		if err != nil {
			t.Fatalf("registration: %v", err)
		}
	}
	return m
}

// For 2000 random instances with at most 5 applicants and 5 programs,
// enumerate every stable matching by brute force and check that Run returns
// a stable matching in which every applicant gets the best program they can
// obtain in any stable matching.
func TestRandomizedAgainstBruteForce(t *testing.T) {
	rng := rand.New(rand.NewSource(20261002))
	for iter := 0; iter < 2000; iter++ {
		inst := randomInstance(rng)

		m1 := registerShuffled(t, rng, inst)
		result, proposals, err := m1.Run()
		if err != nil {
			t.Fatalf("iter %d: Run: %v; input %s", iter, err, inst)
		}

		m2 := registerShuffled(t, rng, inst)
		result2, proposals2, err := m2.Run()
		if err != nil {
			t.Fatalf("iter %d: second Run: %v; input %s", iter, err, inst)
		}
		if proposals2 != proposals || !reflect.DeepEqual(result2, result) {
			t.Fatalf("iter %d: shuffled registration changed result: %v/%d vs %v/%d; input %s",
				iter, result2, proposals2, result, proposals, inst)
		}

		if !inst.validAssign(result) {
			t.Fatalf("iter %d: result %v violates acceptability or capacity; input %s", iter, result, inst)
		}

		pairs, err := m1.Verify(result)
		if err != nil {
			t.Fatalf("iter %d: Verify(result): %v; input %s", iter, err, inst)
		}
		if len(pairs) != 0 {
			t.Fatalf("iter %d: Verify(result) = %v, want no blocking pairs; input %s", iter, pairs, inst)
		}

		wantProposals := 0
		for _, a := range inst.applicantIDs() {
			if p := result[a]; p != 0 {
				wantProposals += indexOf(inst.appPrefs[a], p) + 1
			} else {
				wantProposals += len(inst.appPrefs[a])
			}
		}
		if proposals != wantProposals {
			t.Fatalf("iter %d: proposals = %d, want %d; input %s result %v",
				iter, proposals, wantProposals, inst, result)
		}

		stable := inst.stableMatchings()
		found := false
		for _, s := range stable {
			if reflect.DeepEqual(s, result) {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("iter %d: result %v is not a stable matching; input %s", iter, result, inst)
		}

		for _, a := range inst.applicantIDs() {
			best, bestRank := 0, len(inst.appPrefs[a])+1
			for _, s := range stable {
				p := s[a]
				r := len(inst.appPrefs[a]) + 1
				if p != 0 {
					r = indexOf(inst.appPrefs[a], p)
				}
				if r < bestRank {
					best, bestRank = p, r
				}
			}
			if result[a] != best {
				t.Fatalf("iter %d: applicant %d got %d, but best stable program is %d; input %s",
					iter, a, result[a], best, inst)
			}
		}

		t.Logf("iter %d input=%s output={result:%v proposals:%d} verdict={stableMatchings:%d resultStable:true applicantOptimal:true}",
			iter, inst, result, proposals, len(stable))
	}
}

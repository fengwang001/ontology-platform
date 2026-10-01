package matching

import (
	"fmt"
	"maps"
	"math/rand"
	"slices"
	"testing"
)

// instance is a randomly generated matching problem.
type instance struct {
	appPrefs  map[int][]int
	progPrefs map[int][]int
	caps      map[int]int
}

func (inst instance) String() string {
	s := "applicants:"
	for _, a := range slices.Sorted(maps.Keys(inst.appPrefs)) {
		s += fmt.Sprintf(" %d->%v", a, inst.appPrefs[a])
	}
	s += " programs:"
	for _, p := range slices.Sorted(maps.Keys(inst.progPrefs)) {
		s += fmt.Sprintf(" %d(cap %d)->%v", p, inst.caps[p], inst.progPrefs[p])
	}
	return s
}

func (inst instance) mutuallyAcceptable(a, p int) bool {
	return slices.Contains(inst.appPrefs[a], p) && slices.Contains(inst.progPrefs[p], a)
}

// rankOf returns the 0-based position of p in prefs, or len(prefs) when
// absent (used as the "unmatched" rank, worse than any listed program).
func rankOf(prefs []int, p int) int {
	if i := slices.Index(prefs, p); i >= 0 {
		return i
	}
	return len(prefs)
}

// isStable is an independent stability check used to validate Run: every
// pair mutually acceptable, capacities respected, and no blocking pair.
func isStable(inst instance, match map[int]int) bool {
	counts := map[int]int{}
	for a, p := range match {
		if p == 0 {
			continue
		}
		if !inst.mutuallyAcceptable(a, p) {
			return false
		}
		counts[p]++
		if counts[p] > inst.caps[p] {
			return false
		}
	}
	for _, a := range slices.Sorted(maps.Keys(inst.appPrefs)) {
		current := match[a]
		for _, p := range slices.Sorted(maps.Keys(inst.progPrefs)) {
			if !inst.mutuallyAcceptable(a, p) {
				continue
			}
			if current != 0 && rankOf(inst.appPrefs[a], p) >= rankOf(inst.appPrefs[a], current) {
				continue
			}
			if counts[p] < inst.caps[p] {
				return false // p has a free slot
			}
			worst := -1
			for b, q := range match {
				if q != p {
					continue
				}
				if worst == -1 || rankOf(inst.progPrefs[p], b) > rankOf(inst.progPrefs[p], worst) {
					worst = b
				}
			}
			if worst != -1 && rankOf(inst.progPrefs[p], a) < rankOf(inst.progPrefs[p], worst) {
				return false // p prefers a over its worst holder
			}
		}
	}
	return true
}

// bruteForceStable enumerates every stable matching of inst by trying,
// for each applicant, every mutually acceptable program plus "unmatched",
// pruning branches that already exceed a program's capacity.
func bruteForceStable(inst instance) []map[int]int {
	apps := slices.Sorted(maps.Keys(inst.appPrefs))
	counts := make(map[int]int, len(inst.progPrefs))
	current := make(map[int]int, len(apps))
	var stable []map[int]int
	var rec func(i int)
	rec = func(i int) {
		if i == len(apps) {
			if isStable(inst, current) {
				stable = append(stable, maps.Clone(current))
			}
			return
		}
		a := apps[i]
		current[a] = 0
		rec(i + 1)
		for _, p := range inst.appPrefs[a] {
			if !inst.mutuallyAcceptable(a, p) {
				continue
			}
			if counts[p] >= inst.caps[p] {
				continue
			}
			current[a] = p
			counts[p]++
			rec(i + 1)
			counts[p]--
		}
		current[a] = 0
	}
	rec(0)
	return stable
}

func randomInstance(rng *rand.Rand) instance {
	nA := 1 + rng.Intn(5)
	nP := 1 + rng.Intn(5)
	inst := instance{
		appPrefs:  make(map[int][]int, nA),
		progPrefs: make(map[int][]int, nP),
		caps:      make(map[int]int, nP),
	}
	shuffledSubset := func(n int) []int {
		ids := make([]int, 0, n)
		for id := 1; id <= n; id++ {
			if rng.Float64() < 0.7 {
				ids = append(ids, id)
			}
		}
		rng.Shuffle(len(ids), func(i, j int) { ids[i], ids[j] = ids[j], ids[i] })
		return ids
	}
	for a := 1; a <= nA; a++ {
		inst.appPrefs[a] = shuffledSubset(nP)
	}
	for p := 1; p <= nP; p++ {
		inst.progPrefs[p] = shuffledSubset(nA)
		inst.caps[p] = 1 + rng.Intn(3)
	}
	return inst
}

// For 2000 random instances with at most 5 applicants and 5 programs,
// Run must produce a stable matching in which every applicant gets the
// program they like most among all stable matchings (brute-forced).
func TestRandomApplicantOptimal(t *testing.T) {
	rng := rand.New(rand.NewSource(20261001))
	for i := 0; i < 2000; i++ {
		inst := randomInstance(rng)
		m := New()
		for _, a := range slices.Sorted(maps.Keys(inst.appPrefs)) {
			if err := m.AddApplicant(a, inst.appPrefs[a]); err != nil {
				t.Fatalf("case %d: AddApplicant(%d): %v", i, a, err)
			}
		}
		for _, p := range slices.Sorted(maps.Keys(inst.progPrefs)) {
			if err := m.AddProgram(p, inst.caps[p], inst.progPrefs[p]); err != nil {
				t.Fatalf("case %d: AddProgram(%d): %v", i, p, err)
			}
		}
		if err := m.Run(); err != nil {
			t.Fatalf("case %d: Run: %v", i, err)
		}
		res, proposals, err := m.Result()
		if err != nil {
			t.Fatalf("case %d: Result: %v", i, err)
		}

		stable := bruteForceStable(inst)
		if len(stable) == 0 {
			t.Fatalf("case %d: brute force found no stable matching", i)
		}
		// Per applicant, the best rank achievable in any stable matching
		// (rank len(prefs) means unmatched).
		best := map[int]int{}
		for a := range inst.appPrefs {
			best[a] = len(inst.appPrefs[a]) + 1
		}
		for _, s := range stable {
			for a := range inst.appPrefs {
				if r := rankOf(inst.appPrefs[a], s[a]); r < best[a] {
					best[a] = r
				}
			}
		}

		ok := isStable(inst, res)
		optimal := true
		for a := range inst.appPrefs {
			if rankOf(inst.appPrefs[a], res[a]) != best[a] {
				optimal = false
			}
		}
		t.Logf("case %d\n  input: %s\n  output: matching=%v proposals=%d\n  judgment: stable=%v applicant-optimal=%v (%d stable matchings brute-forced, best ranks=%v)",
			i, inst, res, proposals, ok, optimal, len(stable), best)

		if !ok {
			t.Errorf("case %d: Run result %v is not stable for %s", i, res, inst)
		}
		if !optimal {
			t.Errorf("case %d: Run result %v is not applicant-optimal (best ranks %v) for %s", i, res, best, inst)
		}
		checkInvariants(t, m, res, proposals)
	}
}

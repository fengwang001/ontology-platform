package mergebase

import (
	"errors"
	"fmt"
	"math/rand"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
)

// mustCommit registers a commit and fails the test on error.
func mustCommit(t *testing.T, s *Store, id string, parents ...string) {
	t.Helper()
	if err := s.Commit(id, parents); err != nil {
		t.Fatalf("Commit(%q, %v): %v", id, parents, err)
	}
}

func mustMergeBases(t *testing.T, s *Store, a, b string) []string {
	t.Helper()
	got, err := s.MergeBases(a, b)
	if err != nil {
		t.Fatalf("MergeBases(%q, %q): %v", a, b, err)
	}
	return got
}

// allAncestors returns the full ancestor set of id (including id itself),
// deliberately walking the whole graph with no generation pruning.
func allAncestors(s *Store, id string) map[string]bool {
	seen := map[string]bool{}
	stack := []string{id}
	for len(stack) > 0 {
		cur := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if seen[cur] {
			continue
		}
		seen[cur] = true
		stack = append(stack, s.commits[cur].parents...)
	}
	return seen
}

// naiveMergeBases computes merge bases by definition: intersect the two full
// ancestor sets, then keep the maximal elements. It is intentionally
// independent of the generation-pruned implementation under test.
func naiveMergeBases(s *Store, a, b string) []string {
	ancA := allAncestors(s, a)
	ancB := allAncestors(s, b)
	ca := map[string]bool{}
	for id := range ancA {
		if ancB[id] {
			ca[id] = true
		}
	}
	var bases []string
	for c := range ca {
		maximal := true
		for d := range ca {
			if d != c && allAncestors(s, d)[c] {
				maximal = false
				break
			}
		}
		if maximal {
			bases = append(bases, c)
		}
	}
	sort.Strings(bases)
	return bases
}

func TestCrissCrossMerge(t *testing.T) {
	s := NewStore()
	mustCommit(t, s, "R")
	mustCommit(t, s, "X", "R")
	mustCommit(t, s, "Y", "R")
	mustCommit(t, s, "M1", "X", "Y")
	mustCommit(t, s, "M2", "Y", "X") // parent order must not matter

	got := mustMergeBases(t, s, "M1", "M2")
	want := []string{"X", "Y"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("MergeBases(M1, M2) = %v, want exactly %v (no R)", got, want)
	}
	// Symmetric query and swapped parent order give the same result.
	if got2 := mustMergeBases(t, s, "M2", "M1"); !reflect.DeepEqual(got2, want) {
		t.Fatalf("MergeBases(M2, M1) = %v, want %v", got2, want)
	}
}

func TestAncestorIsMergeBase(t *testing.T) {
	s := NewStore()
	mustCommit(t, s, "A")
	mustCommit(t, s, "B", "A")
	mustCommit(t, s, "C", "B")

	if got := mustMergeBases(t, s, "A", "C"); !reflect.DeepEqual(got, []string{"A"}) {
		t.Fatalf("MergeBases(A, C) = %v, want [A]", got)
	}
	if got := mustMergeBases(t, s, "C", "A"); !reflect.DeepEqual(got, []string{"A"}) {
		t.Fatalf("MergeBases(C, A) = %v, want [A]", got)
	}
}

func TestEqualCommits(t *testing.T) {
	s := NewStore()
	mustCommit(t, s, "A")
	mustCommit(t, s, "B", "A")

	if got := mustMergeBases(t, s, "B", "B"); !reflect.DeepEqual(got, []string{"B"}) {
		t.Fatalf("MergeBases(B, B) = %v, want [B]", got)
	}
	ok, err := s.IsAncestor("B", "B")
	if err != nil || !ok {
		t.Fatalf("IsAncestor(B, B) = %v, %v; want true, nil", ok, err)
	}
}

func TestDisjointRoots(t *testing.T) {
	s := NewStore()
	mustCommit(t, s, "R1")
	mustCommit(t, s, "R2")
	mustCommit(t, s, "A", "R1")
	mustCommit(t, s, "B", "R2")

	got, err := s.MergeBases("A", "B")
	if err != nil {
		t.Fatalf("MergeBases(A, B) returned error: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("MergeBases(A, B) = %v, want empty set", got)
	}
}

func TestOctopusMerge(t *testing.T) {
	s := NewStore()
	mustCommit(t, s, "R")
	mustCommit(t, s, "A", "R")
	mustCommit(t, s, "B", "R")
	mustCommit(t, s, "C", "R")
	mustCommit(t, s, "O", "A", "B", "C") // three-parent octopus merge

	if gen, err := s.Gen("O"); err != nil || gen != 3 {
		t.Fatalf("Gen(O) = %d, %v; want 3, nil", gen, err)
	}
	// A commit on the B line only: merge base with O is B.
	mustCommit(t, s, "D", "B")
	if got := mustMergeBases(t, s, "O", "D"); !reflect.DeepEqual(got, []string{"B"}) {
		t.Fatalf("MergeBases(O, D) = %v, want [B]", got)
	}
	// Second octopus one level down: merge bases are A, B, C.
	mustCommit(t, s, "A2", "A")
	mustCommit(t, s, "B2", "B")
	mustCommit(t, s, "C2", "C")
	mustCommit(t, s, "O2", "A2", "B2", "C2")
	if got := mustMergeBases(t, s, "O", "O2"); !reflect.DeepEqual(got, []string{"A", "B", "C"}) {
		t.Fatalf("MergeBases(O, O2) = %v, want [A B C]", got)
	}
}

func TestEqualGenerationNotAncestors(t *testing.T) {
	s := NewStore()
	mustCommit(t, s, "R")
	mustCommit(t, s, "X", "R")
	mustCommit(t, s, "Y", "R")

	gx, err := s.Gen("X")
	if err != nil {
		t.Fatal(err)
	}
	gy, err := s.Gen("Y")
	if err != nil {
		t.Fatal(err)
	}
	if gx != gy {
		t.Fatalf("Gen(X)=%d, Gen(Y)=%d; want equal generations", gx, gy)
	}
	for _, pair := range [][2]string{{"X", "Y"}, {"Y", "X"}} {
		ok, err := s.IsAncestor(pair[0], pair[1])
		if err != nil {
			t.Fatal(err)
		}
		if ok {
			t.Fatalf("IsAncestor(%s, %s) = true, want false", pair[0], pair[1])
		}
	}
}

func TestCommitRejectOrder(t *testing.T) {
	s := NewStore()
	mustCommit(t, s, "root")
	mustCommit(t, s, "dup")
	before := len(s.commits)

	many := []string{"root", "root", "u1", "u2", "u3", "u4", "u5", "u6", "u7"}
	cases := []struct {
		name    string
		id      string
		parents []string
		want    error
		substr  string
	}{
		{"empty id beats everything", "", many, ErrEmptyID, ""},
		{"duplicate id beats parent checks", "dup", []string{"nope"}, ErrDuplicateID, `"dup"`},
		{"too many parents beats duplicates", "many", many, ErrTooManyParents, "9 > 8"},
		{"duplicate parent beats unknown parent", "dp", []string{"root", "root", "nope"}, ErrDuplicateParent, `"root"`},
		{"unknown parent lowest index", "up", []string{"u1", "u2"}, ErrUnknownParent, `parents[0]="u1"`},
		{"unknown parent skips registered prefix", "up2", []string{"root", "u2", "u1"}, ErrUnknownParent, `parents[1]="u2"`},
	}
	for _, tc := range cases {
		err := s.Commit(tc.id, tc.parents)
		if !errors.Is(err, tc.want) {
			t.Errorf("%s: Commit(%q, %v) error = %v, want %v", tc.name, tc.id, tc.parents, err, tc.want)
			continue
		}
		if tc.substr != "" && !strings.Contains(err.Error(), tc.substr) {
			t.Errorf("%s: error %q does not mention %s", tc.name, err, tc.substr)
		}
	}
	if len(s.commits) != before {
		t.Fatalf("rejected commits changed the store: %d commits, want %d", len(s.commits), before)
	}
	for _, id := range []string{"many", "dp", "up", "up2"} {
		if _, err := s.Gen(id); !errors.Is(err, ErrUnknownCommit) {
			t.Fatalf("Gen(%q) after rejection = %v, want ErrUnknownCommit", id, err)
		}
	}
}

func TestUnknownIDs(t *testing.T) {
	s := NewStore()
	mustCommit(t, s, "root")

	if _, err := s.Gen("ghost"); !errors.Is(err, ErrUnknownCommit) || !strings.Contains(err.Error(), `"ghost"`) {
		t.Errorf("Gen(ghost) = %v, want ErrUnknownCommit naming the id", err)
	}
	// a is reported before b.
	if _, err := s.IsAncestor("ga", "gb"); !errors.Is(err, ErrUnknownCommit) || !strings.Contains(err.Error(), `"ga"`) {
		t.Errorf("IsAncestor(ga, gb) = %v, want error naming ga", err)
	}
	if _, err := s.IsAncestor("root", "gb"); !errors.Is(err, ErrUnknownCommit) || !strings.Contains(err.Error(), `"gb"`) {
		t.Errorf("IsAncestor(root, gb) = %v, want error naming gb", err)
	}
	if _, err := s.MergeBases("ga", "gb"); !errors.Is(err, ErrUnknownCommit) || !strings.Contains(err.Error(), `"ga"`) {
		t.Errorf("MergeBases(ga, gb) = %v, want error naming ga", err)
	}
	if _, err := s.MergeBases("root", "gb"); !errors.Is(err, ErrUnknownCommit) || !strings.Contains(err.Error(), `"gb"`) {
		t.Errorf("MergeBases(root, gb) = %v, want error naming gb", err)
	}
	// ErrUnknownCommit must be distinguishable from registration errors.
	if errors.Is(ErrUnknownCommit, ErrUnknownParent) || errors.Is(ErrUnknownCommit, ErrDuplicateID) {
		t.Error("query and registration errors are not distinguishable")
	}
}

// buildFork builds a linear trunk of n commits with two 10-commit branches
// at its tip, and returns the two branch tips.
func buildFork(t *testing.T, s *Store, n int) (tipA, tipB string) {
	t.Helper()
	prev := "t0"
	mustCommit(t, s, prev)
	for i := 1; i < n; i++ {
		id := fmt.Sprintf("t%d", i)
		mustCommit(t, s, id, prev)
		prev = id
	}
	trunkTip := prev
	prevA, prevB := trunkTip, trunkTip
	for i := 0; i < 10; i++ {
		a := fmt.Sprintf("a%d", i)
		mustCommit(t, s, a, prevA)
		prevA = a
		b := fmt.Sprintf("b%d", i)
		mustCommit(t, s, b, prevB)
		prevB = b
	}
	return prevA, prevB
}

func TestTraversalIndependentOfHistoryLength(t *testing.T) {
	counts := make(map[int]int64)
	for _, n := range []int{1000, 100000} {
		s := NewStore()
		tipA, tipB := buildFork(t, s, n)
		got := mustMergeBases(t, s, tipA, tipB)
		want := []string{fmt.Sprintf("t%d", n-1)}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("N=%d: MergeBases(%s, %s) = %v, want %v", n, tipA, tipB, got, want)
		}
		counts[n] = s.visited.Load()
		t.Logf("N=%d: traversed %d commits (history length %d)", n, counts[n], n+20)
	}
	if counts[1000] != counts[100000] {
		t.Fatalf("traversal count depends on history length: N=1000 -> %d, N=100000 -> %d",
			counts[1000], counts[100000])
	}
}

func TestConcurrentUse(t *testing.T) {
	s := NewStore()
	mustCommit(t, s, "root")

	const layers = 8
	const width = 16
	prev := []string{"root"}
	for l := 0; l < layers; l++ {
		var mu sync.Mutex
		var cur []string
		var wg sync.WaitGroup
		for w := 0; w < width; w++ {
			wg.Add(1)
			go func(l, w int) {
				defer wg.Done()
				id := fmt.Sprintf("l%dc%d", l, w)
				// Parents come only from already-completed layers.
				parents := []string{prev[(l*width+w)%len(prev)]}
				if second := prev[(l*width+w+1)%len(prev)]; w%3 == 0 && second != parents[0] {
					parents = append(parents, second)
				}
				if err := s.Commit(id, parents); err != nil {
					t.Errorf("Commit(%q): %v", id, err)
					return
				}
				mu.Lock()
				cur = append(cur, id)
				mu.Unlock()
				// Concurrent queries on already-registered commits.
				if _, err := s.IsAncestor("root", id); err != nil {
					t.Errorf("IsAncestor(root, %q): %v", id, err)
				}
				if _, err := s.Gen(id); err != nil {
					t.Errorf("Gen(%q): %v", id, err)
				}
				if _, err := s.MergeBases("root", id); err != nil {
					t.Errorf("MergeBases(root, %q): %v", id, err)
				}
			}(l, w)
		}
		wg.Wait()
		sort.Strings(cur)
		prev = cur
	}
	// The final graph must be consistent with the naive definition.
	a, b := prev[0], prev[len(prev)-1]
	got := mustMergeBases(t, s, a, b)
	want := naiveMergeBases(s, a, b)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("after concurrent registration: MergeBases(%s, %s) = %v, naive = %v", a, b, got, want)
	}
}

func TestReplayDeterminism(t *testing.T) {
	rnd := rand.New(rand.NewSource(42))
	const n = 200
	type op struct {
		id      string
		parents []string
	}
	var ops []op
	var ids []string
	for i := 0; i < n; i++ {
		id := fmt.Sprintf("c%03d", i)
		k := 0
		if len(ids) > 0 {
			k = rnd.Intn(min(8, len(ids)) + 1)
		}
		perm := rnd.Perm(len(ids))
		var parents []string
		for _, j := range perm[:k] {
			parents = append(parents, ids[j])
		}
		ops = append(ops, op{id, parents})
		ids = append(ids, id)
	}

	run := func() [][]string {
		s := NewStore()
		for _, o := range ops {
			if err := s.Commit(o.id, o.parents); err != nil {
				t.Fatalf("replay Commit(%q): %v", o.id, err)
			}
		}
		queries := rand.New(rand.NewSource(7))
		var out [][]string
		for i := 0; i < 50; i++ {
			a := ids[queries.Intn(len(ids))]
			b := ids[queries.Intn(len(ids))]
			out = append(out, mustMergeBases(t, s, a, b))
		}
		return out
	}
	first := run()
	second := run()
	if !reflect.DeepEqual(first, second) {
		t.Fatal("replaying the same registration sequence gave different results")
	}
}

func TestRandomDifferential(t *testing.T) {
	rnd := rand.New(rand.NewSource(20261002))
	const graphs = 2000
	for g := 0; g < graphs; g++ {
		s := NewStore()
		n := 1 + rnd.Intn(30)
		ids := make([]string, 0, n)
		var desc []string
		for i := 0; i < n; i++ {
			id := fmt.Sprintf("c%d", i)
			k := 0
			if len(ids) > 0 {
				k = rnd.Intn(min(8, len(ids)) + 1)
			}
			perm := rnd.Perm(len(ids))
			var parents []string
			for _, j := range perm[:k] {
				parents = append(parents, ids[j])
			}
			mustCommit(t, s, id, parents...)
			desc = append(desc, fmt.Sprintf("%s<-%v", id, parents))
			ids = append(ids, id)
		}
		queries := 1 + rnd.Intn(4)
		for q := 0; q < queries; q++ {
			a := ids[rnd.Intn(len(ids))]
			b := ids[rnd.Intn(len(ids))]
			got := mustMergeBases(t, s, a, b)
			want := naiveMergeBases(s, a, b)
			ca := map[string]bool{}
			for id := range allAncestors(s, a) {
				if allAncestors(s, b)[id] {
					ca[id] = true
				}
			}
			t.Logf("graph=%d query=(%s,%s) got=%v want=%v basis=CA%v取极大元 input=%v",
				g, a, b, got, want, sortedKeys(ca), desc)
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("graph %d query (%s, %s): got %v, naive %v, input %v",
					g, a, b, got, want, desc)
			}
		}
	}
}

func sortedKeys(m map[string]bool) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

package sticky

import (
	"bytes"
	"errors"
	"fmt"
	"math/rand"
	"strings"
	"sync"
	"testing"
)

func newTestAssignor(t *testing.T, partitions, maxMembers int) *Assignor {
	t.Helper()
	a, err := New(partitions, maxMembers, &logBuf{t: t})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return a
}

type logBuf struct{ t *testing.T }

func (b *logBuf) Write(p []byte) (int, error) {
	b.t.Log(strings.TrimRight(string(p), "\n"))
	return len(p), nil
}

func mustBatch(t *testing.T, a *Assignor, ops []Op) {
	t.Helper()
	if err := a.Batch(ops); err != nil {
		t.Fatalf("Batch(%v) unexpected error: %v", ops, err)
	}
	if err := a.Verify(); err != nil {
		t.Fatalf("Verify after %v: %v", ops, err)
	}
}

func holdings(snap Snapshot) map[string]int {
	h := map[string]int{}
	for _, id := range snap.Members {
		h[id] = 0
	}
	for _, owner := range snap.Assignment {
		h[owner]++
	}
	return h
}

func assertBalanced(t *testing.T, snap Snapshot) {
	t.Helper()
	if len(snap.Members) == 0 {
		if len(snap.Assignment) != 0 {
			t.Fatalf("no members but partitions assigned: %v", snap.Assignment)
		}
		return
	}
	h := holdings(snap)
	min, max := -1, -1
	for _, n := range h {
		if min == -1 || n < min {
			min = n
		}
		if n > max {
			max = n
		}
	}
	if max-min > 1 {
		t.Fatalf("unbalanced holdings %v (min=%d max=%d)", h, min, max)
	}
}

func migrationCount(before, after map[int]string) int {
	n := 0
	for p, old := range before {
		if newOwner, ok := after[p]; ok && old != newOwner {
			n++
		}
	}
	return n
}

func TestNewRejectsInvalidArgs(t *testing.T) {
	for _, c := range []struct{ p, m int }{
		{0, 1}, {-1, 1}, {1, 0}, {1, -2},
	} {
		if _, err := New(c.p, c.m, nil); !errors.Is(err, ErrInvalidArgument) {
			t.Fatalf("New(%d,%d) err=%v, want ErrInvalidArgument", c.p, c.m, err)
		}
	}
}

func TestInterleavedJoinLeave(t *testing.T) {
	a := newTestAssignor(t, 6, 10)

	mustBatch(t, a, []Op{Join("a"), Join("b"), Join("c")})
	snap := a.Query()
	assertBalanced(t, snap)
	if snap.Generation != 1 {
		t.Fatalf("generation=%d want 1", snap.Generation)
	}
	if got := holdings(snap); got["a"] != 2 || got["b"] != 2 || got["c"] != 2 {
		t.Fatalf("initial holdings=%v", got)
	}
	initial := cloneAssign(snap.Assignment)

	// Join two more members: spread must stay within one partition per member.
	mustBatch(t, a, []Op{Join("d"), Join("e")})
	snap = a.Query()
	assertBalanced(t, snap)
	afterJoin := cloneAssign(snap.Assignment)
	if got := migrationCount(initial, afterJoin); got != 2 {
		t.Fatalf("migrations after join d,e = %d, want 2 (a keeps 2, b and c each shed 1)", got)
	}

	// Two leave at once: partitions collapse back, movement must be minimal.
	mustBatch(t, a, []Op{Leave("d"), Leave("e")})
	snap = a.Query()
	assertBalanced(t, snap)
	if fmt.Sprint(snap.Assignment) != fmt.Sprint(initial) {
		t.Fatalf("assignment after d,e leave:\n%v\nwant original:\n%v", snap.Assignment, initial)
	}
	if snap.Generation != 3 {
		t.Fatalf("generation=%d want 3", snap.Generation)
	}

	// Everyone leaves: all partitions unowned, generation still advances.
	mustBatch(t, a, []Op{Leave("a"), Leave("b"), Leave("c")})
	snap = a.Query()
	if len(snap.Assignment) != 0 || len(snap.Members) != 0 {
		t.Fatalf("expected empty group, got members=%v assignment=%v", snap.Members, snap.Assignment)
	}
	assertBalanced(t, snap)
}

func TestRemainderTieBreakByHoldingsThenID(t *testing.T) {
	// 5 partitions, two members where a holds 4 and b holds 1 pre-batch.
	a := newTestAssignor(t, 5, 10)
	a.members = map[string]struct{}{"a": {}, "b": {}}
	a.assignment = map[int]string{0: "a", 1: "a", 2: "a", 3: "a", 4: "b"}
	a.generation = 7

	// Add c: base=1, remainder=2; pre-holdings a=4 > b=1 > c=0, so a and b
	// get quota 2, c gets quota 1. a must release highest-numbered partitions.
	mustBatch(t, a, []Op{Join("c")})
	snap := a.Query()
	h := holdings(snap)
	if h["a"] != 2 || h["b"] != 2 || h["c"] != 1 {
		t.Fatalf("quota-driven holdings=%v want a=2 b=2 c=1", h)
	}
	// a sheds highest-numbered partitions and keeps 0,1; b has quota 2 and
	// keeps p4; filling ascending (p2 then p3): c gets p2, b backfills p3.
	want := map[int]string{0: "a", 1: "a", 2: "c", 3: "b", 4: "b"}
	if fmt.Sprint(snap.Assignment) != fmt.Sprint(want) {
		t.Fatalf("assignment=%v want %v", snap.Assignment, want)
	}
}

func TestDeterministicRegardlessOfBatchOrder(t *testing.T) {
	sequence := [][]Op{
		{Join("a"), Join("b"), Join("c")},
		{Join("d"), Leave("a")},
		{Join("e"), Join("f"), Leave("c"), Leave("b")},
		{Leave("d")},
		{Join("g"), Join("h"), Leave("e")},
	}
	reference := runSequence(t, 11, 16, sequence, nil)

	rng := rand.New(rand.NewSource(42))
	for trial := 0; trial < 20; trial++ {
		shuffled := make([][]Op, len(sequence))
		for i, batch := range sequence {
			perm := rng.Perm(len(batch))
			shuffled[i] = make([]Op, len(batch))
			for j, k := range perm {
				shuffled[i][j] = batch[k]
			}
		}
		got := runSequence(t, 11, 16, shuffled, nil)
		for gen, want := range reference {
			if fmt.Sprint(got[gen]) != fmt.Sprint(want) {
				t.Fatalf("trial %d gen %d:\n got %v\nwant %v", trial, gen, got[gen], want)
			}
		}
	}
}

func runSequence(t *testing.T, partitions, maxMembers int, sequence [][]Op, logW *bytes.Buffer) []map[int]string {
	t.Helper()
	a := newTestAssignor(t, partitions, maxMembers)
	if logW != nil {
		a.logw = logW
	}
	out := make([]map[int]string, 0, len(sequence)+1)
	for _, batch := range sequence {
		mustBatch(t, a, batch)
		out = append(out, cloneAssign(a.Query().Assignment))
	}
	return out
}

func cloneAssign(src map[int]string) map[int]string {
	dst := make(map[int]string, len(src))
	for k, v := range src {
		dst[k] = v
	}
	return dst
}

func TestInvalidBatchesRejectedWithDistinctErrors(t *testing.T) {
	cases := []struct {
		name string
		ops  []Op
		want error
	}{
		{"empty join id", []Op{{Kind: OpJoin, ID: ""}}, ErrEmptyMemberID},
		{"empty leave id", []Op{Join("a"), {Kind: OpLeave, ID: ""}}, ErrEmptyMemberID},
		{"duplicate join", []Op{Join("a"), Join("b"), Join("a")}, ErrDuplicateJoin},
		{"leave unknown", []Op{Join("a"), Leave("ghost")}, ErrMemberNotFound},
		{"too many members", []Op{Join("a"), Join("b"), Join("c")}, ErrTooManyMembers},
		{"bad op kind", []Op{{Kind: OpKind(9), ID: "x"}}, ErrInvalidArgument},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := newTestAssignor(t, 4, 2)
			err := a.Batch(tc.ops)
			if !errors.Is(err, tc.want) {
				t.Fatalf("Batch err=%v, want %v", err, tc.want)
			}
			snap := a.Query()
			if snap.Generation != 0 || len(snap.Members) != 0 || len(snap.Assignment) != 0 {
				t.Fatalf("rejected batch left state behind: %+v", snap)
			}
			if err := a.Verify(); err != nil {
				t.Fatalf("Verify after rejection: %v", err)
			}
		})
	}

	// Distinct sentinel errors must not alias each other.
	sentinels := []error{ErrEmptyMemberID, ErrDuplicateJoin, ErrMemberNotFound, ErrTooManyMembers, ErrInvalidArgument}
	seen := map[string]struct{}{}
	for _, e := range sentinels {
		if _, dup := seen[e.Error()]; dup {
			t.Fatalf("duplicate error message among sentinels: %q", e)
		}
		seen[e.Error()] = struct{}{}
	}
}

func TestRejectionLeavesStateUntouched(t *testing.T) {
	a := newTestAssignor(t, 5, 8)
	mustBatch(t, a, []Op{Join("a"), Join("b"), Join("c")})
	before := a.Query()

	bad := []Op{Join("d"), Join(""), Leave("ghost"), Join("e")}
	if err := a.Batch(bad); err == nil {
		t.Fatal("expected rejection")
	}
	if err := a.Batch([]Op{Join("d"), Join("d")}); !errors.Is(err, ErrDuplicateJoin) {
		t.Fatalf("want duplicate join, got %v", err)
	}
	if err := a.Batch([]Op{Join("d"), Join("e"), Join("f"), Join("g"), Join("h"), Join("i")}); !errors.Is(err, ErrTooManyMembers) {
		t.Fatalf("want too many members, got %v", err)
	}
	after := a.Query()
	if after.Generation != before.Generation {
		t.Fatalf("generation changed %d -> %d after rejections", before.Generation, after.Generation)
	}
	if fmt.Sprint(after.Assignment) != fmt.Sprint(before.Assignment) {
		t.Fatalf("assignment changed after rejections:\nbefore %v\nafter  %v", before.Assignment, after.Assignment)
	}
	if fmt.Sprint(after.Members) != fmt.Sprint(before.Members) {
		t.Fatalf("members changed after rejections: %v -> %v", before.Members, after.Members)
	}
}

func TestMigrationsMatchBruteForceOptimum(t *testing.T) {
	scenarios := []struct {
		partitions int
		batches    [][]Op
	}{
		{4, [][]Op{{Join("a"), Join("b")}, {Join("c")}, {Leave("a")}, {Join("d"), Join("e")}}},
		{5, [][]Op{{Join("a"), Join("b"), Join("c")}, {Leave("b")}, {Join("d")}, {Leave("a"), Leave("c")}}},
		{6, [][]Op{{Join("a"), Join("b")}, {Join("c"), Join("d")}, {Leave("a"), Leave("c")}}},
		{7, [][]Op{{Join("a")}, {Join("b"), Join("c")}, {Leave("a")}, {Join("d"), Join("e"), Leave("b")}}},
	}
	for si, sc := range scenarios {
		a := newTestAssignor(t, sc.partitions, 12)
		var before map[int]string
		for bi, batch := range sc.batches {
			if bi == 0 {
				before = map[int]string{}
			}
			mustBatch(t, a, batch)
			after := cloneAssign(a.Query().Assignment)
			got := migrationCount(before, after)
			want := bruteForceMinMigrations(sc.partitions, before, after, a.Query().Members)
			if got != want {
				t.Fatalf("scenario %d batch %d: migrations=%d but brute-force optimum=%d\nbefore=%v\nafter=%v",
					si, bi, got, want, before, after)
			}
			before = after
		}
	}
}

// bruteForceMinMigrations enumerates every balanced assignment compatible
// with the surviving membership and returns the smallest possible number of
// owner changes relative to the pre-batch assignment.
func bruteForceMinMigrations(partitions int, before map[int]string, after map[int]string, members []string) int {
	wantMembers := map[string]struct{}{}
	for _, id := range members {
		wantMembers[id] = struct{}{}
	}
	// Any balanced assignment has each holder within {base, base+1}; enumerate
	// all such assignments so the reference is independent of quota-tie-break
	// policy.
	base := partitions / len(members)
	quota := map[string]int{}
	for _, id := range members {
		quota[id] = base + 1
	}
	minQ := base

	best := partitions + 1
	assign := make(map[int]string, partitions)
	used := map[string]int{}

	var enumerate func(p int)
	enumerate = func(p int) {
		if partitions-p < len(members)*minQ {
			// Prune: not enough partitions left for every member to reach base.
			needed := 0
			for _, id := range members {
				if used[id] < minQ {
					needed += minQ - used[id]
				}
			}
			if needed > partitions-p {
				return
			}
		}
		if p == partitions {
			for _, id := range members {
				if used[id] < minQ {
					return
				}
			}
			cost := 0
			for q := 0; q < partitions; q++ {
				if old, ok := before[q]; ok {
					if newOwner := assign[q]; old != newOwner {
						cost++
					}
				}
			}
			if cost < best {
				best = cost
			}
			return
		}
		for _, id := range members {
			if used[id] >= quota[id] {
				continue
			}
			assign[p] = id
			used[id]++
			enumerate(p + 1)
			used[id]--
			delete(assign, p)
		}
	}
	enumerate(0)
	if best > partitions {
		panic("brute force found no feasible assignment")
	}
	return best
}

func TestConcurrentAccess(t *testing.T) {
	a := newTestAssignor(t, 12, 32)
	mustBatch(t, a, []Op{Join("a"), Join("b"), Join("c")})

	const goroutines = 16
	var wg sync.WaitGroup
	stop := make(chan struct{})

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			memberID := fmt.Sprintf("m%d", id)
			for {
				select {
				case <-stop:
					return
				default:
				}
				if err := a.Batch([]Op{Join(memberID)}); err != nil &&
					!errors.Is(err, ErrTooManyMembers) && !errors.Is(err, ErrDuplicateJoin) {
					t.Errorf("unexpected join error: %v", err)
					return
				}
				snap := a.Query()
				assertBalanced(t, snap)
				if err := a.Verify(); err != nil {
					t.Errorf("verify: %v", err)
					return
				}
				_ = a.Batch([]Op{Leave(memberID)})
				_ = a.Query()
			}
		}(i)
	}

	// Readers-only hammering in parallel.
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
					_ = a.Query()
					if err := a.Verify(); err != nil {
						t.Errorf("verify reader: %v", err)
						return
					}
				}
			}
		}()
	}

	// Deterministic lifecycle batches concurrently with the churn.
	for g := 0; g < 3; g++ {
		mustBatch(t, a, []Op{Join("x"), Join("y")})
		_ = a.Batch([]Op{Leave("x"), Leave("y")})
	}

	close(stop)
	wg.Wait()

	if err := a.Verify(); err != nil {
		t.Fatalf("final verify: %v", err)
	}
	snap := a.Query()
	assertBalanced(t, snap)
}

func TestEmptyBatchIsNoOp(t *testing.T) {
	a := newTestAssignor(t, 3, 4)
	if err := a.Batch(nil); err != nil {
		t.Fatalf("empty batch: %v", err)
	}
	if snap := a.Query(); snap.Generation != 0 {
		t.Fatalf("empty batch changed generation to %d", snap.Generation)
	}
}

func TestLoggingRecordsInputsAssignmentAndRationale(t *testing.T) {
	var buf bytes.Buffer
	a, err := New(4, 8, &buf)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Batch([]Op{Join("a"), Join("b"), Join("c")}); err != nil {
		t.Fatal(err)
	}
	_ = a.Query()
	_ = a.Verify()
	log := buf.String()
	for _, want := range []string{
		"batch: ops=[join:a join:b join:c]", // step inputs
		"base=1 remainder=1",                // quota basis
		"quotaOrder=",                       // remainder ordering rationale
		"unowned",                           // fill decision rationale
		"migrations=",                       // migration accounting
		"assignment={0:a",                   // resulting assignment
		"verify: generation=1 ok",           // self-check result
	} {
		if !strings.Contains(log, want) {
			t.Fatalf("log missing %q\nfull log:\n%s", want, log)
		}
	}

	buf.Reset()
	err = a.Batch([]Op{Join(""), Leave("ghost")})
	if !errors.Is(err, ErrEmptyMemberID) {
		t.Fatalf("err=%v want ErrEmptyMemberID", err)
	}
	if !strings.Contains(buf.String(), "rejected") {
		t.Fatalf("rejection log missing rationale:\n%s", buf.String())
	}
}

package reconcile_test

import (
	"errors"
	"fmt"
	"reflect"
	"testing"

	"ontology/reconcile"
)

func mustAdd(t *testing.T, m *reconcile.Matcher, side reconcile.Side, id string, amt, day int64, ref string) {
	t.Helper()
	if err := m.AddLine(side, id, amt, day, ref); err != nil {
		t.Fatalf("AddLine(%v, %q) unexpected error: %v", side, id, err)
	}
}

func mustReconcile(t *testing.T, m *reconcile.Matcher, w int64) []reconcile.Match {
	t.Helper()
	got, err := m.Reconcile(w)
	if err != nil {
		t.Fatalf("Reconcile(%d) unexpected error: %v", w, err)
	}
	return got
}

func expectMatches(t *testing.T, what string, got, want []reconcile.Match) {
	t.Helper()
	if len(got) == 0 && len(want) == 0 {
		return
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%s:\n got %+v\nwant %+v", what, got, want)
	}
}

// The first worked example from the specification.
func TestSpecExampleOne(t *testing.T) {
	m := reconcile.New()
	mustAdd(t, m, reconcile.Bank, "b1", 100, 10, "X")
	mustAdd(t, m, reconcile.Bank, "b2", 100, 12, "X")
	mustAdd(t, m, reconcile.Bank, "b3", 300, 20, "Y")
	mustAdd(t, m, reconcile.Bank, "b4", 50, 30, "")
	mustAdd(t, m, reconcile.Bank, "b5", 50, 31, "")
	mustAdd(t, m, reconcile.Book, "k1", 100, 11, "X")
	mustAdd(t, m, reconcile.Book, "k2", 100, 13, "X")
	mustAdd(t, m, reconcile.Book, "k3", 100, 19, "Y")
	mustAdd(t, m, reconcile.Book, "k4", 200, 21, "Y")
	mustAdd(t, m, reconcile.Book, "k5", 50, 31, "")

	got := mustReconcile(t, m, 2)
	want := []reconcile.Match{
		{MID: 1, Round: 1, BankIDs: []string{"b1"}, BookIDs: []string{"k1"}},
		{MID: 2, Round: 1, BankIDs: []string{"b2"}, BookIDs: []string{"k2"}},
		{MID: 3, Round: 2, BankIDs: []string{"b4"}, BookIDs: []string{"k5"}},
		{MID: 4, Round: 3, BankIDs: []string{"b3"}, BookIDs: []string{"k3", "k4"}},
	}
	expectMatches(t, "spec example 1", got, want)
	// Matches() orders by smallest bank id of each match, then mid.
	wantByBank := []reconcile.Match{want[0], want[1], want[3], want[2]}
	expectMatches(t, "spec example 1 Matches()", m.Matches(), wantByBank)

	if got := m.Unmatched(reconcile.Bank); len(got) != 1 || got[0].ID != "b5" {
		t.Fatalf("unmatched bank = %+v, want [b5]", got)
	}
	if got := m.Unmatched(reconcile.Book); len(got) != 0 {
		t.Fatalf("unmatched book = %+v, want []", got)
	}
}

// The second worked example: many-to-one, reverse, forbidden set, new mid.
func TestSpecExampleTwo(t *testing.T) {
	m := reconcile.New()
	mustAdd(t, m, reconcile.Book, "s1", 300, 40, "Z")
	mustAdd(t, m, reconcile.Bank, "t1", 100, 39, "Z")
	mustAdd(t, m, reconcile.Bank, "t2", 200, 41, "Z")

	got := mustReconcile(t, m, 2)
	want := []reconcile.Match{
		{MID: 1, Round: 4, BankIDs: []string{"t1", "t2"}, BookIDs: []string{"s1"}},
	}
	expectMatches(t, "spec example 2 round 4", got, want)

	restored, err := m.Reverse(1)
	if err != nil {
		t.Fatalf("Reverse(1) unexpected error: %v", err)
	}
	expectMatches(t, "restored lines", []reconcile.Match{restored}, want)

	wantF := []reconcile.Pair{
		{BankID: "t1", BookID: "s1"},
		{BankID: "t2", BookID: "s1"},
	}
	if got := m.Forbidden(); !reflect.DeepEqual(got, wantF) {
		t.Fatalf("forbidden = %+v, want %+v", got, wantF)
	}

	// The forbidden pairs block round 4: T becomes empty, no new match.
	if got := mustReconcile(t, m, 2); len(got) != 0 {
		t.Fatalf("reconcile after reverse = %+v, want no matches", got)
	}

	// A fresh equal-amount line matches s1 in round 1 with a new mid.
	mustAdd(t, m, reconcile.Bank, "t3", 300, 40, "Z")
	got = mustReconcile(t, m, 2)
	want = []reconcile.Match{
		{MID: 2, Round: 1, BankIDs: []string{"t3"}, BookIDs: []string{"s1"}},
	}
	expectMatches(t, "spec example 2 after t3", got, want)

	unmatched := m.Unmatched(reconcile.Bank)
	if len(unmatched) != 2 || unmatched[0].ID != "t1" || unmatched[1].ID != "t2" {
		t.Fatalf("unmatched bank = %+v, want [t1 t2]", unmatched)
	}
}

// Date distance exactly W matches; W+1 does not.
func TestDateDistanceBoundary(t *testing.T) {
	m := reconcile.New()
	mustAdd(t, m, reconcile.Bank, "b1", 100, 10, "X")
	mustAdd(t, m, reconcile.Book, "k1", 100, 12, "X") // distance 2 == W
	mustAdd(t, m, reconcile.Bank, "b2", 100, 10, "Y")
	mustAdd(t, m, reconcile.Book, "k2", 100, 13, "Y") // distance 3 == W+1

	got := mustReconcile(t, m, 2)
	want := []reconcile.Match{
		{MID: 1, Round: 1, BankIDs: []string{"b1"}, BookIDs: []string{"k1"}},
	}
	expectMatches(t, "distance == W matches, W+1 does not", got, want)
}

// Round 1 requires equal amounts: a same-ref book line with a different
// amount is skipped; the equal-amount different-ref line is taken in round 2.
func TestRound1RequiresEqualAmount(t *testing.T) {
	m := reconcile.New()
	mustAdd(t, m, reconcile.Bank, "b1", 100, 10, "X")
	mustAdd(t, m, reconcile.Book, "k1", 200, 10, "X") // same ref, wrong amount
	mustAdd(t, m, reconcile.Book, "k2", 100, 10, "Y") // different ref, equal amount

	got := mustReconcile(t, m, 2)
	want := []reconcile.Match{
		{MID: 1, Round: 2, BankIDs: []string{"b1"}, BookIDs: []string{"k2"}},
	}
	expectMatches(t, "same ref different amount skipped in round 1", got, want)
}

// Round 1 runs before round 2: a ref-equal book line at distance 2 wins over
// a ref-different equal-amount book line at distance 0.
func TestRound1BeatsRound2(t *testing.T) {
	m := reconcile.New()
	mustAdd(t, m, reconcile.Bank, "b1", 100, 10, "X")
	mustAdd(t, m, reconcile.Book, "k1", 100, 12, "X") // same ref, distance 2
	mustAdd(t, m, reconcile.Book, "k2", 100, 10, "Y") // other ref, distance 0

	got := mustReconcile(t, m, 2)
	want := []reconcile.Match{
		{MID: 1, Round: 1, BankIDs: []string{"b1"}, BookIDs: []string{"k1"}},
	}
	expectMatches(t, "round 1 beats round 2", got, want)
}

// Equal date distances break the tie by smallest book id; this includes the
// symmetric case of one candidate on each side of the bank day.
func TestTieBreakSmallestBookID(t *testing.T) {
	m := reconcile.New()
	mustAdd(t, m, reconcile.Bank, "b1", 100, 10, "X")
	mustAdd(t, m, reconcile.Book, "k2", 100, 11, "X") // distance 1
	mustAdd(t, m, reconcile.Book, "k1", 100, 9, "X")  // distance 1, smaller id

	got := mustReconcile(t, m, 2)
	want := []reconcile.Match{
		{MID: 1, Round: 1, BankIDs: []string{"b1"}, BookIDs: []string{"k1"}},
	}
	expectMatches(t, "tie on distance picks smallest id", got, want)
}

// Amounts of opposite sign are never equal, so they never match.
func TestOppositeSignsNeverMatch(t *testing.T) {
	m := reconcile.New()
	mustAdd(t, m, reconcile.Bank, "b1", 100, 10, "X")
	mustAdd(t, m, reconcile.Book, "k1", -100, 10, "X")

	if got := mustReconcile(t, m, 2); len(got) != 0 {
		t.Fatalf("opposite signs matched: %+v", got)
	}
}

// The bank line processed first wins; there is no global optimum. b4 (id
// order first) takes k5 at distance 1 even though b5 is at distance 0.
func TestFirstProcessedBankWins(t *testing.T) {
	m := reconcile.New()
	mustAdd(t, m, reconcile.Bank, "b4", 50, 30, "")
	mustAdd(t, m, reconcile.Bank, "b5", 50, 31, "")
	mustAdd(t, m, reconcile.Book, "k5", 50, 31, "")

	got := mustReconcile(t, m, 2)
	want := []reconcile.Match{
		{MID: 1, Round: 2, BankIDs: []string{"b4"}, BookIDs: []string{"k5"}},
	}
	expectMatches(t, "first processed bank wins", got, want)
	if got := m.Unmatched(reconcile.Bank); len(got) != 1 || got[0].ID != "b5" {
		t.Fatalf("unmatched bank = %+v, want [b5]", got)
	}
}

// Round 3 with |S| == 1 does not match.
func TestRound3SingletonSetNoMatch(t *testing.T) {
	m := reconcile.New()
	mustAdd(t, m, reconcile.Bank, "b1", 200, 10, "X")
	mustAdd(t, m, reconcile.Book, "k1", 100, 10, "X")

	if got := mustReconcile(t, m, 2); len(got) != 0 {
		t.Fatalf("round 3 with |S|=1 matched: %+v", got)
	}
}

// Round 4 with |T| == 1 does not match.
func TestRound4SingletonSetNoMatch(t *testing.T) {
	m := reconcile.New()
	mustAdd(t, m, reconcile.Book, "k1", 200, 10, "Y")
	mustAdd(t, m, reconcile.Bank, "b1", 100, 10, "Y")

	if got := mustReconcile(t, m, 2); len(got) != 0 {
		t.Fatalf("round 4 with |T|=1 matched: %+v", got)
	}
}

// A sum off by one does not match, in either direction.
func TestSumOffByOneNoMatch(t *testing.T) {
	m := reconcile.New()
	mustAdd(t, m, reconcile.Bank, "b1", 300, 10, "X")
	mustAdd(t, m, reconcile.Book, "k1", 100, 10, "X")
	mustAdd(t, m, reconcile.Book, "k2", 199, 10, "X") // sum 299 != 300

	if got := mustReconcile(t, m, 2); len(got) != 0 {
		t.Fatalf("round 3 off-by-one sum matched: %+v", got)
	}

	m2 := reconcile.New()
	mustAdd(t, m2, reconcile.Book, "k1", 300, 10, "Z")
	mustAdd(t, m2, reconcile.Bank, "b1", 100, 10, "Z")
	mustAdd(t, m2, reconcile.Bank, "b2", 201, 10, "Z") // sum 301 != 300

	if got := mustReconcile(t, m2, 2); len(got) != 0 {
		t.Fatalf("round 4 off-by-one sum matched: %+v", got)
	}
}

// Same-ref lines outside the date window are not part of S.
func TestRound3ExcludesOutOfWindow(t *testing.T) {
	m := reconcile.New()
	mustAdd(t, m, reconcile.Bank, "b1", 300, 10, "X")
	mustAdd(t, m, reconcile.Book, "k1", 100, 11, "X") // in window
	mustAdd(t, m, reconcile.Book, "k2", 200, 13, "X") // distance 3 > W=2

	if got := mustReconcile(t, m, 2); len(got) != 0 {
		t.Fatalf("out-of-window line entered S: %+v", got)
	}
}

// Lines already matched stay untouched by later Reconcile calls.
func TestMatchedLinesUntouched(t *testing.T) {
	m := reconcile.New()
	mustAdd(t, m, reconcile.Bank, "b1", 100, 10, "X")
	mustAdd(t, m, reconcile.Book, "k1", 100, 10, "X")
	first := mustReconcile(t, m, 2)
	if len(first) != 1 {
		t.Fatalf("first reconcile = %+v, want 1 match", first)
	}

	// Even with a huge window and tempting new lines, mid 1 is unchanged.
	mustAdd(t, m, reconcile.Bank, "b2", 100, 10, "X")
	mustAdd(t, m, reconcile.Book, "k2", 100, 10, "X")
	second := mustReconcile(t, m, 1_000_000_000)
	wantSecond := []reconcile.Match{
		{MID: 2, Round: 1, BankIDs: []string{"b2"}, BookIDs: []string{"k2"}},
	}
	expectMatches(t, "second reconcile", second, wantSecond)
	expectMatches(t, "all matches", m.Matches(), append(first, second...))
}

// After Reverse, the forbidden combination can never reappear in any round.
func TestReverseForbidsRoundsOneAndTwo(t *testing.T) {
	m := reconcile.New()
	mustAdd(t, m, reconcile.Bank, "b1", 100, 10, "X")
	mustAdd(t, m, reconcile.Book, "k1", 100, 10, "X")
	mustReconcile(t, m, 2) // mid 1, round 1
	if _, err := m.Reverse(1); err != nil {
		t.Fatalf("Reverse(1): %v", err)
	}
	// Neither round 1 (same ref) nor round 2 (no ref condition) may re-pair
	// the forbidden combination, and no other line exists to pair with.
	if got := mustReconcile(t, m, 1_000_000_000); len(got) != 0 {
		t.Fatalf("forbidden pair re-matched: %+v", got)
	}
}

// When exactly one combination of a would-be one-to-many group is forbidden,
// S shrinks and the amount sum no longer holds, so nothing matches. The
// control run without the reverse shows that the group would have matched.
func TestReverseShrinksOneToManySet(t *testing.T) {
	// b1=300, b2=-100, k1=200 first form a many-to-one match; reversing it
	// forbids (b1,k1) and (b2,k1). With k2=k3=50 added later, b1's round-3
	// set S shrinks to {k2,k3} (sum 100 != 300), so nothing matches.
	m := reconcile.New()
	mustAdd(t, m, reconcile.Bank, "b1", 300, 10, "X")
	mustAdd(t, m, reconcile.Bank, "b2", -100, 10, "X")
	mustAdd(t, m, reconcile.Book, "k1", 200, 10, "X")
	got := mustReconcile(t, m, 2)
	want := []reconcile.Match{
		{MID: 1, Round: 4, BankIDs: []string{"b1", "b2"}, BookIDs: []string{"k1"}},
	}
	expectMatches(t, "many-to-one setup", got, want)
	if _, err := m.Reverse(1); err != nil {
		t.Fatalf("Reverse(1): %v", err)
	}
	mustAdd(t, m, reconcile.Book, "k2", 50, 10, "X")
	mustAdd(t, m, reconcile.Book, "k3", 50, 10, "X")
	if got := mustReconcile(t, m, 2); len(got) != 0 {
		t.Fatalf("shrunk S still matched: %+v", got)
	}
	if got := m.Unmatched(reconcile.Bank); len(got) != 2 {
		t.Fatalf("unmatched bank = %+v, want 2 lines", got)
	}
	if got := m.Unmatched(reconcile.Book); len(got) != 3 {
		t.Fatalf("unmatched book = %+v, want 3 lines", got)
	}

	// Control: the same five lines without the reverse produce the
	// one-to-many match b1 -> {k1, k2, k3} (sum 300) in round 3.
	control := reconcile.New()
	mustAdd(t, control, reconcile.Bank, "b1", 300, 10, "X")
	mustAdd(t, control, reconcile.Bank, "b2", -100, 10, "X")
	mustAdd(t, control, reconcile.Book, "k1", 200, 10, "X")
	mustAdd(t, control, reconcile.Book, "k2", 50, 10, "X")
	mustAdd(t, control, reconcile.Book, "k3", 50, 10, "X")
	got = mustReconcile(t, control, 2)
	want = []reconcile.Match{
		{MID: 1, Round: 3, BankIDs: []string{"b1"}, BookIDs: []string{"k1", "k2", "k3"}},
	}
	expectMatches(t, "control one-to-many", got, want)
}

// A reversed mid is never recycled: the next match gets a fresh mid.
func TestMIDNotRecycled(t *testing.T) {
	m := reconcile.New()
	mustAdd(t, m, reconcile.Bank, "b1", 100, 10, "X")
	mustAdd(t, m, reconcile.Book, "k1", 100, 10, "X")
	mustReconcile(t, m, 2) // mid 1
	if _, err := m.Reverse(1); err != nil {
		t.Fatalf("Reverse(1): %v", err)
	}

	mustAdd(t, m, reconcile.Bank, "b2", 100, 10, "Y")
	mustAdd(t, m, reconcile.Book, "k2", 100, 10, "Y")
	got := mustReconcile(t, m, 2)
	want := []reconcile.Match{
		{MID: 2, Round: 1, BankIDs: []string{"b2"}, BookIDs: []string{"k2"}},
	}
	expectMatches(t, "new match gets fresh mid", got, want)
}

// Reverse distinguishes a mid that was never produced from one already
// reversed.
func TestReverseErrorDistinction(t *testing.T) {
	m := reconcile.New()
	if _, err := m.Reverse(1); !errors.Is(err, reconcile.ErrMatchNotFound) {
		t.Fatalf("Reverse(1) on empty matcher = %v, want ErrMatchNotFound", err)
	}
	if _, err := m.Reverse(0); !errors.Is(err, reconcile.ErrMatchNotFound) {
		t.Fatalf("Reverse(0) = %v, want ErrMatchNotFound", err)
	}

	mustAdd(t, m, reconcile.Bank, "b1", 100, 10, "X")
	mustAdd(t, m, reconcile.Book, "k1", 100, 10, "X")
	mustReconcile(t, m, 2) // mid 1

	if _, err := m.Reverse(2); !errors.Is(err, reconcile.ErrMatchNotFound) {
		t.Fatalf("Reverse(2) = %v, want ErrMatchNotFound", err)
	}
	if _, err := m.Reverse(1); err != nil {
		t.Fatalf("Reverse(1) = %v, want nil", err)
	}
	if _, err := m.Reverse(1); !errors.Is(err, reconcile.ErrMatchReversed) {
		t.Fatalf("second Reverse(1) = %v, want ErrMatchReversed", err)
	}
}

// AddLine reports exactly one reason, in the specified order.
func TestAddLineValidationOrder(t *testing.T) {
	m := reconcile.New()

	// Invalid side wins over every line problem.
	if err := m.AddLine(reconcile.Side(7), "", 0, -1, ""); !errors.Is(err, reconcile.ErrInvalidSide) {
		t.Fatalf("invalid side = %v", err)
	}
	// Empty id wins over bad amount and bad day.
	if err := m.AddLine(reconcile.Bank, "", 0, -1, ""); !errors.Is(err, reconcile.ErrEmptyID) {
		t.Fatalf("empty id = %v", err)
	}
	// Bad amount wins over bad day.
	if err := m.AddLine(reconcile.Bank, "b1", 0, -1, ""); !errors.Is(err, reconcile.ErrInvalidAmount) {
		t.Fatalf("zero amount = %v", err)
	}
	if err := m.AddLine(reconcile.Bank, "b1", 1_000_000_000_001, 0, ""); !errors.Is(err, reconcile.ErrInvalidAmount) {
		t.Fatalf("amount too large = %v", err)
	}
	if err := m.AddLine(reconcile.Bank, "b1", -1_000_000_000_001, 0, ""); !errors.Is(err, reconcile.ErrInvalidAmount) {
		t.Fatalf("amount too small = %v", err)
	}
	// Boundary amounts are accepted.
	if err := m.AddLine(reconcile.Bank, "b1", 1_000_000_000_000, 0, ""); err != nil {
		t.Fatalf("max amount rejected: %v", err)
	}
	if err := m.AddLine(reconcile.Bank, "b2", -1_000_000_000_000, 1_000_000_000, ""); err != nil {
		t.Fatalf("min amount / max day rejected: %v", err)
	}
	// Bad day.
	if err := m.AddLine(reconcile.Bank, "b3", 1, -1, ""); !errors.Is(err, reconcile.ErrInvalidDay) {
		t.Fatalf("negative day = %v", err)
	}
	if err := m.AddLine(reconcile.Bank, "b3", 1, 1_000_000_001, ""); !errors.Is(err, reconcile.ErrInvalidDay) {
		t.Fatalf("day too large = %v", err)
	}
	// Duplicate id, including one belonging to a matched line.
	if err := m.AddLine(reconcile.Bank, "b1", 5, 5, ""); !errors.Is(err, reconcile.ErrDuplicateID) {
		t.Fatalf("duplicate id = %v", err)
	}
	// The same id on the other side is fine.
	if err := m.AddLine(reconcile.Book, "b1", 5, 5, ""); err != nil {
		t.Fatalf("same id on other side rejected: %v", err)
	}
}

// A full side reports ErrSideFull before ErrDuplicateID.
func TestAddLineSideFull(t *testing.T) {
	m := reconcile.New()
	for i := 0; i < 100000; i++ {
		id := fmt.Sprintf("b%06d", i)
		if err := m.AddLine(reconcile.Bank, id, 1, 0, ""); err != nil {
			t.Fatalf("add %d: %v", i, err)
		}
	}
	if err := m.AddLine(reconcile.Bank, "zzzzz", 1, 0, ""); !errors.Is(err, reconcile.ErrSideFull) {
		t.Fatalf("full side = %v, want ErrSideFull", err)
	}
	// Full side wins over duplicate id.
	if err := m.AddLine(reconcile.Bank, "b000000", 1, 0, ""); !errors.Is(err, reconcile.ErrSideFull) {
		t.Fatalf("full side with duplicate = %v, want ErrSideFull", err)
	}
	// The other side is unaffected.
	if err := m.AddLine(reconcile.Book, "k1", 1, 0, ""); err != nil {
		t.Fatalf("book side rejected: %v", err)
	}
}

// Reconcile rejects an out-of-range window.
func TestReconcileInvalidWindow(t *testing.T) {
	m := reconcile.New()
	for _, w := range []int64{-1, 1_000_000_001} {
		if _, err := m.Reconcile(w); !errors.Is(err, reconcile.ErrInvalidWindow) {
			t.Fatalf("Reconcile(%d) = %v, want ErrInvalidWindow", w, err)
		}
	}
	if _, err := m.Reconcile(0); err != nil {
		t.Fatalf("Reconcile(0) = %v", err)
	}
	if _, err := m.Reconcile(1_000_000_000); err != nil {
		t.Fatalf("Reconcile(1e9) = %v", err)
	}
}

// A rejected operation changes nothing: lines, matches, F and the mid
// counter all stay as they were.
func TestRejectedOperationsKeepState(t *testing.T) {
	m := reconcile.New()
	mustAdd(t, m, reconcile.Bank, "b1", 100, 10, "X")
	mustAdd(t, m, reconcile.Book, "k1", 100, 10, "X")
	mustReconcile(t, m, 2) // mid 1
	if _, err := m.Reverse(1); err != nil {
		t.Fatalf("Reverse(1): %v", err)
	}

	snapshot := func() ([]reconcile.Match, []reconcile.Line, []reconcile.Line, []reconcile.Pair) {
		return m.Matches(), m.Unmatched(reconcile.Bank), m.Unmatched(reconcile.Book), m.Forbidden()
	}
	beforeM, beforeUB, beforeUK, beforeF := snapshot()

	// Rejected AddLine calls of every flavour.
	_ = m.AddLine(reconcile.Side(3), "x", 1, 1, "")
	_ = m.AddLine(reconcile.Bank, "", 1, 1, "")
	_ = m.AddLine(reconcile.Bank, "x", 0, 1, "")
	_ = m.AddLine(reconcile.Bank, "x", 1, -1, "")
	_ = m.AddLine(reconcile.Bank, "b1", 1, 1, "")
	// Rejected Reconcile.
	_, _ = m.Reconcile(-1)
	_, _ = m.Reconcile(1_000_000_001)
	// Rejected Reverse.
	_, _ = m.Reverse(99)
	_, _ = m.Reverse(1)

	afterM, afterUB, afterUK, afterF := snapshot()
	if !reflect.DeepEqual(beforeM, afterM) || !reflect.DeepEqual(beforeUB, afterUB) ||
		!reflect.DeepEqual(beforeUK, afterUK) || !reflect.DeepEqual(beforeF, afterF) {
		t.Fatalf("rejected operations changed state")
	}

	// The mid counter was not advanced by any rejected call.
	mustAdd(t, m, reconcile.Bank, "b2", 100, 10, "Y")
	mustAdd(t, m, reconcile.Book, "k2", 100, 10, "Y")
	got := mustReconcile(t, m, 2)
	want := []reconcile.Match{
		{MID: 2, Round: 1, BankIDs: []string{"b2"}, BookIDs: []string{"k2"}},
	}
	expectMatches(t, "mid counter intact", got, want)
}

// Matches() orders by smallest bank id of the match, then by mid.
func TestMatchesOrdering(t *testing.T) {
	m := reconcile.New()
	mustAdd(t, m, reconcile.Bank, "z1", 100, 10, "X")
	mustAdd(t, m, reconcile.Book, "k1", 100, 10, "X")
	mustReconcile(t, m, 2) // mid 1: bank z1
	mustAdd(t, m, reconcile.Bank, "a1", 100, 10, "Y")
	mustAdd(t, m, reconcile.Book, "k2", 100, 10, "Y")
	mustReconcile(t, m, 2) // mid 2: bank a1

	got := m.Matches()
	if len(got) != 2 || got[0].MID != 2 || got[1].MID != 1 {
		t.Fatalf("Matches() order = %+v, want mid 2 (bank a1) before mid 1 (bank z1)", got)
	}
}

// The same batch of lines registered in any order yields identical matches.
func TestRegistrationOrderIrrelevant(t *testing.T) {
	type entry struct {
		side     reconcile.Side
		id       string
		amt, day int64
		ref      string
	}
	lines := []entry{
		{reconcile.Bank, "b1", 100, 10, "X"},
		{reconcile.Bank, "b2", 100, 12, "X"},
		{reconcile.Bank, "b3", 300, 20, "Y"},
		{reconcile.Bank, "b4", 50, 30, ""},
		{reconcile.Book, "k1", 100, 11, "X"},
		{reconcile.Book, "k2", 100, 13, "X"},
		{reconcile.Book, "k3", 100, 19, "Y"},
		{reconcile.Book, "k4", 200, 21, "Y"},
		{reconcile.Book, "k5", 50, 31, ""},
	}
	run := func(order []int) []reconcile.Match {
		m := reconcile.New()
		for _, i := range order {
			e := lines[i]
			mustAdd(t, m, e.side, e.id, e.amt, e.day, e.ref)
		}
		return mustReconcile(t, m, 2)
	}
	forward := make([]int, len(lines))
	backward := make([]int, len(lines))
	for i := range lines {
		forward[i] = i
		backward[i] = len(lines) - 1 - i
	}
	gotForward := run(forward)
	gotBackward := run(backward)
	if !reflect.DeepEqual(gotForward, gotBackward) {
		t.Fatalf("registration order changed result:\nforward  %+v\nbackward %+v", gotForward, gotBackward)
	}
}

// Replaying the same call sequence reproduces identical matches and F.
func TestReplayDeterminism(t *testing.T) {
	play := func() ([]reconcile.Match, []reconcile.Pair) {
		m := reconcile.New()
		mustAdd(t, m, reconcile.Bank, "b1", 100, 10, "X")
		mustAdd(t, m, reconcile.Bank, "b2", 200, 11, "X")
		mustAdd(t, m, reconcile.Book, "k1", 300, 10, "X")
		mustAdd(t, m, reconcile.Book, "k2", 100, 10, "X")
		first := mustReconcile(t, m, 2)
		if _, err := m.Reverse(first[0].MID); err != nil {
			t.Fatalf("Reverse: %v", err)
		}
		second := mustReconcile(t, m, 2)
		return append(first, second...), m.Forbidden()
	}
	m1, f1 := play()
	m2, f2 := play()
	if !reflect.DeepEqual(m1, m2) || !reflect.DeepEqual(f1, f2) {
		t.Fatalf("replay diverged:\n%+v %+v\n%+v %+v", m1, f1, m2, f2)
	}
}

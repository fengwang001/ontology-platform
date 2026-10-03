package maglev

import (
	"errors"
	"sync"
	"testing"
)

func mustNew(t *testing.T, m, lim int) *Table {
	t.Helper()
	tb, err := New(m, lim)
	if err != nil {
		t.Fatalf("New(%d, %d) failed: %v", m, lim, err)
	}
	return tb
}

func mustAdd(t *testing.T, tb *Table, name string, offset, skip, weight int) int {
	t.Helper()
	n, err := tb.AddBackend(name, offset, skip, weight)
	if err != nil {
		t.Fatalf("AddBackend(%q, %d, %d, %d) failed: %v", name, offset, skip, weight, err)
	}
	return n
}

func mustRemove(t *testing.T, tb *Table, name string) int {
	t.Helper()
	n, err := tb.RemoveBackend(name)
	if err != nil {
		t.Fatalf("RemoveBackend(%q) failed: %v", name, err)
	}
	return n
}

func wantTable(t *testing.T, tb *Table, want []string) {
	t.Helper()
	got := tb.Table()
	if len(got) != len(want) {
		t.Fatalf("table length = %d, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("table = %v, want %v", got, want)
		}
	}
}

func wantPending(t *testing.T, tb *Table, want int) {
	t.Helper()
	if got := tb.Pending(); got != want {
		t.Fatalf("Pending = %d, want %d", got, want)
	}
}

// drain Steps until cur converges to T*.
func drain(t *testing.T, tb *Table) {
	t.Helper()
	for i := 0; i <= tb.M(); i++ {
		if tb.Pending() == 0 {
			return
		}
		tb.Step()
	}
	t.Fatalf("drain: Pending still %d after %d steps", tb.Pending(), tb.M())
}

// TestConfigValidation rejects illegal configurations as a whole and accepts
// boundary configurations.
func TestConfigValidation(t *testing.T) {
	for _, m := range []int{-7, -1, 0, 1, 4, 6, 9, 100, 65536, 65538, 65539, 70000} {
		if _, err := New(m, 1); !errors.Is(err, ErrInvalidConfig) {
			t.Errorf("New(%d, 1) err = %v, want ErrInvalidConfig", m, err)
		}
	}
	for _, lim := range []int{-2, -1, 0, 8, 100} {
		if _, err := New(7, lim); !errors.Is(err, ErrInvalidConfig) {
			t.Errorf("New(7, %d) err = %v, want ErrInvalidConfig", lim, err)
		}
	}
	for _, cfg := range [][2]int{{2, 1}, {2, 2}, {3, 3}, {65537, 1}, {65537, 65537}} {
		if _, err := New(cfg[0], cfg[1]); err != nil {
			t.Errorf("New(%d, %d) failed: %v", cfg[0], cfg[1], err)
		}
	}
}

// TestAddBackendRejectionOrder checks that rejection reasons are
// distinguishable and reported in the required order, first match only.
func TestAddBackendRejectionOrder(t *testing.T) {
	tb := mustNew(t, 7, 1)
	mustAdd(t, tb, "A", 0, 1, 1)

	cases := []struct {
		name                 string
		offset, skip, weight int
		want                 error
	}{
		{"", 0, 1, 1, ErrEmptyName},      // empty name
		{"", 99, 0, 99, ErrEmptyName},    // empty name wins over bad ranges
		{"B", -1, 1, 1, ErrOutOfRange},   // offset < 0
		{"B", 7, 1, 1, ErrOutOfRange},    // offset >= M
		{"B", 0, 0, 1, ErrOutOfRange},    // skip < 1
		{"B", 0, 7, 1, ErrOutOfRange},    // skip >= M
		{"B", 0, 1, 0, ErrOutOfRange},    // weight < 1
		{"B", 0, 1, 17, ErrOutOfRange},   // weight > 16
		{"A", 99, 0, 0, ErrOutOfRange},   // bad range wins over duplicate name
		{"A", 1, 1, 1, ErrNameExists},    // duplicate name
		{"A", 99, 99, 99, ErrOutOfRange}, // bad range wins over duplicate
	}
	for _, c := range cases {
		_, err := tb.AddBackend(c.name, c.offset, c.skip, c.weight)
		if !errors.Is(err, c.want) {
			t.Errorf("AddBackend(%q, %d, %d, %d) err = %v, want %v",
				c.name, c.offset, c.skip, c.weight, err, c.want)
		}
	}
}

// TestTooManyBackends checks the backend-count cap of M, reported only after
// the other rejection reasons.
func TestTooManyBackends(t *testing.T) {
	tb := mustNew(t, 2, 2)
	mustAdd(t, tb, "A", 0, 1, 1)
	mustAdd(t, tb, "B", 1, 1, 1)
	if _, err := tb.AddBackend("C", 0, 1, 1); !errors.Is(err, ErrTooManyBackends) {
		t.Fatalf("AddBackend(C) err = %v, want ErrTooManyBackends", err)
	}
	// Existing name still wins over the count cap.
	if _, err := tb.AddBackend("A", 0, 1, 1); !errors.Is(err, ErrNameExists) {
		t.Fatalf("AddBackend(A) err = %v, want ErrNameExists", err)
	}
	// Bad ranges still win over the count cap.
	if _, err := tb.AddBackend("C", 0, 0, 1); !errors.Is(err, ErrOutOfRange) {
		t.Fatalf("AddBackend(C, bad skip) err = %v, want ErrOutOfRange", err)
	}
	// After a removal there is room again.
	mustRemove(t, tb, "A")
	mustAdd(t, tb, "C", 0, 1, 1)
}

// TestRemoveBackendNotFound reports unknown names and changes nothing.
func TestRemoveBackendNotFound(t *testing.T) {
	tb := mustNew(t, 7, 1)
	if _, err := tb.RemoveBackend("ghost"); !errors.Is(err, ErrBackendNotFound) {
		t.Fatalf("RemoveBackend on empty set err = %v, want ErrBackendNotFound", err)
	}
	mustAdd(t, tb, "A", 0, 1, 1)
	if _, err := tb.RemoveBackend("ghost"); !errors.Is(err, ErrBackendNotFound) {
		t.Fatalf("RemoveBackend(ghost) err = %v, want ErrBackendNotFound", err)
	}
	wantTable(t, tb, []string{"A", "A", "A", "A", "A", "A", "A"})
}

// TestRejectedOpsKeepState verifies that rejected operations leave both cur
// and the backend set untouched.
func TestRejectedOpsKeepState(t *testing.T) {
	tb := mustNew(t, 7, 1)
	mustAdd(t, tb, "A", 3, 4, 1)
	mustAdd(t, tb, "B", 0, 2, 2)
	before := tb.Table()
	pendingBefore := tb.Pending()

	rejects := []error{
		ErrEmptyName, ErrOutOfRange, ErrNameExists,
	}
	for _, want := range rejects {
		var err error
		switch want {
		case ErrEmptyName:
			_, err = tb.AddBackend("", 0, 1, 1)
		case ErrOutOfRange:
			_, err = tb.AddBackend("C", 0, 0, 1)
		case ErrNameExists:
			_, err = tb.AddBackend("A", 1, 1, 1)
		}
		if !errors.Is(err, want) {
			t.Fatalf("rejected op err = %v, want %v", err, want)
		}
	}
	if _, err := tb.RemoveBackend("ghost"); !errors.Is(err, ErrBackendNotFound) {
		t.Fatalf("RemoveBackend(ghost) err = %v, want ErrBackendNotFound", err)
	}

	wantTable(t, tb, before)
	wantPending(t, tb, pendingBefore)
	// The set is unchanged: A and B are still there, C is not.
	if _, err := tb.RemoveBackend("C"); !errors.Is(err, ErrBackendNotFound) {
		t.Fatalf("RemoveBackend(C) err = %v, want ErrBackendNotFound", err)
	}
	mustRemove(t, tb, "A")
	mustRemove(t, tb, "B")
	wantTable(t, tb, make([]string, 7))
}

// TestExampleOneShot replays the M=7, Lim=7 example: A(3,4,1), B(0,2,2),
// C(3,1,1) registered in order, then C removed.
func TestExampleOneShot(t *testing.T) {
	tb := mustNew(t, 7, 7)
	if n := mustAdd(t, tb, "A", 3, 4, 1); n != 7 {
		t.Fatalf("add A changed %d slots, want 7", n)
	}
	wantTable(t, tb, []string{"A", "A", "A", "A", "A", "A", "A"})
	if n := mustAdd(t, tb, "B", 0, 2, 2); n != 4 {
		t.Fatalf("add B changed %d slots, want 4", n)
	}
	wantTable(t, tb, []string{"B", "B", "B", "A", "A", "A", "B"})
	if n := mustAdd(t, tb, "C", 3, 1, 1); n != 3 {
		t.Fatalf("add C changed %d slots, want 3", n)
	}
	wantTable(t, tb, []string{"B", "A", "B", "A", "C", "B", "B"})
	wantPending(t, tb, 0)
	if n := mustRemove(t, tb, "C"); n != 3 {
		t.Fatalf("remove C changed %d slots, want 3", n)
	}
	wantTable(t, tb, []string{"B", "B", "B", "A", "A", "A", "B"})
	wantPending(t, tb, 0)
}

// TestExampleStepwise replays the same backend set with Lim=1: mandatory
// slots ignore Lim, optional slots migrate one at a time in ascending order.
func TestExampleStepwise(t *testing.T) {
	tb := mustNew(t, 7, 1)
	mustAdd(t, tb, "A", 3, 4, 1)
	mustAdd(t, tb, "B", 0, 2, 2)
	mustAdd(t, tb, "C", 3, 1, 1)
	drain(t, tb)
	wantTable(t, tb, []string{"B", "A", "B", "A", "C", "B", "B"})

	// Removing C: slot 4 is mandatory (owner gone), then exactly one
	// optional slot migrates: slot 1 (lowest differing slot).
	if n := mustRemove(t, tb, "C"); n != 2 {
		t.Fatalf("remove C changed %d slots, want 2", n)
	}
	wantTable(t, tb, []string{"B", "B", "B", "A", "A", "B", "B"})
	wantPending(t, tb, 1)

	if n := tb.Step(); n != 1 {
		t.Fatalf("Step changed %d slots, want 1", n)
	}
	wantTable(t, tb, []string{"B", "B", "B", "A", "A", "A", "B"})
	wantPending(t, tb, 0)
	if n := tb.Step(); n != 0 {
		t.Fatalf("Step on converged table changed %d slots, want 0", n)
	}
}

// TestWeightRoundTruncatedAtFull: a backend's round is cut short when the
// table becomes full mid-round. M=3, A(0,1,2) takes slots 0,1; B(1,1,2)
// skips slot 1, takes slot 2, and its second take never happens.
func TestWeightRoundTruncatedAtFull(t *testing.T) {
	tb := mustNew(t, 3, 3)
	mustAdd(t, tb, "A", 0, 1, 2)
	mustAdd(t, tb, "B", 1, 1, 2)
	wantTable(t, tb, []string{"A", "A", "B"})
	wantPending(t, tb, 0)
}

// TestLateBackendSkippedWhenFull: once the table is full, the remaining
// backends in the round fill nothing. M=2, A(0,1,2) fills both slots in its
// first round, so B(1,1,1) owns no slot at all.
func TestLateBackendSkippedWhenFull(t *testing.T) {
	tb := mustNew(t, 2, 2)
	if n := mustAdd(t, tb, "A", 0, 1, 2); n != 2 {
		t.Fatalf("add A changed %d slots, want 2", n)
	}
	if n := mustAdd(t, tb, "B", 1, 1, 1); n != 0 {
		t.Fatalf("add B changed %d slots, want 0", n)
	}
	wantTable(t, tb, []string{"A", "A"})
	wantPending(t, tb, 0)
	// Removing A hands the whole table to B.
	if n := mustRemove(t, tb, "A"); n != 2 {
		t.Fatalf("remove A changed %d slots, want 2", n)
	}
	wantTable(t, tb, []string{"B", "B"})
}

// TestPointerPersistsAcrossRounds: pointers keep their position between
// rounds instead of restarting at zero. M=5, A(0,1,1), B(1,1,1) interleave
// as A,B,A,B,A over three rounds.
func TestPointerPersistsAcrossRounds(t *testing.T) {
	tb := mustNew(t, 5, 5)
	mustAdd(t, tb, "A", 0, 1, 1)
	mustAdd(t, tb, "B", 1, 1, 1)
	wantTable(t, tb, []string{"A", "B", "A", "B", "A"})
	wantPending(t, tb, 0)
}

// TestSkipOccupiedSlots: a permutation landing on an occupied slot advances
// past it. M=3, A(1,1,1) takes slot 1 first; B(1,1,1) starts at slot 1,
// skips it, and takes slot 2.
func TestSkipOccupiedSlots(t *testing.T) {
	tb := mustNew(t, 3, 3)
	mustAdd(t, tb, "A", 1, 1, 1)
	mustAdd(t, tb, "B", 1, 1, 1)
	wantTable(t, tb, []string{"A", "A", "B"})
	wantPending(t, tb, 0)
}

// TestWeightFillsConsecutiveSlots: a backend with weight 3 occupies three
// slots within its own round before the next backend gets a turn.
func TestWeightFillsConsecutiveSlots(t *testing.T) {
	tb := mustNew(t, 7, 7)
	mustAdd(t, tb, "A", 0, 1, 3)
	mustAdd(t, tb, "B", 1, 1, 1)
	// Round 1: A takes 0,1,2; B skips 1,2 and takes 3.
	// Round 2: A skips 3, takes 4,5,6; table full, B fills nothing more.
	wantTable(t, tb, []string{"A", "A", "A", "B", "A", "A", "A"})
	wantPending(t, tb, 0)
}

// TestNameOrderIndependent: T* depends only on the backend set, not on the
// registration order.
func TestNameOrderIndependent(t *testing.T) {
	t1 := mustNew(t, 7, 1)
	t2 := mustNew(t, 7, 1)
	mustAdd(t, t1, "A", 3, 4, 1)
	mustAdd(t, t1, "B", 0, 2, 2)
	mustAdd(t, t1, "C", 3, 1, 1)
	mustAdd(t, t2, "C", 3, 1, 1)
	mustAdd(t, t2, "A", 3, 4, 1)
	mustAdd(t, t2, "B", 0, 2, 2)
	drain(t, t1)
	drain(t, t2)
	want := []string{"B", "A", "B", "A", "C", "B", "B"}
	wantTable(t, t1, want)
	wantTable(t, t2, want)
}

// TestAddFromEmptyRemoveToEmpty: registering into an empty table and
// removing down to an empty table both change exactly M slots.
func TestAddFromEmptyRemoveToEmpty(t *testing.T) {
	tb := mustNew(t, 7, 1)
	if n := mustAdd(t, tb, "A", 2, 3, 1); n != 7 {
		t.Fatalf("add into empty table changed %d slots, want 7", n)
	}
	mustAdd(t, tb, "B", 0, 1, 2)
	drain(t, tb)
	mustRemove(t, tb, "A")
	drain(t, tb)
	// Removing the last backend empties the whole table at once,
	// regardless of Lim.
	if n := mustRemove(t, tb, "B"); n != 7 {
		t.Fatalf("remove last backend changed %d slots, want 7", n)
	}
	wantTable(t, tb, make([]string, 7))
	wantPending(t, tb, 0)
	if n := tb.Step(); n != 0 {
		t.Fatalf("Step on empty set changed %d slots, want 0", n)
	}
}

// TestSingleBackendFillsAll: one backend with weight 1 owns every slot.
func TestSingleBackendFillsAll(t *testing.T) {
	tb := mustNew(t, 13, 13)
	if n := mustAdd(t, tb, "solo", 5, 7, 1); n != 13 {
		t.Fatalf("add solo changed %d slots, want 13", n)
	}
	for i, owner := range tb.Table() {
		if owner != "solo" {
			t.Fatalf("slot %d owned by %q, want solo", i, owner)
		}
	}
	wantPending(t, tb, 0)
}

// TestM2WithMBackends: with M=2 and exactly M backends, each backend owns
// exactly one slot.
func TestM2WithMBackends(t *testing.T) {
	tb := mustNew(t, 2, 2)
	mustAdd(t, tb, "A", 0, 1, 1)
	if n := mustAdd(t, tb, "B", 1, 1, 1); n != 1 {
		t.Fatalf("add B changed %d slots, want 1", n)
	}
	wantTable(t, tb, []string{"A", "B"})
	wantPending(t, tb, 0)
}

// TestStepDecreasesPendingExactly: each Step lowers Pending by exactly
// min(Lim, Pending) and converges within ceil(M/Lim) steps.
func TestStepDecreasesPendingExactly(t *testing.T) {
	for _, cfg := range [][2]int{{7, 1}, {7, 2}, {7, 3}, {7, 7}, {13, 4}} {
		m, lim := cfg[0], cfg[1]
		tb := mustNew(t, m, lim)
		mustAdd(t, tb, "A", 1, 2, 1)
		mustAdd(t, tb, "B", 0, 3, 2)
		mustAdd(t, tb, "C", 4, 1, 1)
		maxSteps := (m + lim - 1) / lim
		steps := 0
		for {
			p0 := tb.Pending()
			if p0 == 0 {
				break
			}
			n := tb.Step()
			want := min(lim, p0)
			if n != want {
				t.Fatalf("M=%d Lim=%d: Step changed %d, want min(%d, %d) = %d", m, lim, n, lim, p0, want)
			}
			if p1 := tb.Pending(); p0-p1 != want {
				t.Fatalf("M=%d Lim=%d: Pending dropped by %d, want %d", m, lim, p0-p1, want)
			}
			steps++
			if steps > maxSteps {
				t.Fatalf("M=%d Lim=%d: not converged after %d steps", m, lim, maxSteps)
			}
		}
		if steps > maxSteps {
			t.Fatalf("M=%d Lim=%d: took %d steps, want <= %d", m, lim, steps, maxSteps)
		}
	}
}

// TestLimEqualsMOneShot: with Lim=M every registration or removal migrates
// the whole table in a single operation.
func TestLimEqualsMOneShot(t *testing.T) {
	tb := mustNew(t, 11, 11)
	mustAdd(t, tb, "A", 0, 1, 1)
	wantPending(t, tb, 0)
	mustAdd(t, tb, "B", 3, 2, 3)
	wantPending(t, tb, 0)
	mustRemove(t, tb, "A")
	wantPending(t, tb, 0)
	mustAdd(t, tb, "C", 5, 4, 2)
	wantPending(t, tb, 0)
}

// TestLookup: Lookup(h) returns cur[h mod M] and errors on an empty set.
func TestLookup(t *testing.T) {
	tb := mustNew(t, 7, 7)
	if _, err := tb.Lookup(0); !errors.Is(err, ErrNoBackends) {
		t.Fatalf("Lookup on empty set err = %v, want ErrNoBackends", err)
	}
	mustAdd(t, tb, "A", 3, 4, 1)
	mustAdd(t, tb, "B", 0, 2, 2)
	mustAdd(t, tb, "C", 3, 1, 1)
	cur := tb.Table()
	for _, h := range []uint64{0, 1, 6, 7, 8, 100, 1 << 60, ^uint64(0)} {
		got, err := tb.Lookup(h)
		if err != nil {
			t.Fatalf("Lookup(%d) failed: %v", h, err)
		}
		if want := cur[h%7]; got != want {
			t.Fatalf("Lookup(%d) = %q, want %q", h, got, want)
		}
	}
}

// TestTableReturnsCopy: mutating the returned slice does not affect cur.
func TestTableReturnsCopy(t *testing.T) {
	tb := mustNew(t, 7, 7)
	mustAdd(t, tb, "A", 0, 1, 1)
	tab := tb.Table()
	tab[0] = "hacked"
	if got := tb.Table()[0]; got != "A" {
		t.Fatalf("internal table mutated via copy: slot 0 = %q", got)
	}
}

// TestConcurrency hammers the table from many goroutines; with -race this
// checks mutual exclusion, and the final invariants check that the result
// equals some serial execution: a non-empty set implies every slot is owned
// by a live backend, and draining converges to T*.
func TestConcurrency(t *testing.T) {
	tb := mustNew(t, 97, 5)
	names := []string{"a", "b", "c", "d", "e", "f", "g", "h"}
	var wg sync.WaitGroup
	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				name := names[(w*3+i)%len(names)]
				switch i % 4 {
				case 0:
					tb.AddBackend(name, (i*7+w)%97, 1+(i*11+w)%96, 1+(i+w)%16)
				case 1:
					tb.RemoveBackend(name)
				case 2:
					tb.Step()
				case 3:
					tb.Pending()
					tb.Table()
					tb.Lookup(uint64(i*13 + w))
				}
			}
		}(w)
	}
	wg.Wait()

	// Whatever serial order happened, registering every name and draining
	// must converge to a fully owned table with Pending 0.
	for i, name := range names {
		tb.AddBackend(name, i*3%97, 1+(i*5)%96, 1+i%16)
	}
	drain(t, tb)
	owners := make(map[string]bool)
	for _, name := range names {
		owners[name] = true
	}
	for s, owner := range tb.Table() {
		if !owners[owner] {
			t.Fatalf("slot %d owned by %q, not in backend set", s, owner)
		}
	}
}

package epaxos

import (
	"errors"
	"reflect"
	"sync"
	"testing"
)

func assertErrCode(t *testing.T, err error, code ErrCode) {
	t.Helper()
	var ce *Error
	if !errors.As(err, &ce) {
		t.Fatalf("expected *Error with code %v, got %v", code, err)
	}
	if ce.Code != code {
		t.Fatalf("expected code %v, got %v (%s)", code, ce.Code, ce.Msg)
	}
}

func mustCommit(t *testing.T, s *Scheduler, inst Instance, seq int, deps ...Instance) {
	t.Helper()
	if err := s.Commit(inst, seq, deps); err != nil {
		t.Fatalf("Commit(%v, %d, %v) failed: %v", inst, seq, deps, err)
	}
}

func assertOrder(t *testing.T, got, want []Instance) {
	t.Helper()
	if len(got) == 0 && len(want) == 0 {
		return
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("order mismatch:\n got: %v\nwant: %v", got, want)
	}
}

func TestNewInvalidConfig(t *testing.T) {
	for _, cfg := range [][2]int{{0, 1}, {1, 0}, {-3, 5}, {2, -1}} {
		if _, err := New(cfg[0], cfg[1]); err == nil {
			t.Fatalf("New(%d, %d) should fail", cfg[0], cfg[1])
		} else {
			assertErrCode(t, err, ErrInvalidConfig)
		}
	}
	if _, err := New(3, 10); err != nil {
		t.Fatalf("New(3, 10) failed: %v", err)
	}
}

// Two mutually dependent instances execute in seq order: B (seq 3) then A (seq 5).
func TestMutualDependencyOrderedBySeq(t *testing.T) {
	s, _ := New(2, 10)
	a := Instance{0, 1}
	b := Instance{1, 1}
	mustCommit(t, s, a, 5, b)
	mustCommit(t, s, b, 3, a)
	assertOrder(t, s.Execute(), []Instance{b, a})
	assertOrder(t, s.Pending(), nil)
}

// A dependent with a smaller seq still executes after its dependency.
func TestDependentWithSmallerSeqExecutesAfterDep(t *testing.T) {
	s, _ := New(2, 10)
	a := Instance{0, 1}
	b := Instance{1, 1}
	mustCommit(t, s, a, 1, b) // A depends on B but has smaller seq
	mustCommit(t, s, b, 9)
	assertOrder(t, s.Execute(), []Instance{b, a})
}

// A dep on an uncommitted instance blocks the instance and, transitively,
// its dependents; committing the missing instance unblocks the next Execute.
func TestBlockedUntilDepCommitted(t *testing.T) {
	s, _ := New(2, 10)
	c := Instance{0, 1}
	b := Instance{1, 1}
	a := Instance{1, 2}
	mustCommit(t, s, c, 1, b) // C -> B -> A, A not committed
	mustCommit(t, s, b, 2, a)
	assertOrder(t, s.Execute(), nil)
	assertOrder(t, s.Pending(), []Instance{c, b})

	mustCommit(t, s, a, 3)
	assertOrder(t, s.Execute(), []Instance{a, b, c})
	assertOrder(t, s.Pending(), nil)
}

// Among ready components the one with the smallest first member (by seq, R,
// I) is emitted first, even if the other component contains smaller later
// members.
func TestReadyComponentsPickedByFirstMember(t *testing.T) {
	s, _ := New(2, 10)
	// Component A: cycle of seqs 5 and 9 -> first member seq 5.
	a1 := Instance{0, 1}
	a2 := Instance{0, 2}
	// Component B: cycle of seqs 6 and 7 -> first member seq 6.
	b1 := Instance{1, 1}
	b2 := Instance{1, 2}
	mustCommit(t, s, a1, 5, a2)
	mustCommit(t, s, a2, 9, a1)
	mustCommit(t, s, b1, 6, b2)
	mustCommit(t, s, b2, 7, b1)
	assertOrder(t, s.Execute(), []Instance{a1, a2, b1, b2})
}

// Inside one component, equal seqs are ordered by replica then slot.
func TestSameSeqTieBreakByReplicaThenSlot(t *testing.T) {
	s, _ := New(3, 10)
	x := Instance{1, 2}
	y := Instance{0, 5}
	z := Instance{1, 1}
	mustCommit(t, s, x, 4, y)
	mustCommit(t, s, y, 4, z)
	mustCommit(t, s, z, 4, x)
	assertOrder(t, s.Execute(), []Instance{y, z, x})
}

// A dep on an already-executed instance is satisfied and forms no edge.
func TestDepOnExecutedIsSatisfied(t *testing.T) {
	s, _ := New(2, 10)
	a := Instance{0, 1}
	b := Instance{0, 2}
	mustCommit(t, s, a, 1)
	assertOrder(t, s.Execute(), []Instance{a})
	mustCommit(t, s, b, 1, a)
	assertOrder(t, s.Execute(), []Instance{b})
	assertOrder(t, s.Pending(), nil)
}

// A 3-instance cycle plus one external dependent: the cycle's SCC is emitted
// first (sorted by seq), then the dependent.
func TestThreeCycleWithDependent(t *testing.T) {
	s, _ := New(2, 10)
	a := Instance{0, 1}
	b := Instance{1, 1}
	c := Instance{0, 2}
	d := Instance{1, 2}
	mustCommit(t, s, a, 3, b)
	mustCommit(t, s, b, 1, c)
	mustCommit(t, s, c, 2, a)
	mustCommit(t, s, d, 1, a)
	assertOrder(t, s.Execute(), []Instance{b, c, a, d})
}

// Re-committing with identical seq and dep set is a no-op (even after
// execution); differing seq or dep set is a conflict.
func TestDuplicateCommitNoopAndConflict(t *testing.T) {
	s, _ := New(2, 10)
	a := Instance{0, 1}
	b := Instance{1, 1}
	c := Instance{1, 2}
	mustCommit(t, s, a, 2, b, c)
	mustCommit(t, s, a, 2, c, b) // same set, different order: no-op
	if err := s.Commit(a, 3, []Instance{b, c}); err == nil {
		t.Fatal("expected conflict on different seq")
	} else {
		assertErrCode(t, err, ErrConflictingCommit)
	}
	if err := s.Commit(a, 2, []Instance{b}); err == nil {
		t.Fatal("expected conflict on different dep set")
	} else {
		assertErrCode(t, err, ErrConflictingCommit)
	}
	mustCommit(t, s, b, 1)
	mustCommit(t, s, c, 1)
	assertOrder(t, s.Pending(), []Instance{a, b, c})
	assertOrder(t, s.Execute(), []Instance{b, c, a})
	mustCommit(t, s, a, 2, b, c) // identical re-commit after execution: no-op
	assertOrder(t, s.Pending(), nil)
}

// Commit reports the first error in the documented precedence order.
func TestCommitErrorPrecedence(t *testing.T) {
	s, _ := New(2, 10)
	a := Instance{0, 1}
	b := Instance{1, 1}

	// Invalid instance beats invalid seq.
	assertErrCode(t, s.Commit(Instance{2, 1}, 0, nil), ErrInvalidInstance)
	assertErrCode(t, s.Commit(Instance{0, 0}, 0, nil), ErrInvalidInstance)
	// Invalid dep beats invalid seq.
	assertErrCode(t, s.Commit(a, 0, []Instance{{1, 0}}), ErrInvalidInstance)
	// Invalid seq beats self-dependency.
	assertErrCode(t, s.Commit(a, 0, []Instance{a}), ErrInvalidSeq)
	// Self-dependency beats duplicate dependency.
	assertErrCode(t, s.Commit(a, 1, []Instance{a, b, b}), ErrSelfDependency)
	// Duplicate dependency.
	assertErrCode(t, s.Commit(a, 1, []Instance{b, b}), ErrDuplicateDependency)
	// Duplicate-dependency check beats conflict with existing record.
	mustCommit(t, s, a, 1, b)
	assertErrCode(t, s.Commit(a, 1, []Instance{b, b}), ErrDuplicateDependency)
	// Conflict beats capacity.
	s2, _ := New(2, 1)
	mustCommit(t, s2, a, 1)
	assertErrCode(t, s2.Commit(a, 2, nil), ErrConflictingCommit)
}

// A rejected commit changes no state.
func TestRejectedCommitKeepsState(t *testing.T) {
	s, _ := New(2, 2)
	a := Instance{0, 1}
	b := Instance{1, 1}
	mustCommit(t, s, a, 1)
	before := s.Pending()

	rejects := []error{
		s.Commit(Instance{5, 1}, 1, nil),
		s.Commit(Instance{0, 0}, 1, nil),
		s.Commit(b, 0, nil),
		s.Commit(b, 1, []Instance{b}),
		s.Commit(b, 1, []Instance{a, a}),
		s.Commit(a, 2, nil), // conflict with existing record
	}
	// Capacity check needs a full scheduler.
	s3, _ := New(2, 1)
	mustCommit(t, s3, a, 1)
	assertErrCode(t, s3.Commit(b, 1, nil), ErrCapacityExceeded)
	assertOrder(t, s3.Pending(), []Instance{a})
	assertOrder(t, s3.Execute(), []Instance{a})
	mustCommit(t, s3, b, 1) // capacity freed after execution
	assertOrder(t, s3.Pending(), []Instance{b})

	for _, err := range rejects {
		if err == nil {
			t.Fatal("expected rejection")
		}
	}
	assertOrder(t, s.Pending(), before)
	assertOrder(t, s.Execute(), []Instance{a})
}

// Pending lists committed-but-unexecuted instances sorted by (R, I).
func TestPendingSorted(t *testing.T) {
	s, _ := New(3, 10)
	mustCommit(t, s, Instance{2, 3}, 1)
	mustCommit(t, s, Instance{0, 4}, 1)
	mustCommit(t, s, Instance{0, 1}, 1)
	mustCommit(t, s, Instance{2, 1}, 1)
	assertOrder(t, s.Pending(), []Instance{{0, 1}, {0, 4}, {2, 1}, {2, 3}})
}

// The slice returned by Execute does not alias internal state.
func TestExecuteResultNotAliased(t *testing.T) {
	s, _ := New(2, 10)
	a := Instance{0, 1}
	mustCommit(t, s, a, 1)
	got := s.Execute()
	assertOrder(t, got, []Instance{a})
	got[0] = Instance{9, 9}
	assertOrder(t, s.Pending(), nil)
	mustCommit(t, s, a, 1) // no-op re-commit still sees the original record
}

// Concurrent calls are safe and equivalent to some serial order: every
// committed instance is executed exactly once across all Execute results.
func TestConcurrentSmoke(t *testing.T) {
	const n = 4
	const perReplica = 25
	s, _ := New(n, n*perReplica)

	var wg sync.WaitGroup
	for r := 0; r < n; r++ {
		r := r
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 1; i <= perReplica; i++ {
				if err := s.Commit(Instance{r, i}, i, nil); err != nil {
					t.Errorf("commit failed: %v", err)
				}
			}
		}()
	}
	wg.Wait()

	seen := make(map[Instance]int)
	var mu sync.Mutex
	for k := 0; k < 8; k++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for _, inst := range s.Execute() {
				mu.Lock()
				seen[inst]++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if len(seen) != n*perReplica {
		t.Fatalf("expected %d executed instances, got %d", n*perReplica, len(seen))
	}
	for inst, cnt := range seen {
		if cnt != 1 {
			t.Fatalf("instance %v executed %d times", inst, cnt)
		}
	}
	assertOrder(t, s.Pending(), nil)
}

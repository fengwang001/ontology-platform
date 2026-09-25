package ra

import (
	"errors"
	"fmt"
	"math/rand"
	"sort"
	"testing"

	"ontology/ord"
)

func ok(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
func enterables(s *System) []int {
	got := []int{}
	for pid := 0; pid < s.n; pid++ {
		if e, _ := s.Enterable(pid); e {
			got = append(got, pid)
		}
	}
	return got
}

// drainAll: exactly one enterable per step (pins inv.1 mutex, inv.3 no-deadlock).
func drainAll(t *testing.T, s *System, k int) []int {
	t.Helper()
	order := make([]int, 0, k)
	for range k {
		cur := enterables(s)
		if len(cur) != 1 {
			t.Fatalf("enterable set = %v, want exactly one", cur)
		}
		order = append(order, cur[0])
		ok(t, s.Leave(cur[0]))
	}
	return order
}
func TestJudge_TableDriven(t *testing.T) {
	P1, P2, P3 := ord.Ticket{TS: 5, PID: 1}, ord.Ticket{TS: 3, PID: 2}, ord.Ticket{TS: 5, PID: 3}
	for i, c := range []struct {
		interested bool
		recv, send ord.Ticket
		want       Verdict
	}{
		{false, P1, P2, Grant}, {false, P3, P2, Grant}, {true, P2, P1, Defer},
		{false, P3, P1, Grant}, {true, P2, P3, Defer}, {true, P1, P3, Defer},
		{true, P3, P1, Grant},
	} {
		if got := Judge(c.interested, c.recv, c.send); got != c.want {
			t.Fatalf("row %d: Judge = %v, want %v", i+1, got, c.want)
		}
	}
}
func TestEntryOrder_MatchesTotalOrder(t *testing.T) {
	for seed := int64(0); seed < 40; seed++ {
		rng := rand.New(rand.NewSource(seed))
		n := 2 + rng.Intn(12)
		s, tss, pids := NewSystem(n), make([]int, n), rng.Perm(n)
		for _, pid := range pids {
			tss[pid] = rng.Intn(20)
			if err := s.Request(pid, tss[pid]); err != nil {
				t.Fatalf("seed %d: %v", seed, err)
			}
		}
		s.Resolve()
		s.Resolve() // idempotent: re-settling must not double count OKs
		sort.Slice(pids, func(a, b int) bool {
			return ord.Less(ord.Ticket{TS: tss[pids[a]], PID: pids[a]}, ord.Ticket{TS: tss[pids[b]], PID: pids[b]})
		})
		if got := drainAll(t, s, n); fmt.Sprint(got) != fmt.Sprint(pids) {
			t.Fatalf("seed %d: order %v, want %v", seed, got, pids)
		}
		// Second round on the same System: stale OK bits must not survive Leave.
		for pid := 0; pid < n; pid++ {
			ok(t, s.Request(pid, int(seed)+pid))
		}
		s.Resolve()
		drainAll(t, s, n)
	}
}
func TestMutex_AtMostOneEnterable(t *testing.T) {
	s := NewSystem(6)
	for pid := 0; pid < 6; pid++ {
		ok(t, s.Request(pid, pid%3))
	}
	s.Resolve()
	drainAll(t, s, 6)
}
func TestNoDeadlock_AlwaysEnterable(t *testing.T) {
	for seed := int64(100); seed < 130; seed++ {
		rng := rand.New(rand.NewSource(seed))
		n, k := 1+rng.Intn(10), 0
		s := NewSystem(n)
		for _, pid := range rng.Perm(n) { // non-empty partial interested set
			if k > 0 && rng.Intn(2) == 0 {
				continue
			}
			ok(t, s.Request(pid, rng.Intn(8)))
			k++
		}
		s.Resolve()
		drainAll(t, s, k)
	}
}
func TestRejectedOps_LeaveStateUntouched(t *testing.T) {
	s := NewSystem(3)
	_, badEnter := s.Enterable(9)
	errs := []error{s.Request(-1, 0), s.Request(3, 0), s.Request(0, -5), s.Leave(1), badEnter}
	wants := []error{ErrPIDOutOfRange, ErrPIDOutOfRange, ErrNegativeTS, ErrNotInterested, ErrPIDOutOfRange}
	for i := range errs {
		if !errors.Is(errs[i], wants[i]) {
			t.Errorf("case %d: got %v, want %v", i, errs[i], wants[i])
		}
	}
	if ErrPIDOutOfRange == ErrNegativeTS || ErrNegativeTS == ErrNotInterested ||
		ErrNotInterested == ErrDuplicateRequest || ErrDuplicateRequest == ErrPIDOutOfRange {
		t.Fatal("the four sentinel errors are not distinct")
	}
	if err := s.Request(0, 7); err != nil { // rejected calls left no trace
		t.Fatalf("group unusable after rejections: %v", err)
	}
	if err := s.Request(0, 8); !errors.Is(err, ErrDuplicateRequest) {
		t.Fatalf("duplicate request: got %v", err)
	} else if err := s.Request(1, 7); err != nil { // first request still intact
		t.Fatalf("pending request changed by rejected duplicate: %v", err)
	}
	s.Resolve()
	if cur := enterables(s); len(cur) != 1 || cur[0] != 0 { // (7,0)<(7,1)
		t.Fatalf("order corrupted after rejections: %v", cur)
	}
}
func TestOKCollectionCheckConstant(t *testing.T) {
	first := -1
	for _, m := range []int{100, 1000, 10000} {
		s := NewSystem(m)
		for pid := 0; pid < m; pid++ {
			ok(t, s.Request(pid, pid))
		}
		s.Resolve()
		if s.lastCheck != 1 || (first != -1 && s.lastCheck != first) {
			t.Fatalf("m=%d: lastCheck = %d, first = %d", m, s.lastCheck, first)
		}
		first = s.lastCheck // one counter comparison at every scale: O(1)
	}
}

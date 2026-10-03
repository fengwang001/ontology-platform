package cidpool

import (
	"errors"
	"reflect"
	"testing"
)

func cid(b byte) []byte { return []byte{b} }

func tok(b byte) [16]byte {
	var t [16]byte
	for i := range t {
		t[i] = b
	}
	return t
}

func mustPool(t *testing.T, limit int) *Pool {
	t.Helper()
	p, err := New(limit, cid(0x00), tok(0x00))
	if err != nil {
		t.Fatalf("New(%d): %v", limit, err)
	}
	return p
}

func errCode(err error) ErrCode {
	var e *Error
	if errors.As(err, &e) {
		return e.Code
	}
	return 0
}

func wantErr(t *testing.T, what string, err error, code ErrCode) {
	t.Helper()
	if errCode(err) != code {
		t.Fatalf("%s: got error %v, want code %v", what, err, code)
	}
}

func wantOK(t *testing.T, what string, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("%s: unexpected error %v", what, err)
	}
}

func wantQueue(t *testing.T, what string, p *Pool, want []uint64) {
	t.Helper()
	got := p.TakeRetires()
	if len(want) == 0 && len(got) == 0 {
		return
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%s: retire queue = %v, want %v", what, got, want)
	}
}

func wantPath(t *testing.T, what string, p *Pool, pid uint64, seq uint64, st PathState) {
	t.Helper()
	gotSeq, gotSt := p.PathSeq(pid)
	if gotSt != st || (st == PathActive && gotSeq != seq) {
		t.Fatalf("%s: PathSeq(%d) = (%d, %v), want (%d, %v)", what, pid, gotSeq, gotSt, seq, st)
	}
}

func wantActive(t *testing.T, what string, p *Pool, n int) {
	t.Helper()
	if got := p.ActiveCount(); got != n {
		t.Fatalf("%s: ActiveCount = %d, want %d", what, got, n)
	}
}

// The worked example from the specification: limit enforcement,
// retire_prior_to batch retirement, path reassignment, duplicate
// replay, and conflicting reissue.
func TestSpecExampleLimit(t *testing.T) {
	p := mustPool(t, 3)

	wantOK(t, "NewPath(1)", p.NewPath(1))
	wantPath(t, "path1 takes seq0", p, 1, 0, PathActive)

	wantOK(t, "OnNew(1,0)", p.OnNew(1, 0, cid(0x01), tok(0x01)))
	wantOK(t, "OnNew(2,0)", p.OnNew(2, 0, cid(0x02), tok(0x02)))
	wantActive(t, "three active", p, 3)

	// A fourth active entry with no retirement exceeds the limit.
	wantErr(t, "OnNew(3,0) limit", p.OnNew(3, 0, cid(0x03), tok(0x03)), ErrLimit)
	wantActive(t, "state unchanged after ErrLimit", p, 3)
	wantPath(t, "path1 still on seq0", p, 1, 0, PathActive)
	wantQueue(t, "nothing retired", p, nil)

	// retire_prior_to=2 retires seq0 and seq1, making room.
	wantOK(t, "OnNew(3,2)", p.OnNew(3, 2, cid(0x03), tok(0x03)))
	wantActive(t, "active {2,3}", p, 2)
	wantQueue(t, "retire queue [0 1]", p, []uint64{0, 1})
	wantPath(t, "path1 moved to seq2", p, 1, 2, PathActive)

	// Exact replay of a retired frame is a duplicate: success, no change.
	wantOK(t, "duplicate OnNew(1,0)", p.OnNew(1, 0, cid(0x01), tok(0x01)))
	wantActive(t, "duplicate changed nothing", p, 2)
	wantQueue(t, "duplicate enqueued nothing", p, nil)
	wantPath(t, "path1 unchanged", p, 1, 2, PathActive)

	// Same sequence with different cid/token is a violation.
	wantErr(t, "conflicting OnNew(1,0)", p.OnNew(1, 0, cid(0x09), tok(0x09)), ErrViolation)
}

// Late-arriving sequence numbers below the effective R retire on
// arrival and never count toward the limit.
func TestSpecExampleLateArrival(t *testing.T) {
	p := mustPool(t, 2)

	// rpt=4 retires seq0; seq5 survives.
	wantOK(t, "OnNew(5,4)", p.OnNew(5, 4, cid(0x05), tok(0x05)))
	wantActive(t, "only seq5 active", p, 1)
	wantQueue(t, "retire queue [0]", p, []uint64{0})

	// seq3 < R=4: retires immediately, does not count as active, and
	// must not raise ErrLimit even though the limit is small.
	wantOK(t, "late OnNew(3,0)", p.OnNew(3, 0, cid(0x03), tok(0x03)))
	wantActive(t, "seq3 not active", p, 1)
	wantQueue(t, "retire queue [3]", p, []uint64{3})

	// Replaying the same late frame is a duplicate: success, no change.
	wantOK(t, "duplicate late OnNew(3,0)", p.OnNew(3, 0, cid(0x03), tok(0x03)))
	wantActive(t, "duplicate changed nothing", p, 1)
	wantQueue(t, "duplicate enqueued nothing", p, nil)
}

// Shelved paths: a path that loses its connection ID waits until a new
// one arrives; shelved paths are served in ascending pathID order.
func TestSpecExampleShelved(t *testing.T) {
	p := mustPool(t, 2)

	wantOK(t, "NewPath(1)", p.NewPath(1))
	wantPath(t, "path1 on seq0", p, 1, 0, PathActive)

	// The only active entry is occupied: no connection ID for path2.
	wantErr(t, "NewPath(2) no cid", p.NewPath(2), ErrNoCID)
	wantPath(t, "path2 not created", p, 2, 0, PathMissing)

	// Fill the pool and give path2 an entry so both paths are active.
	wantOK(t, "OnNew(1,0)", p.OnNew(1, 0, cid(0x01), tok(0x01)))
	wantOK(t, "NewPath(2)", p.NewPath(2))
	wantPath(t, "path2 on seq1", p, 2, 1, PathActive)

	// Retire both active entries; the single survivor goes to the
	// smallest pathID, the other path stays shelved.
	wantOK(t, "OnNew(2,2)", p.OnNew(2, 2, cid(0x02), tok(0x02)))
	wantQueue(t, "retire queue [0 1]", p, []uint64{0, 1})
	wantPath(t, "path1 reassigned first", p, 1, 2, PathActive)
	wantPath(t, "path2 shelved", p, 2, 0, PathShelved)

	// The next arriving connection ID is handed to the shelved path.
	wantOK(t, "OnNew(3,0)", p.OnNew(3, 0, cid(0x03), tok(0x03)))
	wantPath(t, "path2 gets seq3", p, 2, 3, PathActive)
}

// rpt == seq keeps the new entry; rpt == seq+1 is an encoding error.
func TestRetirePriorToBoundary(t *testing.T) {
	p := mustPool(t, 2)

	// rpt == seq: legal, retires everything below, keeps the new entry.
	wantOK(t, "OnNew(1,1)", p.OnNew(1, 1, cid(0x01), tok(0x01)))
	wantActive(t, "only seq1 active", p, 1)
	wantQueue(t, "retire queue [0]", p, []uint64{0})
	if !p.IsReset(tok(0x01)) {
		t.Fatal("seq1 token should be active")
	}
	if p.IsReset(tok(0x00)) {
		t.Fatal("retired seq0 token must be invalid")
	}

	// rpt == seq+1: encoding error, nothing changes.
	wantErr(t, "OnNew(2,3) encoding", p.OnNew(2, 3, cid(0x02), tok(0x02)), ErrEncoding)
	wantActive(t, "state unchanged", p, 1)
	wantQueue(t, "nothing retired", p, nil)
}

// A cid belonging to a retired tombstone may not be reused under a
// different sequence number.
func TestRetiredCIDReuse(t *testing.T) {
	p := mustPool(t, 2)

	wantOK(t, "OnNew(1,1)", p.OnNew(1, 1, cid(0x01), tok(0x01)))
	wantQueue(t, "seq0 retired", p, []uint64{0})

	// cid of retired seq0 reused under seq5: violation.
	wantErr(t, "reuse retired cid", p.OnNew(5, 0, cid(0x00), tok(0x05)), ErrViolation)
	// Same cid under the same sequence with a different token: violation.
	wantErr(t, "same seq different token", p.OnNew(0, 0, cid(0x00), tok(0x09)), ErrViolation)
	// Exact replay of retired seq0: duplicate, success.
	wantOK(t, "exact duplicate", p.OnNew(0, 0, cid(0x00), tok(0x00)))
}

// A rejected frame must not advance R. Construct a rejection whose rpt
// exceeds R, then prove R stayed put via the late-arrival rule.
func TestRejectedFrameDoesNotAdvanceR(t *testing.T) {
	p := mustPool(t, 2)

	// Reach R=2 with active {5,6}.
	wantOK(t, "OnNew(5,2)", p.OnNew(5, 2, cid(0x05), tok(0x05)))
	wantOK(t, "OnNew(6,0)", p.OnNew(6, 0, cid(0x06), tok(0x06)))
	wantQueue(t, "seq0 retired", p, []uint64{0})
	wantActive(t, "active {5,6}", p, 2)

	// rpt=3 > R=2 but retires nothing (5,6 >= 3): adding seq7 would
	// make 3 active entries, so it is rejected with ErrLimit.
	wantErr(t, "OnNew(7,3) limit", p.OnNew(7, 3, cid(0x07), tok(0x07)), ErrLimit)

	// If R had advanced to 3, seq2 would retire on arrival and succeed
	// despite the full pool. R is still 2, so seq2 counts as active and
	// the frame is rejected.
	wantErr(t, "OnNew(2,0) still limited", p.OnNew(2, 0, cid(0x02), tok(0x02)), ErrLimit)
	wantActive(t, "state unchanged", p, 2)
	wantQueue(t, "nothing retired", p, nil)
}

// Local retirement: entry leaves the active set, its token dies, the
// occupying path is shelved and reassigned; R is untouched.
func TestLocalRetire(t *testing.T) {
	p := mustPool(t, 2)

	wantOK(t, "OnNew(1,0)", p.OnNew(1, 0, cid(0x01), tok(0x01)))
	wantOK(t, "NewPath(1)", p.NewPath(1))
	wantPath(t, "path1 on seq0", p, 1, 0, PathActive)

	wantErr(t, "Retire unknown", p.Retire(9), ErrArg)
	wantOK(t, "Retire(0)", p.Retire(0))
	wantQueue(t, "retire queue [0]", p, []uint64{0})
	wantPath(t, "path1 moved to seq1", p, 1, 1, PathActive)
	if p.IsReset(tok(0x00)) {
		t.Fatal("locally retired token must be invalid")
	}

	// Retiring the same sequence again is an argument error.
	wantErr(t, "Retire twice", p.Retire(0), ErrArg)

	// R was not touched: seq2 with rpt=0 stays active normally.
	wantOK(t, "OnNew(2,0)", p.OnNew(2, 0, cid(0x02), tok(0x02)))
	wantActive(t, "active {1,2}", p, 2)
}

// FreePath releases its connection ID and triggers reassignment of
// remaining shelved paths in ascending pathID order.
func TestFreePathReassign(t *testing.T) {
	p := mustPool(t, 2)

	wantOK(t, "OnNew(1,0)", p.OnNew(1, 0, cid(0x01), tok(0x01)))
	wantOK(t, "NewPath(1)", p.NewPath(1))
	wantOK(t, "NewPath(2)", p.NewPath(2))
	wantPath(t, "path1 on seq0", p, 1, 0, PathActive)
	wantPath(t, "path2 on seq1", p, 2, 1, PathActive)

	wantErr(t, "NewPath duplicate", p.NewPath(1), ErrArg)
	wantErr(t, "FreePath missing", p.FreePath(9), ErrArg)

	// Retire seq0: path1 is shelved because seq1 is still occupied.
	wantOK(t, "Retire(0)", p.Retire(0))
	wantQueue(t, "retire queue [0]", p, []uint64{0})
	wantPath(t, "path1 shelved", p, 1, 0, PathShelved)

	// Freeing path2 releases seq1, which the shelved path1 then takes.
	wantOK(t, "FreePath(2)", p.FreePath(2))
	wantPath(t, "path2 gone", p, 2, 0, PathMissing)
	wantPath(t, "path1 takes seq1", p, 1, 1, PathActive)

	// The released entry was freed, not retired.
	wantQueue(t, "no extra retirements", p, nil)
	wantActive(t, "seq1 still active", p, 1)
}

package chat

import "testing"

// The rejection precedence is:
//
//	invalid param > clock skew > not member > message not found
//		> permission denied > state (recalled > timeout > edit limit)
//
// Each test below constructs an operation that violates two adjacent
// categories and asserts that only the higher-ranked one is reported.

// param > clock: empty body AND regressed now -> invalid param.
func TestPrecedenceParamOverClock(t *testing.T) {
	c := newTestChannel(t)
	send(t, c, "alice", "m1", nil, 10)
	mustCode(t, sendErr(c, "alice", "", nil, 5), ErrInvalidParam)
	// clock not consumed by the rejection: now=10 still accepted.
	send(t, c, "alice", "m2", nil, 10)
}

// clock > not member: regressed now AND unknown sender -> clock skew.
func TestPrecedenceClockOverNotMember(t *testing.T) {
	c := newTestChannel(t)
	send(t, c, "alice", "m1", nil, 10)
	mustCode(t, sendErr(c, "ghost", "hi", nil, 5), ErrClockSkew)
}

// not member > message not found: unknown editor AND unknown seq.
func TestPrecedenceNotMemberOverNotFound(t *testing.T) {
	c := newTestChannel(t)
	send(t, c, "alice", "m1", nil, 1)
	mustCode(t, c.Edit("ghost", 99, "x", nil, 2), ErrNotMember)
	mustCode(t, c.Recall("ghost", 99, 2), ErrNotMember)
}

// message not found > permission: member edits a nonexistent message that,
// had it existed, they would not own -> message not found.
func TestPrecedenceNotFoundOverPermission(t *testing.T) {
	c := newTestChannel(t)
	joinAll(t, c, 0, "bob")
	send(t, c, "alice", "m1", nil, 1)
	mustCode(t, c.Edit("bob", 99, "x", nil, 2), ErrMessageNotFound)
	mustCode(t, c.Recall("bob", 99, 2), ErrMessageNotFound)
}

// permission > state: bob (neither author nor admin) edits a recalled,
// timed-out message -> permission denied, not the state error.
func TestPrecedencePermissionOverState(t *testing.T) {
	c := newTestChannel(t) // E=10
	joinAll(t, c, 0, "bob", "carol")
	seq := send(t, c, "alice", "m1", nil, 1)
	mustOK(t, c.Recall("alice", seq, 2))
	mustCode(t, c.Edit("bob", seq, "x", nil, 1000), ErrPermissionDenied)
	// Same for recall: carol cannot recall alice's recalled message.
	mustCode(t, c.Recall("carol", seq, 1001), ErrPermissionDenied)
}

// state: recalled > timeout. The author edits their own recalled message
// long after E -> already recalled wins over timeout.
func TestPrecedenceRecalledOverTimeout(t *testing.T) {
	c := newTestChannel(t) // E=10
	seq := send(t, c, "alice", "m1", nil, 1)
	mustOK(t, c.Recall("alice", seq, 2))
	mustCode(t, c.Edit("alice", seq, "x", nil, 1000), ErrAlreadyRecalled)
}

// state: timeout > edit limit. Message past E with the edit count already
// at K -> timeout wins.
func TestPrecedenceTimeoutOverEditLimit(t *testing.T) {
	c, err := NewChannel(10, 1000, 1)
	mustOK(t, err)
	joinAll(t, c, 0, "alice")
	seq := send(t, c, "alice", "v0", nil, 1)
	mustOK(t, c.Edit("alice", seq, "v1", nil, 2)) // edit count now 1 = K
	mustCode(t, c.Edit("alice", seq, "v2", nil, 1000), ErrTimeout)
}

// state: edit limit reached while still inside the window -> edit limit.
func TestPrecedenceEditLimitAlone(t *testing.T) {
	c, err := NewChannel(10, 1000, 1)
	mustOK(t, err)
	joinAll(t, c, 0, "alice")
	seq := send(t, c, "alice", "v0", nil, 1)
	mustOK(t, c.Edit("alice", seq, "v1", nil, 2))
	mustCode(t, c.Edit("alice", seq, "v2", nil, 3), ErrEditLimit)
}

// MarkRead state layer: rollback and out-of-range are both reported at the
// state level, below clock and membership.
func TestPrecedenceMarkReadLayers(t *testing.T) {
	c := newTestChannel(t)
	joinAll(t, c, 0, "bob")
	send(t, c, "alice", "m1", nil, 10)
	mustOK(t, c.MarkRead("bob", 1, 10))
	// clock skew outranks rollback.
	mustCode(t, c.MarkRead("bob", 0, 5), ErrClockSkew)
	// membership outranks rollback.
	mustCode(t, c.MarkRead("ghost", 0, 10), ErrNotMember)
	// rollback itself.
	mustCode(t, c.MarkRead("bob", 0, 10), ErrWatermarkRollback)
	// out of range itself.
	mustCode(t, c.MarkRead("bob", 2, 10), ErrOutOfRange)
}

// Rejected operations must not advance the clock: after a rejected op with
// a large now, an op with the previous now is still accepted.
func TestRejectedOpKeepsClock(t *testing.T) {
	c := newTestChannel(t)
	send(t, c, "alice", "m1", nil, 10)
	// Rejected (not a member) with now=100.
	mustCode(t, sendErr(c, "ghost", "hi", nil, 100), ErrNotMember)
	// Rejected (empty body) with now=200.
	mustCode(t, sendErr(c, "alice", "", nil, 200), ErrInvalidParam)
	// now=10 is still the last accepted now.
	send(t, c, "alice", "m2", nil, 10)
	mustCode(t, sendErr(c, "alice", "m3", nil, 9), ErrClockSkew)
}

package chat

import (
	"strings"
	"testing"
)

func TestFirstJoinerIsAdminAndPromote(t *testing.T) {
	c, err := NewChannel(10, 5, 2)
	mustOK(t, err)
	joinAll(t, c, 0, "alice", "bob", "carol")

	// bob is not an admin: cannot promote, cannot recall others' messages.
	mustCode(t, c.Promote("bob", "carol", 1), ErrPermissionDenied)
	seq := send(t, c, "alice", "hello", nil, 1)
	mustCode(t, c.Recall("bob", seq, 2), ErrPermissionDenied)

	// alice promotes bob; bob becomes admin and can recall anyone, anytime.
	mustOK(t, c.Promote("alice", "bob", 2))
	mustOK(t, c.Recall("bob", seq, 100))

	// Promote target must be a member (parameter constraint).
	mustCode(t, c.Promote("alice", "ghost", 101), ErrInvalidParam)
	// Promoting an existing admin is a no-op success.
	mustOK(t, c.Promote("alice", "bob", 102))
	// Promote caller must be a member.
	mustCode(t, c.Promote("ghost", "bob", 103), ErrNotMember)
}

func TestSendValidationAndSeqDensity(t *testing.T) {
	c := newTestChannel(t)
	joinAll(t, c, 0, "bob", "carol")

	mustCode(t, sendErr(c, "alice", "", nil, 1), ErrInvalidParam)
	mustCode(t, sendErr(c, "alice", strings.Repeat("x", 4001), nil, 1), ErrInvalidParam)
	mustCode(t, sendErr(c, "alice", "hi", []string{"bob", "bob"}, 1), ErrInvalidParam) // duplicate
	mustCode(t, sendErr(c, "alice", "hi", []string{"alice"}, 1), ErrInvalidParam)      // self
	mustCode(t, sendErr(c, "alice", "hi", []string{"ghost"}, 1), ErrInvalidParam)      // non-member
	mustCode(t, sendErr(c, "ghost", "hi", nil, 1), ErrNotMember)                       // sender
	mustCode(t, sendErr(c, "alice", "hi", make([]string, 21), 1), ErrInvalidParam)     // >20 mentions
	mustCode(t, sendErr(c, "", "hi", nil, 1), ErrInvalidParam)                         // empty user
	mustCode(t, sendErr(c, "alice", "hi", nil, -1), ErrInvalidParam)                   // now < 0
	mustCode(t, sendErr(c, "alice", "hi", nil, MaxNow+1), ErrInvalidParam)             // now > 1e12

	// All rejected: latest seq is still 0.
	if got := c.Latest(); got != 0 {
		t.Fatalf("rejected sends consumed seqs: latest=%d", got)
	}
	// 20 distinct member mentions are accepted.
	many := make([]string, 0, 20)
	for i := 0; i < 19; i++ {
		u := strings.Repeat("u", i+1)
		joinAll(t, c, 1, u)
		many = append(many, u)
	}
	many = append(many, "bob")
	if seq := send(t, c, "alice", "hi", many, 2); seq != 1 {
		t.Fatalf("first accepted send seq=%d, want 1", seq)
	}
	if seq := send(t, c, "bob", "yo", nil, 3); seq != 2 {
		t.Fatalf("second send seq=%d, want 2", seq)
	}
	// Exactly 4000 characters is accepted.
	send(t, c, "alice", strings.Repeat("字", 4000), nil, 4)
}

// TestEditWindowBoundary: channel E=10, message sent at now=100.
// Editable while now-100 < 10, i.e. up to now=109; now=110 is exactly E
// and must be rejected as timed out.
func TestEditWindowBoundary(t *testing.T) {
	c, err := NewChannel(10, 1000, 5)
	mustOK(t, err)
	joinAll(t, c, 0, "alice")
	seq := send(t, c, "alice", "v0", nil, 100)

	mustOK(t, c.Edit("alice", seq, "v1", nil, 109))               // E-1 seconds elapsed
	mustCode(t, c.Edit("alice", seq, "v2", nil, 110), ErrTimeout) // exactly E
	mustCode(t, c.Edit("alice", seq, "v2", nil, 111), ErrTimeout)
}

// TestRecallWindowBoundary: channel R=5, message sent at now=100.
// Author recall works up to now=104; now=105 is exactly R and times out.
func TestRecallWindowBoundary(t *testing.T) {
	c, err := NewChannel(1000, 5, 5)
	mustOK(t, err)
	joinAll(t, c, 0, "alice", "bob")

	seq1 := send(t, c, "bob", "a", nil, 100)
	mustOK(t, c.Recall("bob", seq1, 104)) // R-1 seconds elapsed

	seq2 := send(t, c, "bob", "b", nil, 105)
	mustCode(t, c.Recall("bob", seq2, 110), ErrTimeout) // exactly R
	// ...but an admin can still recall it at any time.
	mustOK(t, c.Recall("alice", seq2, 99999))
}

// TestEditCountLimit: K=2 allows exactly two edits; the third reports the
// edit limit.
func TestEditCountLimit(t *testing.T) {
	c, err := NewChannel(1000, 1000, 2)
	mustOK(t, err)
	joinAll(t, c, 0, "alice")
	seq := send(t, c, "alice", "v0", nil, 1)

	mustOK(t, c.Edit("alice", seq, "v1", nil, 2))
	mustOK(t, c.Edit("alice", seq, "v2", nil, 3))
	mustCode(t, c.Edit("alice", seq, "v3", nil, 4), ErrEditLimit)

	// A rejected edit consumed nothing: body is still v2.
	views, err := c.Fetch("alice", 0, 1, 5)
	mustOK(t, err)
	if views[0].Body != "v2" || views[0].EditCount != 2 {
		t.Fatalf("got %+v, want body=v2 edits=2", views[0])
	}
}

// TestEditCountZero: K=0 forbids any edit.
func TestEditCountZero(t *testing.T) {
	c, err := NewChannel(1000, 1000, 0)
	mustOK(t, err)
	joinAll(t, c, 0, "alice")
	seq := send(t, c, "alice", "v0", nil, 1)
	mustCode(t, c.Edit("alice", seq, "v1", nil, 2), ErrEditLimit)
}

// TestAdminRecallAnytimeAndPlaceholder: an admin recalls another member's
// message long after R; the recalled message keeps its seq and shows up in
// Fetch as a placeholder carrying only seq, recaller and recall time.
func TestAdminRecallAnytimeAndPlaceholder(t *testing.T) {
	c := newTestChannel(t) // E=10 R=5 K=2, alice is admin
	joinAll(t, c, 0, "bob")
	seq1 := send(t, c, "bob", "first", []string{"alice"}, 1)
	seq2 := send(t, c, "alice", "second", nil, 2)

	mustOK(t, c.Recall("alice", seq1, 1000)) // way past R=5, admin override

	views, err := c.Fetch("bob", 0, 100, 1001)
	mustOK(t, err)
	if len(views) != 2 {
		t.Fatalf("fetch returned %d views, want 2", len(views))
	}
	// Descending order: seq2 first.
	if views[0].Seq != seq2 || views[1].Seq != seq1 {
		t.Fatalf("fetch order = [%d %d], want [%d %d]", views[0].Seq, views[1].Seq, seq2, seq1)
	}
	ph := views[1]
	if !ph.Recalled || ph.RecalledBy != "alice" || ph.RecalledAt != 1000 {
		t.Fatalf("placeholder = %+v", ph)
	}
	if ph.Body != "" || ph.Author != "" || ph.Mentions != nil {
		t.Fatalf("placeholder leaks content: %+v", ph)
	}
	// Recalled message left every unread/mention counter.
	if n := unread(t, c, "alice"); n != 0 {
		t.Fatalf("alice unread=%d, want 0 (own message only)", n)
	}
	if n := unreadMentions(t, c, "alice"); n != 0 {
		t.Fatalf("alice unreadMentions=%d, want 0 after recall", n)
	}
	// Re-recall reports already-recalled.
	mustCode(t, c.Recall("alice", seq1, 1002), ErrAlreadyRecalled)
	// Recalled messages cannot be edited.
	mustCode(t, c.Edit("bob", seq1, "x", nil, 1003), ErrAlreadyRecalled)
}

// TestUnreadBasics: own messages never count; recalled messages drop out.
func TestUnreadBasics(t *testing.T) {
	c := newTestChannel(t)
	joinAll(t, c, 0, "bob")

	send(t, c, "alice", "m1", nil, 1) // for bob: unread
	send(t, c, "bob", "m2", nil, 2)   // own message: not unread for bob
	send(t, c, "alice", "m3", nil, 3)
	if n := unread(t, c, "bob"); n != 2 {
		t.Fatalf("bob unread=%d, want 2", n)
	}
	mustOK(t, c.Recall("alice", 3, 4))
	if n := unread(t, c, "bob"); n != 1 {
		t.Fatalf("bob unread after recall=%d, want 1", n)
	}
	// alice sees bob's message as her only unread.
	if n := unread(t, c, "alice"); n != 1 {
		t.Fatalf("alice unread=%d, want 1", n)
	}
}

// TestMentionAfterReadDoesNotCount: a mention added (or re-added) by an
// edit only enters the unread-mention count when the message seq is above
// the reader's watermark.
func TestMentionAfterReadDoesNotCount(t *testing.T) {
	c, err := NewChannel(1000, 1000, 5)
	mustOK(t, err)
	joinAll(t, c, 0, "alice", "bob", "carol")

	seq := send(t, c, "alice", "m1", nil, 1)
	// bob and carol have both read m1.
	mustOK(t, c.MarkRead("bob", 1, 2))
	mustOK(t, c.MarkRead("carol", 1, 2))

	// Edit adds mentions of bob and carol: seq(1) <= watermark(1), so the
	// re-mention does NOT count for them.
	mustOK(t, c.Edit("alice", seq, "m1'", []string{"bob", "carol"}, 3))
	if n := unreadMentions(t, c, "bob"); n != 0 {
		t.Fatalf("bob unreadMentions=%d, want 0 (message already read)", n)
	}
	if n := unreadMentions(t, c, "carol"); n != 0 {
		t.Fatalf("carol unreadMentions=%d, want 0 (message already read)", n)
	}

	// A new message above the watermark mentioning bob counts immediately.
	send(t, c, "alice", "m2", []string{"bob"}, 4)
	if n := unreadMentions(t, c, "bob"); n != 1 {
		t.Fatalf("bob unreadMentions=%d, want 1", n)
	}
	// carol was not mentioned in m2.
	if n := unreadMentions(t, c, "carol"); n != 0 {
		t.Fatalf("carol unreadMentions=%d, want 0", n)
	}
}

// TestEditRemovesMention: deleting a mention subtracts it from the unread
// mention count immediately.
func TestEditRemovesMention(t *testing.T) {
	c, err := NewChannel(1000, 1000, 5)
	mustOK(t, err)
	joinAll(t, c, 0, "alice", "bob")

	seq := send(t, c, "alice", "hey bob", []string{"bob"}, 1)
	if n := unreadMentions(t, c, "bob"); n != 1 {
		t.Fatalf("bob unreadMentions=%d, want 1", n)
	}
	mustOK(t, c.Edit("alice", seq, "hey nobody", nil, 2))
	if n := unreadMentions(t, c, "bob"); n != 0 {
		t.Fatalf("bob unreadMentions after edit=%d, want 0", n)
	}
	// The message itself is still unread for bob (only the mention left).
	if n := unread(t, c, "bob"); n != 1 {
		t.Fatalf("bob unread=%d, want 1", n)
	}
}

// TestEditReplacesMentionSet: the mention set is replaced as a whole.
func TestEditReplacesMentionSet(t *testing.T) {
	c, err := NewChannel(1000, 1000, 5)
	mustOK(t, err)
	joinAll(t, c, 0, "alice", "bob", "carol")

	seq := send(t, c, "alice", "m1", []string{"bob"}, 1)
	mustOK(t, c.Edit("alice", seq, "m1'", []string{"carol"}, 2))
	if n := unreadMentions(t, c, "bob"); n != 0 {
		t.Fatalf("bob unreadMentions=%d, want 0", n)
	}
	if n := unreadMentions(t, c, "carol"); n != 1 {
		t.Fatalf("carol unreadMentions=%d, want 1", n)
	}
}

// TestLeaveRejoinResetsWatermark: history before the re-join is invisible
// and never counts as unread.
func TestLeaveRejoinResetsWatermark(t *testing.T) {
	c := newTestChannel(t)
	joinAll(t, c, 0, "bob")

	send(t, c, "alice", "m1", []string{"bob"}, 1)
	send(t, c, "alice", "m2", nil, 2)
	if n := unread(t, c, "bob"); n != 2 {
		t.Fatalf("bob unread=%d, want 2", n)
	}
	mustOK(t, c.Leave("bob", 3))
	// While away: not a member anymore.
	mustCode(t, c.MarkRead("bob", 2, 4), ErrNotMember)
	if _, err := c.Unread("bob"); CodeOf(err) != ErrNotMember {
		t.Fatalf("Unread after leave: %v", err)
	}
	send(t, c, "alice", "m3", nil, 5)
	send(t, c, "alice", "m4", nil, 6)

	mustOK(t, c.Join("bob", 7))
	// Watermark reset to latest (4): nothing unread, nothing visible.
	if n := unread(t, c, "bob"); n != 0 {
		t.Fatalf("bob unread after rejoin=%d, want 0", n)
	}
	if n := unreadMentions(t, c, "bob"); n != 0 {
		t.Fatalf("bob unreadMentions after rejoin=%d, want 0", n)
	}
	views, err := c.Fetch("bob", 0, 100, 8)
	mustOK(t, err)
	if len(views) != 0 {
		t.Fatalf("bob sees %d messages after rejoin, want 0", len(views))
	}
	// New traffic is visible and unread again.
	send(t, c, "alice", "m5", nil, 9)
	if n := unread(t, c, "bob"); n != 1 {
		t.Fatalf("bob unread=%d, want 1", n)
	}
	views, err = c.Fetch("bob", 0, 100, 10)
	mustOK(t, err)
	if len(views) != 1 || views[0].Seq != 5 {
		t.Fatalf("bob fetch=%+v, want only seq 5", views)
	}
}

// TestMarkReadBoundaries: rollback, out-of-range and the exact-equal no-op.
func TestMarkReadBoundaries(t *testing.T) {
	c := newTestChannel(t)
	joinAll(t, c, 0, "bob")
	send(t, c, "alice", "m1", nil, 1)
	send(t, c, "alice", "m2", nil, 2)
	send(t, c, "alice", "m3", nil, 3)

	mustOK(t, c.MarkRead("bob", 2, 4))
	// Exactly equal: success, no change.
	mustOK(t, c.MarkRead("bob", 2, 5))
	if n := unread(t, c, "bob"); n != 1 {
		t.Fatalf("bob unread=%d, want 1", n)
	}
	// Rollback rejected.
	mustCode(t, c.MarkRead("bob", 1, 6), ErrWatermarkRollback)
	// Out of range rejected.
	mustCode(t, c.MarkRead("bob", 4, 7), ErrOutOfRange)
	// Negative upto is a parameter error, which outranks the state checks.
	mustCode(t, c.MarkRead("bob", -1, 8), ErrInvalidParam)
	// Watermark unchanged by the rejections.
	if n := unread(t, c, "bob"); n != 1 {
		t.Fatalf("bob unread=%d after rejections, want 1", n)
	}
	// Advance to the latest.
	mustOK(t, c.MarkRead("bob", 3, 9))
	if n := unread(t, c, "bob"); n != 0 {
		t.Fatalf("bob unread=%d, want 0", n)
	}
}

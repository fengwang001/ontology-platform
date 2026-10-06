package channel

import (
	"errors"
	"strings"
	"testing"
)

func newTestChannel(t *testing.T) *Channel {
	t.Helper()
	c, err := New(Config{EditWindow: 10, RecallWindow: 20, MaxEdits: 2})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return c
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func wantErr(t *testing.T, got, want error, ctx string) {
	t.Helper()
	if !errors.Is(got, want) {
		t.Fatalf("%s: want %v, got %v", ctx, want, got)
	}
}

func TestFirstJoinerIsAdminAndSequenceFromOne(t *testing.T) {
	c := newTestChannel(t)
	must(t, c.Join("alice", 0))
	if !c.IsAdmin("alice") {
		t.Fatal("first joiner must be admin")
	}
	wantErr(t, c.Join("alice", 1), ErrInvalidArg, "duplicate join")
	must(t, c.Join("bob", 1))
	if c.IsAdmin("bob") {
		t.Fatal("second joiner must not be admin")
	}
	s1, err := c.Send("alice", "hi", nil, 2)
	must(t, err)
	s2, err := c.Send("bob", "yo", nil, 2)
	must(t, err)
	if s1 != 1 || s2 != 2 {
		t.Fatalf("seq = %d,%d, want 1,2", s1, s2)
	}
}

func TestSendValidationAndRejectedDoesNotConsumeSeq(t *testing.T) {
	c := newTestChannel(t)
	must(t, c.Join("alice", 0))

	wantErr(t, firstErr(c.Send("alice", "", nil, 1)), ErrInvalidArg, "empty body")
	wantErr(t, firstErr(c.Send("alice", strings.Repeat("x", 4001), nil, 1)), ErrInvalidArg, "long body")
	wantErr(t, firstErr(c.Send("alice", "ok", []string{"alice"}, 1)), ErrInvalidArg, "self mention")
	wantErr(t, firstErr(c.Send("alice", "ok", []string{"ghost"}, 1)), ErrInvalidArg, "non-member mention")
	wantErr(t, firstErr(c.Send("alice", "ok", []string{"bob", "bob"}, 1)), ErrInvalidArg, "dup mention")
	wantErr(t, firstErr(c.Send("ghost", "ok", nil, 1)), ErrNotMember, "non-member sender")
	// 时钟回退用例放到最后：先以 now=0 建立接受基线，再以更小 now 调用。
	seq0, err := c.Send("alice", "ok0", nil, 0)
	must(t, err)
	if seq0 != 1 {
		t.Fatalf("seq0 = %d, want 1", seq0)
	}
	wantErr(t, firstErr(c.Send("alice", "rewind", nil, -1)), ErrInvalidArg, "now<0 is invalidArg before clock")

	seq, err := c.Send("alice", "ok", nil, 1)
	must(t, err)
	if seq != 2 {
		t.Fatalf("rejected sends must not consume seq, got %d", seq)
	}
}

func firstErr(_ int64, err error) error { return err }

func TestEditWindowBoundaryAndCountLimit(t *testing.T) {
	c := newTestChannel(t) // E=10, K=2
	must(t, c.Join("alice", 0))
	must(t, c.Join("bob", 0))
	seq, err := c.Send("alice", "v0", nil, 0)
	must(t, err)

	must(t, c.Edit("alice", seq, "v1", nil, 9))
	wantErr(t, c.Edit("alice", seq, "v2", nil, 10), ErrTimeout, "elapsed == E")
	must(t, c.Edit("alice", seq, "v2", nil, 9))
	wantErr(t, c.Edit("alice", seq, "v3", nil, 9), ErrEditLimit, "K reached")

	wantErr(t, c.Edit("bob", seq, "x", nil, 9), ErrForbidden, "non-author edit")
}

func TestRecallWindowAdminAndPlaceholder(t *testing.T) {
	c := newTestChannel(t) // R=20
	must(t, c.Join("alice", 0))
	must(t, c.Join("carol", 1))
	must(t, c.Join("bob", 1))
	seq, err := c.Send("carol", "secret", nil, 1)
	must(t, err)

	// 权限检查先于后续更大 now 的操作，避免时钟回退干扰。
	wantErr(t, c.Recall("bob", seq, 5), ErrForbidden, "non-admin recall others")
	// 非管理员作者在恰等于 R 时超时；差一秒（19 < 20）窗口内可撤回。
	wantErr(t, c.Recall("carol", seq, 21), ErrTimeout, "elapsed == R")

	seq2, err := c.Send("carol", "ok2", nil, 6)
	must(t, err)
	// seq2 差一秒：created=6, now=25 => 19 < 20，窗口内撤回成功。
	must(t, c.Recall("carol", seq2, 25))

	must(t, c.Promote("alice", "bob", 27))
	// seq 尚未撤回（恰等于 R 的撤回已被拒绝）；管理员可随时撤回任何人的消息。
	must(t, c.Recall("bob", seq, 100))
	if c.Latest() != 2 {
		t.Fatalf("recall must not change seq, latest=%d", c.Latest())
	}
	wantErr(t, c.Recall("bob", seq, 100), ErrRecalled, "double recall")
	wantErr(t, c.Edit("carol", seq, "x", nil, 100), ErrRecalled, "edit recalled")

	items, err := c.Fetch("bob", 0, 10, 100)
	must(t, err)
	if len(items) != 2 {
		t.Fatalf("recalled must appear as placeholder: %+v", items)
	}
	if items[0].Seq != 2 || items[0].Placeholder == nil || items[1].Placeholder == nil {
		t.Fatalf("both recalled messages must be placeholders: %+v", items)
	}
	if items[0].Placeholder.RecalledBy != "carol" || items[0].Placeholder.RecalledAt != 25 {
		t.Fatalf("placeholder trace wrong: %+v", items[0].Placeholder)
	}
	if items[1].Placeholder.RecalledBy != "bob" || items[1].Placeholder.RecalledAt != 100 {
		t.Fatalf("admin recall trace wrong: %+v", items[1].Placeholder)
	}
}

func TestReadWatermarkJoinLeaveRejoin(t *testing.T) {
	c := newTestChannel(t)
	must(t, c.Join("alice", 0))
	_, _ = c.Send("alice", "a", nil, 0)
	must(t, c.Join("bob", 1))
	w, err := c.Watermark("bob")
	must(t, err)
	if w != 1 {
		t.Fatalf("join watermark = %d, want 1", w)
	}
	_, _ = c.Send("alice", "b", nil, 2)
	must(t, c.MarkRead("bob", 2, 2))
	wantErr(t, c.MarkRead("bob", 1, 3), ErrWatermarkBack, "watermark back")
	wantErr(t, c.MarkRead("bob", 3, 3), ErrOutOfRange, "beyond latest")
	must(t, c.MarkRead("bob", 2, 4)) // 恰等于当前水位：成功无变化

	must(t, c.Leave("bob", 5))
	_, _ = c.Send("alice", "c", nil, 6)
	must(t, c.Join("bob", 7))
	w, err = c.Watermark("bob")
	must(t, err)
	if w != 3 {
		t.Fatalf("rejoin watermark = %d, want 3", w)
	}
	u, err := c.Unread("bob")
	must(t, err)
	if u != 0 {
		t.Fatalf("history before rejoin invisible, unread=%d", u)
	}
}

func TestMentionLifecycle(t *testing.T) {
	c := newTestChannel(t)
	must(t, c.Join("alice", 0))
	must(t, c.Join("bob", 0))

	s1, err := c.Send("alice", "m1", nil, 1)
	must(t, err)
	must(t, c.MarkRead("bob", 1, 1))
	must(t, c.Edit("alice", s1, "m1", []string{"bob"}, 2))
	um, err := c.UnreadMentions("bob")
	must(t, err)
	if um != 0 {
		t.Fatalf("mention added after read must not count, got %d", um)
	}

	s2, err := c.Send("alice", "m2", []string{"bob"}, 3)
	must(t, err)
	if um, _ = c.UnreadMentions("bob"); um != 1 {
		t.Fatalf("mention unread = %d, want 1", um)
	}
	if u, _ := c.Unread("bob"); u != 1 {
		t.Fatalf("unread = %d, want 1", u)
	}
	must(t, c.Edit("alice", s2, "m2", nil, 4))
	if um, _ = c.UnreadMentions("bob"); um != 0 {
		t.Fatalf("removed mention must subtract, got %d", um)
	}

	s3, err := c.Send("alice", "m3", []string{"bob"}, 5)
	must(t, err)
	must(t, c.Recall("alice", s3, 5))
	if um, _ = c.UnreadMentions("bob"); um != 0 {
		t.Fatalf("recalled mention must exit, got %d", um)
	}
	// s3 撤回后退出未读；但 s2（提及已删、正文仍在）仍贡献 1 条未读。
	if u, _ := c.Unread("bob"); u != 1 {
		t.Fatalf("recalled message must exit unread while s2 remains, got %d", u)
	}

	must(t, c.MarkRead("bob", s2, 6))
	_, err = c.Send("bob", "self", nil, 6)
	must(t, err)
	if u, _ := c.Unread("bob"); u != 0 {
		t.Fatalf("own messages excluded from unread, got %d", u)
	}
}

func TestFetchVisibilityOrderAndPaging(t *testing.T) {
	c := newTestChannel(t)
	must(t, c.Join("alice", 0))
	_, _ = c.Send("alice", "1", nil, 0)
	_, _ = c.Send("alice", "2", nil, 0)
	must(t, c.Join("bob", 1))
	_, _ = c.Send("alice", "3", nil, 2)
	_, _ = c.Send("alice", "4", nil, 2)

	items, err := c.Fetch("bob", 0, 100, 2)
	must(t, err)
	if len(items) != 2 || items[0].Seq != 4 || items[1].Seq != 3 {
		t.Fatalf("visibility/order wrong: %+v", items)
	}
	page, err := c.Fetch("bob", 4, 1, 2)
	must(t, err)
	if len(page) != 1 || page[0].Seq != 3 {
		t.Fatalf("before paging wrong: %+v", page)
	}
	wantErr(t, firstFetchErr(c.Fetch("ghost", 0, 10, 2)), ErrNotMember, "non-member fetch")
	wantErr(t, firstFetchErr(c.Fetch("bob", 0, 0, 2)), ErrInvalidArg, "limit 0")
	wantErr(t, firstFetchErr(c.Fetch("bob", 0, 101, 2)), ErrInvalidArg, "limit 101")
}

func firstFetchErr(_ []Item, err error) error { return err }

func TestRejectionOrderingAdjacentPairs(t *testing.T) {
	c := newTestChannel(t)
	must(t, c.Join("alice", 0))
	_, _ = c.Send("alice", "seed", nil, 5)

	// 参数非法 > 时钟回退
	wantErr(t, firstErr(c.Send("alice", "", nil, 0)), ErrInvalidArg, "invalidArg vs clockRewind")
	// 时钟回退 > 非成员
	must(t, c.Join("zoe", 6))
	must(t, c.Leave("zoe", 7))
	wantErr(t, firstErr(c.Send("zoe", "x", nil, 0)), ErrClockRewind, "clockRewind vs notMember")
	// 非成员 > 消息不存在
	wantErr(t, c.Edit("zoe", 999, "x", nil, 8), ErrNotMember, "notMember vs notFound")
	// 消息不存在 > 权限不足
	must(t, c.Join("bob", 8))
	wantErr(t, c.Edit("bob", 999, "x", nil, 9), ErrNotFound, "notFound vs forbidden")
	// 权限不足 > 已撤回（bob 非管理员，撤回 alice 的已撤回消息）
	s, _ := c.Send("alice", "todie", nil, 10)
	must(t, c.Recall("alice", s, 10))
	wantErr(t, c.Recall("bob", s, 11), ErrForbidden, "forbidden vs recalled")
	// 已撤回 > 超时（作者超时后再撤回自己已撤回的消息）
	wantErr(t, c.Recall("alice", s, 100), ErrRecalled, "recalled vs timeout")
	// 超时 > 次数超限（K 已用尽且已超时）
	c2 := newTestChannel(t)
	must(t, c2.Join("alice", 0))
	s2, _ := c2.Send("alice", "x", nil, 0)
	must(t, c2.Edit("alice", s2, "x", nil, 0))
	must(t, c2.Edit("alice", s2, "x", nil, 0))
	wantErr(t, c2.Edit("alice", s2, "x", nil, 100), ErrTimeout, "timeout vs editLimit")
}

func TestRejectedOpDoesNotAdvanceClockOrState(t *testing.T) {
	c := newTestChannel(t)
	must(t, c.Join("alice", 0))
	wantErr(t, firstErr(c.Send("alice", "", nil, 5)), ErrInvalidArg, "bad send")
	// 被拒操作时钟未推进到 5：now=3 合法；若已推进则应为时钟回退。
	seq, err := c.Send("alice", "ok", nil, 3)
	must(t, err)
	if seq != 1 {
		t.Fatalf("seq = %d, want 1", seq)
	}
}

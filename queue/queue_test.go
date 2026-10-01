package queue

import (
	"errors"
	"testing"
)

func mustNew(t *testing.T, capacity int, policy Policy) *Queue {
	t.Helper()
	q, err := New(capacity, policy)
	if err != nil {
		t.Fatalf("New(%d, %v) 返回错误: %v", capacity, policy, err)
	}
	return q
}

func mustEnqueue(t *testing.T, q *Queue, msg any, ttl, now int64) {
	t.Helper()
	if err := q.Enqueue(msg, ttl, now); err != nil {
		t.Fatalf("Enqueue(%v, ttl=%d, now=%d) 返回错误: %v", msg, ttl, now, err)
	}
}

func mustDequeue(t *testing.T, q *Queue, now int64) any {
	t.Helper()
	msg, err := q.Dequeue(now)
	if err != nil {
		t.Fatalf("Dequeue(now=%d) 返回错误: %v", now, err)
	}
	return msg
}

func assertDead(t *testing.T, q *Queue, want ...DeadLetter) {
	t.Helper()
	got := q.DeadLetters()
	if len(got) != len(want) {
		t.Fatalf("死信条数 = %d (%v)，期望 %d (%v)", len(got), got, len(want), want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("死信[%d] = %+v，期望 %+v", i, got[i], want[i])
		}
	}
}

func TestInvalidCapacity(t *testing.T) {
	for _, c := range []int{0, -1, -100} {
		if _, err := New(c, DropHead); !errors.Is(err, ErrInvalidCapacity) {
			t.Fatalf("New(%d) 错误 = %v，期望 ErrInvalidCapacity", c, err)
		}
	}
	if _, err := New(1, Reject); err != nil {
		t.Fatalf("New(1) 返回错误: %v", err)
	}
}

// 到期时刻恰等于 now 时消息视为已过期。
func TestExpireAtEqualsNow(t *testing.T) {
	q := mustNew(t, 2, DropHead)
	mustEnqueue(t, q, "a", 0, 10) // 到期时刻 10
	mustEnqueue(t, q, "b", 5, 10) // 到期时刻 15

	// now=10 入队触发清理：a 到期时刻恰为 10，视为过期进入死信。
	mustEnqueue(t, q, "c", 5, 10)
	assertDead(t, q, DeadLetter{Message: "a", Reason: ReasonExpired})

	// 队首 b 到期时刻 15 未过期，出队返回 b。
	msg := mustDequeue(t, q, 10)
	if msg != "b" {
		t.Fatalf("出队 = %v，期望 b", msg)
	}
}

// 队首未过期时清理停止，其后已过期的消息仍留在队中并计入长度。
func TestHeadAliveBlocksTailExpired(t *testing.T) {
	q := mustNew(t, 3, DropHead)
	mustEnqueue(t, q, "head", 100, 0) // 到期时刻 100，未过期
	mustEnqueue(t, q, "tail1", 1, 0)  // 到期时刻 1，已过期但被队首挡住
	mustEnqueue(t, q, "tail2", 1, 0)  // 同上

	// now=50：tail1/tail2 已过期，但队首未过期，清理不会发生，
	// 长度仍为 3（等于容量），入队触发丢弃队首策略。
	mustEnqueue(t, q, "new", 10, 50)

	if q.Len() != 3 {
		t.Fatalf("长度 = %d，期望 3", q.Len())
	}
	// 被驱逐的是未过期的队首 head，而不是已过期的 tail1/tail2。
	assertDead(t, q, DeadLetter{Message: "head", Reason: ReasonOverflow})

	// 出队时队首是已过期的 tail1/tail2，连续清理后返回 new。
	msg := mustDequeue(t, q, 50)
	if msg != "new" {
		t.Fatalf("出队 = %v，期望 new", msg)
	}
	assertDead(t, q,
		DeadLetter{Message: "head", Reason: ReasonOverflow},
		DeadLetter{Message: "tail1", Reason: ReasonExpired},
		DeadLetter{Message: "tail2", Reason: ReasonExpired},
	)
}

// 拒绝策略下清理后仍满则整体拒绝，队列、死信与已见最大 now 均不变。
func TestRejectPolicyFull(t *testing.T) {
	q := mustNew(t, 1, Reject)
	mustEnqueue(t, q, "a", 100, 10)

	if err := q.Enqueue("b", 1, 20); !errors.Is(err, ErrQueueFull) {
		t.Fatalf("满队列拒绝策略入队错误 = %v，期望 ErrQueueFull", err)
	}
	if q.Len() != 1 {
		t.Fatalf("拒绝后长度 = %d，期望 1", q.Len())
	}
	assertDead(t, q)

	// 已见最大 now 仍为 10：now=15 的调用不应被判为时钟倒退。
	if msg := mustDequeue(t, q, 15); msg != "a" {
		t.Fatalf("出队 = %v，期望 a（拒绝的入队不得更新已见最大 now）", msg)
	}
}

// 拒绝策略下，即使模拟清理能清出过期消息，只要清理后仍满就拒绝；
// 且出队清理后队空时清理不落地（过期消息留在队中、死信不变）。
func TestDequeueEmptyCleanupNotCommitted(t *testing.T) {
	q := mustNew(t, 1, DropHead)
	mustEnqueue(t, q, "a", 1, 0) // 到期时刻 1

	// now=10：a 已过期，清理后队空，返回 ErrEmptyQueue 且清理不落地。
	if _, err := q.Dequeue(10); !errors.Is(err, ErrEmptyQueue) {
		t.Fatalf("清理后队空应返回 ErrEmptyQueue，得到 %v", err)
	}
	if q.Len() != 1 {
		t.Fatalf("清理不应落地，长度 = %d，期望 1", q.Len())
	}
	assertDead(t, q)

	// 之后的入队重新触发清理并落地：a 进入死信（过期），b 入队。
	mustEnqueue(t, q, "b", 100, 10)
	assertDead(t, q, DeadLetter{Message: "a", Reason: ReasonExpired})
	if msg := mustDequeue(t, q, 10); msg != "b" {
		t.Fatalf("出队 = %v，期望 b", msg)
	}
}

// 清理腾出空位时入队不触发溢出。
func TestCleanupFreesSpace(t *testing.T) {
	q := mustNew(t, 2, DropHead)
	mustEnqueue(t, q, "x", 1, 0) // 到期时刻 1
	mustEnqueue(t, q, "y", 1, 0) // 到期时刻 1

	// now=10：x、y 均已过期且在队首连续段，清理后长度 0 < 2，直接入队。
	mustEnqueue(t, q, "z", 100, 10)

	assertDead(t, q,
		DeadLetter{Message: "x", Reason: ReasonExpired},
		DeadLetter{Message: "y", Reason: ReasonExpired},
	)
	if q.Len() != 1 {
		t.Fatalf("长度 = %d，期望 1", q.Len())
	}
	if msg := mustDequeue(t, q, 10); msg != "z" {
		t.Fatalf("出队 = %v，期望 z", msg)
	}
}

func TestNegativeTTL(t *testing.T) {
	q := mustNew(t, 1, DropHead)
	if err := q.Enqueue("a", -1, 0); !errors.Is(err, ErrNegativeTTL) {
		t.Fatalf("负 ttl 错误 = %v，期望 ErrNegativeTTL", err)
	}
	if q.Len() != 0 {
		t.Fatalf("拒绝后长度 = %d，期望 0", q.Len())
	}
}

// 时钟倒退优先于其他原因（负 ttl、队满、队空）。
func TestClockBackwardPriority(t *testing.T) {
	q := mustNew(t, 1, Reject)
	mustEnqueue(t, q, "a", 100, 10)

	// now 倒退 + 负 ttl + 队满：必须报时钟倒退。
	if err := q.Enqueue("b", -1, 5); !errors.Is(err, ErrClockBackward) {
		t.Fatalf("时钟倒退应优先，得到 %v", err)
	}
	// now 倒退 + 队空出队场景。
	q2 := mustNew(t, 1, DropHead)
	mustEnqueue(t, q2, "x", 1, 10)
	if _, err := q2.Dequeue(5); !errors.Is(err, ErrClockBackward) {
		t.Fatalf("时钟倒退应优先于队空，得到 %v", err)
	}
	// 被拒绝的调用不更新已见最大 now：now=10 仍合法。
	mustEnqueue(t, q2, "y", 1, 10)
}

// 相同调用序列重放得到完全相同的出队与死信序列。
func TestReplayDeterminism(t *testing.T) {
	type op struct {
		enqueue  bool
		msg      any
		ttl, now int64
	}
	ops := []op{
		{true, "m1", 5, 0},
		{true, "m2", 1, 0},
		{false, nil, 0, 3},
		{true, "m3", 0, 3},
		{false, nil, 0, 3},
		{true, "m4", 10, 4},
		{false, nil, 0, 20},
		{false, nil, 0, 20},
	}
	run := func() ([]any, []DeadLetter) {
		q := mustNew(t, 2, DropHead)
		var out []any
		for _, o := range ops {
			if o.enqueue {
				_ = q.Enqueue(o.msg, o.ttl, o.now)
			} else if msg, err := q.Dequeue(o.now); err == nil {
				out = append(out, msg)
			}
		}
		return out, q.DeadLetters()
	}
	out1, dead1 := run()
	out2, dead2 := run()
	if len(out1) != len(out2) || len(dead1) != len(dead2) {
		t.Fatalf("重放结果不一致: %v/%v vs %v/%v", out1, dead1, out2, dead2)
	}
	for i := range out1 {
		if out1[i] != out2[i] {
			t.Fatalf("出队[%d] 重放不一致: %v vs %v", i, out1[i], out2[i])
		}
	}
	for i := range dead1 {
		if dead1[i] != dead2[i] {
			t.Fatalf("死信[%d] 重放不一致: %v vs %v", i, dead1[i], dead2[i])
		}
	}
	t.Logf("重放一致：出队序列 %v，死信序列 %v", out1, dead1)
}

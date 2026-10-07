package whiteboard

import "testing"

// 拒绝优先级：参数非法 > 时钟回退 > 不存在 > 不合法 > 被锁定 > 版本冲突。
// 每个用例同时违反相邻两类约束，只应报告优先级更高的一类。
func TestRejectionPrecedenceAdjacentPairs(t *testing.T) {
	// 参数非法 > 时钟回退：空 target 且 now 回退。
	t.Run("param>clock", func(t *testing.T) {
		b := New()
		setup(t, b, "a")
		mustOK(t, b.Add("u", "b", 100))
		mustKind(t, b.Reorder("u", "", "a", Above, 0, 50), ErrInvalidParam)
	})
	// 时钟回退 > 不存在：target 不存在且 now 回退。
	t.Run("clock>notfound", func(t *testing.T) {
		b := New()
		setup(t, b, "a")
		mustOK(t, b.Add("u", "b", 100))
		mustKind(t, b.Reorder("u", "ghost", "a", Above, 0, 50), ErrClock)
	})
	// 不存在 > 不合法：成员不存在且另一成员已属组合。
	t.Run("notfound>invalid", func(t *testing.T) {
		b := New()
		setup(t, b, "a", "b", "c")
		mustOK(t, b.Group("u", "g1", []string{"a", "b"}, 10))
		// ghost 不存在，a 已组合会重叠——但先报不存在。
		mustKind(t, b.Group("u", "g2", []string{"ghost", "a"}, 11), ErrNotFound)
	})
	// 不合法 > 被锁定：直接移动组合成员，且该成员被他人锁住。
	t.Run("invalid>locked", func(t *testing.T) {
		b := New()
		setup(t, b, "a", "b", "c")
		mustOK(t, b.Group("u", "g1", []string{"a", "b"}, 10))
		mustOK(t, b.Lock("alice", "a", 100, 11))
		mustKind(t, b.Reorder("bob", "a", "c", Above, 99, 12), ErrInvalidTarget)
	})
	// 被锁定 > 版本冲突：移动集被他人锁住，且最近影响修订号大于 baseRev。
	t.Run("locked>conflict", func(t *testing.T) {
		b := New()
		setup(t, b, "a", "b") // a 的 rev=1
		mustOK(t, b.Lock("alice", "a", 100, 10))
		mustKind(t, b.Reorder("bob", "a", "b", Above, 0, 11), ErrLocked)
	})
}

// 被拒绝的操作不得改变修订号、次序、锁与时钟。
func TestRejectedOpsHaveNoSideEffects(t *testing.T) {
	b := New()
	setup(t, b, "a", "b", "c")
	mustOK(t, b.Group("u", "g", []string{"a", "b"}, 10))
	mustOK(t, b.Lock("alice", "c", 100, 11)) // c 被 alice 锁到 111
	revBefore := b.Revision()
	orderBefore := b.Order()
	// 各类被拒绝操作。
	mustKind(t, b.Add("", "x", 12), ErrInvalidParam)
	mustKind(t, b.Add("u", "x", 5), ErrClock)
	mustKind(t, b.Remove("u", "ghost", 12), ErrNotFound)
	mustKind(t, b.Reorder("u", "a", "c", Above, 99, 12), ErrInvalidTarget)
	mustKind(t, b.Remove("bob", "c", 12), ErrLocked)
	mustKind(t, b.Reorder("u", "g", "c", Above, 0, 12), ErrConflict) // a、b 的 rev=4 > 0
	mustKind(t, b.Lock("bob", "c", 10, 12), ErrLocked)
	mustKind(t, b.Unlock("bob", "c", 12), ErrLocked)
	// 修订号与次序不变。
	if got := b.Revision(); got != revBefore {
		t.Fatalf("revision changed: %d -> %d", revBefore, got)
	}
	mustOrder(t, b, orderBefore...)
	// 锁不变：c 仍被 alice 持有，alice 仍可解锁。
	werr := mustKind(t, b.Lock("bob", "c", 10, 12), ErrLocked)
	if werr.Holder != "alice" || werr.Remaining != 99 {
		t.Fatalf("lock state changed: holder=%q remaining=%d", werr.Holder, werr.Remaining)
	}
	// 时钟不变：被拒操作的 now=12 不得推进时钟；now=11 仍应被接受。
	mustOK(t, b.Lock("alice", "c", 10, 11)) // 持有者续期，now=11 被接受
	mustOK(t, b.Unlock("alice", "c", 12))
}

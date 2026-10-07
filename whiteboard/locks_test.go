package whiteboard

import "testing"

// 到期边界：锁在 now+ttl 到期，恰等于到期时刻视为已到期，差一秒仍未到期。
func TestLockExpiryBoundary(t *testing.T) {
	b := New()
	setup(t, b, "a")
	mustOK(t, b.Lock("alice", "a", 100, 1000)) // 到期时刻 1100

	// 差一秒（now=1099）：他人加锁被拒，剩余 1 秒。
	err := b.Lock("bob", "a", 10, 1099)
	werr := mustKind(t, err, ErrLocked)
	if werr.Holder != "alice" || werr.Remaining != 1 {
		t.Fatalf("holder=%q remaining=%d, want alice/1", werr.Holder, werr.Remaining)
	}

	// 恰等于到期时刻（now=1100）：视为已到期，他人可加锁。
	mustOK(t, b.Lock("bob", "a", 10, 1100))

	// bob 的锁 1110 到期；alice 在 1109 操作被拒，1110 可行。
	mustKind(t, b.Lock("alice", "a", 5, 1109), ErrLocked)
	mustOK(t, b.Lock("alice", "a", 5, 1110))
}

// 续期：同一用户再次加锁取代旧到期时刻；他人加锁被拒。
func TestLockRenewAndOthers(t *testing.T) {
	b := New()
	setup(t, b, "a")
	mustOK(t, b.Lock("alice", "a", 10, 0)) // 到期 10
	mustOK(t, b.Lock("alice", "a", 50, 5)) // 续期：到期 55

	// 若未续期，now=20 时锁早已到期；续期后仍被 alice 持有。
	err := b.Lock("bob", "a", 10, 20)
	werr := mustKind(t, err, ErrLocked)
	if werr.Holder != "alice" || werr.Remaining != 35 {
		t.Fatalf("holder=%q remaining=%d, want alice/35", werr.Holder, werr.Remaining)
	}

	// 仅持有者可解锁。
	mustKind(t, b.Unlock("bob", "a", 20), ErrLocked)
	mustOK(t, b.Unlock("alice", "a", 20))
	mustOK(t, b.Lock("bob", "a", 10, 20)) // 解锁后他人可加锁
}

// 组合锁覆盖成员；成员锁使组合整体不可被他人操作。
func TestGroupLockCoverage(t *testing.T) {
	b := New()
	setup(t, b, "a", "b", "c", "d")
	mustOK(t, b.Group("alice", "g", []string{"b", "c"}, 10))

	// 组合锁覆盖全部成员：他人不能动成员，也不能动组合。
	mustOK(t, b.Lock("alice", "g", 100, 20))
	mustOK(t, b.Reorder("bob", "a", "b", Above, 99, 21)) // anchor 被锁不算触碰：允许
	mustOrder(t, b, "b", "a", "c", "d")
	mustOK(t, b.Reorder("bob", "a", "d", Above, 99, 21)) // 复位，便于后续断言
	mustOrder(t, b, "b", "c", "d", "a")
	mustOK(t, b.Reorder("bob", "a", "b", Below, 99, 21))
	mustOrder(t, b, "a", "b", "c", "d")
	mustKind(t, b.Remove("bob", "g", 21), ErrLocked)
	mustKind(t, b.Ungroup("bob", "g", 21), ErrLocked)
	mustKind(t, b.Lock("bob", "b", 10, 21), ErrLocked) // 成员被组合锁覆盖

	// 持有者自己不受妨。
	mustOK(t, b.Reorder("alice", "g", "a", Below, 99, 22))
	mustOrder(t, b, "b", "c", "a", "d")
	mustOK(t, b.Reorder("alice", "g", "a", Above, 99, 22))
	mustOrder(t, b, "a", "b", "c", "d")

	mustOK(t, b.Unlock("alice", "g", 23))

	// 成员上的锁使组合整体不可被他人操作。
	mustOK(t, b.Lock("bob", "c", 100, 24))
	mustKind(t, b.Reorder("alice", "g", "d", Below, 99, 25), ErrLocked)
	mustKind(t, b.Lock("alice", "g", 10, 25), ErrLocked)
	mustOK(t, b.Reorder("bob", "g", "d", Above, 99, 25)) // 持有者自己可行
	mustOrder(t, b, "a", "d", "b", "c")
}

// 移到被他人锁住的元素旁边不算触碰该元素。
func TestMoveNextToLockedAnchorAllowed(t *testing.T) {
	b := New()
	setup(t, b, "a", "b", "c")
	mustOK(t, b.Lock("alice", "b", 100, 2))
	// bob 把 a 移到被 alice 锁住的 b 旁边：允许。
	mustOK(t, b.Reorder("bob", "a", "b", Above, 99, 3))
	mustOrder(t, b, "b", "a", "c")
	mustOK(t, b.Reorder("bob", "c", "b", Below, 99, 4))
	mustOrder(t, b, "c", "b", "a")
}

// 锁与解锁不改修订号。
func TestLocksDoNotBumpRevision(t *testing.T) {
	b := New()
	setup(t, b, "a")
	rev := b.Revision()
	mustOK(t, b.Lock("alice", "a", 10, 1))
	mustOK(t, b.Unlock("alice", "a", 2))
	_, _ = b.Rank("a")
	_ = b.Order()
	if got := b.Revision(); got != rev {
		t.Fatalf("revision changed by lock/unlock/query: %d -> %d", rev, got)
	}
}

package envlock

import "testing"

func TestTryTakeStaleExpiredLockedOrder(t *testing.T) {
	e := New("e")
	e.Enqueue(Queued{ID: 1, Ver: 5}) // 已过时（cur=7）
	e.Enqueue(Queued{ID: 2, Ver: 8}) // 批准不足
	e.Enqueue(Queued{ID: 3, Ver: 9}) // 可授锁
	e.MarkSucceeded(7)
	need := map[int64]int{1: 0, 2: 1, 3: 0}
	votes := map[int64]int{2: 0}
	var stale, expired []int64
	e.TryTake(
		func(id int64) int { return votes[id] },
		func(id int64) int { return need[id] },
		&stale, &expired,
	)
	if len(stale) != 1 || stale[0] != 1 {
		t.Fatalf("stale=%v want [1]", stale)
	}
	if len(expired) != 1 || expired[0] != 2 {
		t.Fatalf("expired=%v want [2]", expired)
	}
	if e.Holder() != 3 || e.HolderVer() != 9 {
		t.Fatalf("holder=%d ver=%d want 3/9", e.Holder(), e.HolderVer())
	}
}

func TestRemoveFromQueue(t *testing.T) {
	e := New("e")
	for _, id := range []int64{1, 2, 3} {
		e.Enqueue(Queued{ID: id, Ver: id})
	}
	e.Remove(2)
	e.Remove(99)
	if e.QueueLen() != 2 {
		t.Fatalf("len=%d want 2", e.QueueLen())
	}
}

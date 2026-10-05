package escrow

import "testing"

func TestSetAndRelease(t *testing.T) {
	e := New()
	e.Set(1, "a", map[string]int64{"x": 3, "y": 2}, 10)
	e.Set(1, "b", map[string]int64{"x": 1}, 0)
	e.Set(2, "a", map[string]int64{"x": 1}, 5)

	if got := e.Locked("a", "x"); got != 4 {
		t.Fatalf("locked a.x = %d, want 4", got)
	}
	if got := e.Locked("a", "y"); got != 2 {
		t.Fatalf("locked a.y = %d, want 2", got)
	}
	if got := e.LockedGold("a"); got != 15 {
		t.Fatalf("locked gold a = %d, want 15", got)
	}

	// 整体替换 a 在会话 1 的报价：旧锁定撤掉，新锁定计入。
	e.Set(1, "a", map[string]int64{"z": 7}, 1)
	if got := e.Locked("a", "x"); got != 1 {
		t.Fatalf("after replace locked a.x = %d, want 1", got)
	}
	if got := e.Locked("a", "y"); got != 0 {
		t.Fatalf("after replace locked a.y = %d, want 0", got)
	}
	if got := e.Locked("a", "z"); got != 7 {
		t.Fatalf("after replace locked a.z = %d, want 7", got)
	}
	if got := e.LockedGold("a"); got != 6 {
		t.Fatalf("after replace locked gold a = %d, want 6", got)
	}

	e.Release(1)
	if got := e.Locked("a", "z"); got != 0 {
		t.Fatalf("after release locked a.z = %d, want 0", got)
	}
	if got := e.Locked("b", "x"); got != 0 {
		t.Fatalf("after release locked b.x = %d, want 0", got)
	}
	if got := e.LockedGold("a"); got != 5 {
		t.Fatalf("after release locked gold a = %d, want 5", got)
	}
	t.Logf("release 后仅余会话 2 的锁定: a.x=%d gold=%d", e.Locked("a", "x"), e.LockedGold("a"))
}

// TestTouchedIndependentOfOtherSessions 证明一次 Set 读写的锁定记录数
// 不超过新旧报价条目数之和加 2，与该玩家其他会话数量无关。
func TestTouchedIndependentOfOtherSessions(t *testing.T) {
	for _, others := range []int{1, 1000} {
		e := New()
		for i := 0; i < others; i++ {
			e.Set(int64(1000+i), "p", map[string]int64{"k": 1}, 1)
		}
		e.Set(1, "p", map[string]int64{"x": 2, "y": 3}, 5) // 旧报价：2 物品 + 金币
		before := e.touched
		e.Set(1, "p", map[string]int64{"z": 1}, 7) // 新报价：1 物品 + 金币
		got := e.touched - before
		want := int64(2 + 1 + 2) // 旧 2 条 + 新 1 条 + 金币新旧 2 条
		if got != want {
			t.Fatalf("others=%d: touched = %d, want %d", others, got, want)
		}
		t.Logf("其他会话 %d 个: Set 触碰记录 %d 条（旧 2 + 新 1 + 金币 2），与其他会话数无关",
			others, got)
	}
}

// TestTouchedFirstOffer 首次报价（无旧报价）只触碰新报价条目加 2。
func TestTouchedFirstOffer(t *testing.T) {
	e := New()
	before := e.touched
	e.Set(9, "p", map[string]int64{"x": 1, "y": 1, "z": 1}, 0)
	if got := e.touched - before; got != 5 {
		t.Fatalf("touched = %d, want 5", got)
	}
}

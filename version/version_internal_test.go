package version

import (
	"testing"
)

// TestTouchedBound 证明 Get（Current）与“删除当前版本后重新确定当前版本”
// 触碰的版本记录数不超过 2，且与全桶版本总数、该键版本数无关。
func TestTouchedBound(t *testing.T) {
	for _, n := range []int{100, 10000} {
		s := NewStore()
		key := []byte("k")
		for v := int64(1); v <= int64(n); v++ {
			s.Append(key, v, false, v)
		}
		if s.totalVersions() != n {
			t.Fatalf("n=%d 总数=%d", n, s.totalVersions())
		}

		// Get 当前版本：只触碰 1 条。
		s.resetTouched()
		cur, ok := s.Current(key)
		if !ok || cur.Ver != int64(n) {
			t.Fatalf("n=%d 当前版本错误: %+v %v", n, cur, ok)
		}
		getTouch := s.touchedCount()
		if getTouch != 1 {
			t.Fatalf("n=%d Current 触碰 %d 条，期望 1", n, getTouch)
		}

		// 删除当前版本并重定当前版本：触碰被删节点 + 前驱，共 2 条。
		s.resetTouched()
		if was := s.Delete(key, int64(n)); !was {
			t.Fatalf("n=%d 删除当前版本未报告", n)
		}
		cur, ok = s.Current(key)
		if !ok || cur.Ver != int64(n-1) {
			t.Fatalf("n=%d 重指后当前版本错误: %+v %v", n, cur, ok)
		}
		if got := s.touchedCount(); got > 2 {
			t.Fatalf("n=%d 删除当前+重定触碰 %d 条，上界 2", n, got)
		}
		repTouch := s.touchedCount()
		t.Logf("全桶总数=%d 该键版本数=%d: Get 触碰 %d 条；删除当前版本后重定当前触碰 %d 条（上界 2）",
			n, n, getTouch, repTouch)
		t.Logf("总数=%d: Get 当前触碰 %d 条；删除当前版本后重定当前触碰 %d 条（上界 2）",
			n, 1, s.touchedCount())

		// 删非当前版本：只触碰 1 条，且当前版本不变。
		s.resetTouched()
		s.Delete(key, 1)
		if got := s.touchedCount(); got != 1 {
			t.Fatalf("n=%d 删除非当前版本触碰 %d 条，期望 1", n, got)
		}
		cur, _ = s.Current(key)
		if cur.Ver != int64(n-1) {
			t.Fatalf("n=%d 非当前删除影响了当前版本: %d", n, cur.Ver)
		}

		// 删光后键不存在。
		s2 := NewStore()
		s2.Append(key, 0, false, 1)
		s2.resetTouched()
		s2.Delete(key, 1)
		if _, ok := s2.Current(key); ok {
			t.Fatal("唯一版本删除后应无当前版本")
		}
		if got := s2.touchedCount(); got > 2 {
			t.Fatalf("删唯一版本触碰 %d 条", got)
		}
	}
}

// TestMarkerChain 验证数据版本与删除标记混排时当前指针始终指向尾部。
func TestMarkerChain(t *testing.T) {
	s := NewStore()
	key := []byte("a")
	s.Append(key, 1, false, 1)
	s.Append(key, 0, true, 2)
	s.Append(key, 3, false, 3)
	cur, _ := s.Current(key)
	if cur.Marker || cur.Ver != 3 || cur.Size != 3 {
		t.Fatalf("当前应为数据 v3: %+v", cur)
	}
	s.Delete(key, 3)
	cur, _ = s.Current(key)
	if !cur.Marker || cur.Ver != 2 {
		t.Fatalf("当前应为标记 v2: %+v", cur)
	}
	s.Delete(key, 2)
	cur, _ = s.Current(key)
	if cur.Marker || cur.Ver != 1 {
		t.Fatalf("当前应为数据 v1: %+v", cur)
	}
	s.Delete(key, 1)
	if _, ok := s.Current(key); ok {
		t.Fatal("全部删除后应不存在")
	}
}

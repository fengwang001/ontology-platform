package whiteboard

import (
	"sync"
	"testing"
)

// 组合整体移动保持内部相对次序。
func TestGroupMovePreservesInternalOrder(t *testing.T) {
	b := New()
	setup(t, b, "a", "b", "c", "d", "e", "f")
	mustOK(t, b.Group("u", "g", []string{"b", "c", "e"}, 10))
	mustOK(t, b.Reorder("u", "g", "f", Above, 99, 11))
	mustOrder(t, b, "a", "d", "f", "b", "c", "e") // b<c<e 相对次序不变
	mustOK(t, b.Reorder("u", "g", "a", Below, 99, 12))
	mustOrder(t, b, "b", "c", "e", "a", "d", "f")
	// 段内名次连续。
	for i, id := range []string{"b", "c", "e"} {
		r, ok := b.Rank(id)
		if !ok || r != i {
			t.Fatalf("Rank(%s)=%d,%v want %d,true", id, r, ok, i)
		}
	}
}

// 锚点在他人组合内：允许，落点紧贴 anchor 本身，可把该组合从中间隔开。
func TestAnchorInsideOtherGroup(t *testing.T) {
	b := New()
	setup(t, b, "a", "b", "c", "d", "x")
	mustOK(t, b.Group("u", "g", []string{"b", "c", "d"}, 10))
	// 把 x 移到组合 g 的成员 c 上方：g 被从中间隔开，允许。
	mustOK(t, b.Reorder("u", "x", "c", Above, 99, 11))
	mustOrder(t, b, "a", "b", "c", "x", "d")
	// 组合整体移动后重新连续，且内部次序不变。
	mustOK(t, b.Reorder("u", "g", "x", Above, 99, 12))
	mustOrder(t, b, "a", "x", "b", "c", "d")
	if got := b.Revision(); got != 8 { // 5 Add + Group + 2 Reorder
		t.Fatalf("revision = %d, want 8", got)
	}
}

// 直接对属于某组合的元素单独 Reorder 须拒绝。
func TestMemberSoloReorderRejected(t *testing.T) {
	b := New()
	setup(t, b, "a", "b", "c", "d")
	mustOK(t, b.Group("u", "g", []string{"b", "c"}, 10))
	mustKind(t, b.Reorder("u", "b", "d", Above, 99, 11), ErrInvalidTarget)
	mustKind(t, b.Reorder("u", "c", "a", Below, 99, 11), ErrInvalidTarget)
	mustOrder(t, b, "a", "b", "c", "d") // 次序未变
}

// 参照在移动集合内须拒绝。
func TestAnchorInsideMovingSetRejected(t *testing.T) {
	b := New()
	setup(t, b, "a", "b", "c", "d")
	mustOK(t, b.Group("u", "g", []string{"b", "c"}, 10))
	mustKind(t, b.Reorder("u", "g", "b", Above, 99, 11), ErrInvalidTarget)
	mustKind(t, b.Reorder("u", "a", "a", Below, 99, 11), ErrInvalidTarget)
}

// 版本冲突边界：最近影响修订号等于 baseRev 通过，大于 baseRev 冲突。
func TestVersionConflictBoundary(t *testing.T) {
	b := New()
	setup(t, b, "a", "b", "c") // rev: a=1,b=2,c=3
	// b 的最近影响修订号为 2：baseRev=2 通过，baseRev=1 冲突。
	mustOK(t, b.Reorder("u", "b", "c", Above, 2, 10)) // 接受后 b 的 rev=4
	mustKind(t, b.Reorder("u", "b", "a", Below, 3, 11), ErrConflict)
	mustOK(t, b.Reorder("u", "b", "a", Below, 4, 11))
	// 组合任一成员超限即冲突。
	mustOK(t, b.Group("u", "g", []string{"a", "c"}, 12)) // rev=6，a、c 的 rev=6
	mustKind(t, b.Reorder("u", "g", "b", Above, 5, 13), ErrConflict)
	mustOK(t, b.Reorder("u", "g", "b", Above, 6, 13))
	mustOrder(t, b, "b", "a", "c")
}

// 组合成员重叠：已属于组合的元素不能再入新组合。
func TestGroupMemberOverlapRejected(t *testing.T) {
	b := New()
	setup(t, b, "a", "b", "c", "d")
	mustOK(t, b.Group("u", "g1", []string{"a", "b"}, 10))
	mustKind(t, b.Group("u", "g2", []string{"b", "c"}, 11), ErrInvalidTarget)
	mustOK(t, b.Group("u", "g2", []string{"c", "d"}, 11))
}

// 并发调用：结果等价于某个串行顺序（-race 下验证无数据竞争，
// 且树结构不变量与修订号计数始终成立）。
func TestConcurrentSerialization(t *testing.T) {
	b := New()
	setup(t, b, "a", "b", "c", "d", "e", "f", "g", "h")
	mustOK(t, b.Group("u0", "grp", []string{"c", "d"}, 100))
	var wg sync.WaitGroup
	accepted := make(chan struct{}, 10000)
	users := []string{"u1", "u2", "u3", "u4"}
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			u := users[i%len(users)]
			for j := 0; j < 200; j++ {
				now := int64(100 + j)
				switch j % 6 {
				case 0:
					if err := b.Reorder(u, "grp", "h", Above, 1<<62, now); err == nil {
						accepted <- struct{}{}
					}
				case 1:
					if err := b.Reorder(u, "a", "b", Below, 1<<62, now); err == nil {
						accepted <- struct{}{}
					}
				case 2:
					_ = b.Lock(u, "e", 5, now)
				case 3:
					_ = b.Unlock(u, "e", now)
				case 4:
					_, _ = b.Rank("c")
				case 5:
					_ = b.Order()
				}
			}
		}(i)
	}
	wg.Wait()
	close(accepted)
	// 每次被接受的 Reorder 恰好使修订号加 1。
	want := uint64(9) // 8 Add + 1 Group
	for range accepted {
		want++
	}
	if got := b.Revision(); got != want {
		t.Fatalf("revision = %d, want %d", got, want)
	}
	// 树结构不变量。
	if err := b.ol.validate(); err != nil {
		t.Fatalf("treap invariant violated: %v", err)
	}
	// Order 长度守恒。
	if got := len(b.Order()); got != 8 {
		t.Fatalf("order length = %d, want 8", got)
	}
}

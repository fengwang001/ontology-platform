package scope

import (
	"testing"
	"time"
)

// 语义 6：钩子按注册逆序（LIFO）执行，每个恰好一次。
func TestHooksRunLIFOOnce(t *testing.T) {
	clk := newFakeClock()
	root := NewRoot(clk.now, time.Time{})

	var order []int
	root.OnDone(func() { order = append(order, 1) })
	root.OnDone(func() { order = append(order, 2) })
	root.OnDone(func() { order = append(order, 3) })

	root.Cancel("")

	if len(order) != 3 || order[0] != 3 || order[1] != 2 || order[2] != 1 {
		t.Fatalf("hooks LIFO order = %v, want [3 2 1]", order)
	}

	// 再次注册：已结束时立即执行，旧钩子不重复跑。
	root.OnDone(func() { order = append(order, 4) })
	if len(order) != 4 || order[3] != 4 {
		t.Fatalf("post-end hook order = %v", order)
	}

	root.Tick()
	root.Cancel("again")
	if len(order) != 4 {
		t.Fatalf("hooks must run exactly once, got %d executions", len(order))
	}
}

// 语义 6：单个钩子 panic 不得中断其余钩子，也不得让进程崩溃。
func TestHookPanicDoesNotBreakOthers(t *testing.T) {
	clk := newFakeClock()
	root := NewRoot(clk.now, time.Time{})

	var ranAfter, ranBefore bool
	root.OnDone(func() { ranAfter = true })
	root.OnDone(func() { panic("boom in hook") })
	root.OnDone(func() { ranBefore = true })

	root.Cancel("") // 不得把 panic 透出来

	if !ranBefore || !ranAfter {
		t.Fatalf("hooks around panicking hook must still run: before=%v after=%v", ranBefore, ranAfter)
	}

	// 在已结束作用域上注册一个会 panic 的钩子，也必须被吞掉。
	root.OnDone(func() { panic("late boom") })
}

// 语义 5/6：已结束父亲派生出的子作用域，其钩子在结束时照样 LIFO 执行。
func TestHooksOnAncestorEndedChild(t *testing.T) {
	clk := newFakeClock()
	root := NewRoot(clk.now, time.Time{})

	var got []string
	// 派生时未结束，先注册钩子；再由根连坐。
	mid := root.Child(time.Time{})
	mid.OnDone(func() { got = append(got, "a") })
	mid.OnDone(func() { got = append(got, "b") })

	root.Cancel("x")

	if len(got) != 2 || got[0] != "b" || got[1] != "a" {
		t.Fatalf("propagated hooks LIFO = %v", got)
	}
}

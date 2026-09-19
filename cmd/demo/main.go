// Command demo 对 scope 包的 8 条语义逐条做运行期自检并打印 OK/FAIL。
// 始终以退出码 0 结束；判定结果只体现在输出中。
package main

import (
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	"ontology/scope"
)

type checker struct {
	lines []string
}

func (c *checker) check(name string, ok bool, detail string) {
	tag := "OK  "
	if !ok {
		tag = "FAIL"
	}
	line := tag + name
	if !ok && detail != "" {
		line += " -- " + detail
	}
	c.lines = append(c.lines, line)
}

type fakeClock struct{ t atomic.Int64 }

func newClock() *fakeClock {
	c := &fakeClock{}
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC).UnixNano()
	c.t.Store(base)
	return c
}

func (c *fakeClock) now() time.Time      { return time.Unix(0, c.t.Load()) }
func (c *fakeClock) add(d time.Duration) { c.t.Add(int64(d)) }

func main() {
	c := &checker{}

	// 1. 截止时间只能收紧
	{
		clk := newClock()
		base := clk.now()
		root := scope.NewRoot(clk.now, base.Add(10*time.Second))
		later := root.Child(base.Add(20 * time.Second))
		inherited := root.Child(time.Time{})
		c.check("1 deadline only tightens",
			later.Deadline().Equal(base.Add(10*time.Second)) &&
				inherited.Deadline().Equal(base.Add(10*time.Second)),
			fmt.Sprintf("later=%v inherited=%v", later.Deadline(), inherited.Deadline()))
	}

	// 2. 三类原因可用 errors.Is 判定
	{
		clk := newClock()
		base := clk.now()
		r1 := scope.NewRoot(clk.now, base.Add(time.Second))
		clk.add(time.Second)
		r1.Tick()
		r2 := scope.NewRoot(clk.now, time.Time{})
		r2.Cancel("x")
		r3 := scope.NewRoot(clk.now, time.Time{})
		child := r3.Child(time.Time{})
		r3.Cancel("x")
		c.check("2 three distinguishable reasons",
			errors.Is(r1.Err(), scope.ErrDeadlineExceeded) &&
				errors.Is(r2.Err(), scope.ErrCanceled) &&
				errors.Is(child.Err(), scope.ErrAncestorEnded),
			fmt.Sprintf("%v / %v / %v", r1.Err(), r2.Err(), child.Err()))
	}

	// 3. 归因到最早触发者
	{
		clk := newClock()
		root := scope.NewRoot(clk.now, time.Time{})
		mid := root.Child(time.Time{})
		leaf := mid.Child(time.Time{})
		root.Cancel("the-origin")
		c.check("3 origin & reason attribution",
			leaf.Origin() == root && leaf.Reason() == "the-origin",
			fmt.Sprintf("origin-is-root=%v reason=%q", leaf.Origin() == root, leaf.Reason()))
	}

	// 4. 首个原因不可改写
	{
		clk := newClock()
		base := clk.now()
		root := scope.NewRoot(clk.now, base.Add(time.Second))
		root.Cancel("first")
		root.Cancel("second")
		clk.add(time.Minute)
		root.Tick()
		c.check("4 first cause is immutable",
			errors.Is(root.Err(), scope.ErrCanceled) && root.Reason() == "first",
			fmt.Sprintf("err=%v reason=%q", root.Err(), root.Reason()))
	}

	// 5. 已结束父亲上派生：立即连坐结束，钩子恰好一次
	{
		clk := newClock()
		root := scope.NewRoot(clk.now, time.Time{})
		root.Cancel("boom")
		var calls int
		late := root.Child(time.Time{})
		done := false
		select {
		case <-late.Done():
			done = true
		default:
		}
		late.OnDone(func() { calls++ })
		c.check("5 child on ended parent",
			done && errors.Is(late.Err(), scope.ErrAncestorEnded) &&
				late.Origin() == root && calls == 1,
			fmt.Sprintf("done=%v err=%v calls=%d", done, late.Err(), calls))
	}

	// 6. 钩子 LIFO、恰好一次、panic 隔离
	{
		clk := newClock()
		root := scope.NewRoot(clk.now, time.Time{})
		var order []int
		root.OnDone(func() { order = append(order, 1) })
		root.OnDone(func() { panic("ignored") })
		root.OnDone(func() { order = append(order, 2) })
		root.Cancel("")
		root.OnDone(func() { order = append(order, 3) })
		lifo := len(order) == 3 && order[0] == 2 && order[1] == 1 && order[2] == 3
		c.check("6 hooks LIFO once, panic-safe", lifo,
			fmt.Sprintf("order=%v", order))
	}

	// 7. 结束传播到所有后代，Done 只关一次
	{
		clk := newClock()
		root := scope.NewRoot(clk.now, time.Time{})
		var leaves []*scope.Scope
		mid := root.Child(time.Time{})
		for range 5 {
			leaves = append(leaves, mid.Child(time.Time{}))
		}
		root.Cancel("p")
		all := true
		for _, leaf := range leaves {
			select {
			case <-leaf.Done():
			default:
				all = false
			}
			if !errors.Is(leaf.Err(), scope.ErrAncestorEnded) {
				all = false
			}
		}
		root.Cancel("again") // 不 panic 即说明 Done 未重复关闭
		c.check("7 propagate to all descendants", all, "")
	}

	// 8. 只走注入时钟，左闭右开
	{
		clk := newClock()
		base := clk.now()
		root := scope.NewRoot(clk.now, base.Add(10*time.Second))
		clk.add(9 * time.Second)
		root.Tick()
		open := root.Err() == nil
		clk.add(time.Second)
		root.Tick()
		c.check("8 injected clock, [start,end)",
			open && errors.Is(root.Err(), scope.ErrDeadlineExceeded),
			fmt.Sprintf("open-at-9s=%v final-err=%v", open, root.Err()))
	}

	fmt.Println("scope semantics self-check:")
	fmt.Println(strings.Join(c.lines, "\n"))
}

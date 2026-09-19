package main

import (
	"errors"
	"fmt"
	"time"

	"ontology/scope"
)

type clock struct{ now time.Time }

func (c *clock) Now() time.Time          { return c.now }
func (c *clock) Advance(d time.Duration) { c.now = c.now.Add(d) }

func closed(s *scope.Scope) bool {
	select {
	case <-s.Done():
		return true
	default:
		return false
	}
}

func report(name string, ok bool) {
	mark := "OK"
	if !ok {
		mark = "FAIL"
	}
	fmt.Printf("%-28s %s\n", name, mark)
}

func main() {
	base := time.Date(2026, 9, 20, 0, 0, 0, 0, time.UTC)
	clk := &clock{now: base}

	// 1. 截止时间只能收紧
	root := scope.NewRoot(clk.Now, base.Add(10*time.Second))
	ok1 := root.Child(base.Add(time.Minute)).Deadline().Equal(root.Deadline()) &&
		root.Child(time.Time{}).Deadline().Equal(root.Deadline()) &&
		root.Child(base.Add(3*time.Second)).Deadline().Equal(base.Add(3*time.Second))
	report("1 deadline only tightens", ok1)

	// 2. 三类原因可 errors.Is 判定
	timed := scope.NewRoot(clk.Now, base)
	timed.Tick()
	canceled := scope.NewRoot(clk.Now, time.Time{})
	canceled.Cancel("r")
	orphan := canceled.Child(time.Time{})
	ok2 := errors.Is(timed.Err(), scope.ErrDeadlineExceeded) &&
		errors.Is(canceled.Err(), scope.ErrCanceled) &&
		errors.Is(orphan.Err(), scope.ErrAncestorEnded)
	report("2 three error kinds", ok2)

	// 3. 归因到最早触发者
	r3 := scope.NewRoot(clk.Now, time.Time{})
	deep := r3.Child(time.Time{}).Child(time.Time{}).Child(time.Time{})
	r3.Cancel("root gone")
	report("3 origin attribution", deep.Origin() == r3 && deep.Reason() == "root gone")

	// 4. 首个原因不可改写
	r4 := scope.NewRoot(clk.Now, base.Add(time.Hour))
	r4.Cancel("first")
	r4.Cancel("second")
	clk.Advance(2 * time.Hour)
	r4.Tick()
	ok4 := r4.Reason() == "first" && errors.Is(r4.Err(), scope.ErrCanceled) && r4.Origin() == r4
	report("4 first cause wins", ok4)

	// 5. 已结束的父亲上派生
	n5 := 0
	orphan.OnDone(func() { n5++ })
	ok5 := closed(orphan) && errors.Is(orphan.Err(), scope.ErrAncestorEnded) &&
		orphan.Origin() == canceled && n5 == 1
	report("5 child of ended scope", ok5)

	// 6. 钩子 LIFO、恰好一次、panic 安全
	r6 := scope.NewRoot(clk.Now, time.Time{})
	var order []int
	r6.OnDone(func() { order = append(order, 1) })
	r6.OnDone(func() { panic("boom") })
	r6.OnDone(func() { order = append(order, 3) })
	r6.Cancel("x")
	ok6 := len(order) == 2 && order[0] == 3 && order[1] == 1
	report("6 hooks LIFO panic-safe", ok6)

	// 7. 传播到所有后代
	r7 := scope.NewRoot(clk.Now, time.Time{})
	mid := r7.Child(time.Time{})
	leaf := mid.Child(time.Time{})
	r7.Cancel("all")
	ok7 := closed(r7) && closed(mid) && closed(leaf) &&
		errors.Is(leaf.Err(), scope.ErrAncestorEnded) && leaf.Origin() == r7
	report("7 propagation to all", ok7)

	// 8. 注入时钟，左闭右开
	clk8 := &clock{now: base}
	r8 := scope.NewRoot(clk8.Now, base.Add(time.Second))
	clk8.Advance(time.Second - time.Nanosecond)
	r8.Tick()
	early := closed(r8)
	clk8.Advance(time.Nanosecond)
	r8.Tick()
	report("8 injected clock half-open", !early && closed(r8))
}

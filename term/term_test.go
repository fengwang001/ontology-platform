package term

import (
	"sync"
	"testing"
)

func TestTermBasic(t *testing.T) {
	cases := []int{1, 8, 1000}
	for _, n := range cases {
		tm := New()
		var wg sync.WaitGroup
		for i := 0; i < n; i++ {
			tm.Add()
			wg.Add(1)
			go func() { defer wg.Done(); tm.Done() }()
		}
		wg.Wait()
		tm.Wait()
		if c := tm.Count(); c != 0 {
			t.Fatalf("n=%d count=%d", n, c)
		}
	}
}

// TestTermRecursive 模拟「任务在自己 Done 前先 Add 子任务」：
// 若计数在开始执行时加、返回时减，子任务入队后计数会错误归零导致提前终止。
func TestTermRecursive(t *testing.T) {
	for round := 0; round < 200; round++ {
		tm := New()
		done := make(chan struct{})
		var run func(depth int)
		run = func(depth int) {
			defer tm.Done()
			if depth == 0 {
				return
			}
			tm.Add() // 正确规则：入队前加，而不是开始执行时加
			go run(depth - 1)
		}
		tm.Add()
		go func() { run(16); close(done) }()
		tm.Wait()
		select {
		case <-done:
		default:
			t.Fatalf("round %d: Wait returned before run finished", round)
		}
		if c := tm.Count(); c != 0 {
			t.Fatalf("round %d: count=%d", round, c)
		}
	}
}

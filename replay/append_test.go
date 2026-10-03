package replay

import (
	"errors"
	"testing"

	"ontology/code"
	"ontology/history"
)

func TestAppendCapacityDuringRun(t *testing.T) {
	// 代码项数上限 1000，单个 Branch 的 O 至多 1000 项。构造合法代码：
	// 1 个顶层 z + 99 个 Branch（O 各 1000 个 z，下一事件为 S 故走 O）+
	// 最后一个 Branch 的 O 为 1000 个 z（消费 997 个后历史耗尽，续跑 3 个）。
	// 消费总数 1 + 99*1000 + 997 = 99998，追加 3 条后超过上限。
	store := &history.Store{}
	wf := []byte("cap")
	const preLen = history.MaxEventsPerWorkflow - 2
	pre := make([]history.Event, preLen)
	for i := range pre {
		pre[i] = history.Step([]byte("z"))
	}
	if err := store.Append(wf, 0, pre); err != nil {
		t.Fatalf("prefill: %v", err)
	}
	r := NewRunner(store)
	oFull := make([]code.Item, code.MaxBranchItems)
	for i := range oFull {
		oFull[i] = code.Step([]byte("z"))
	}
	prog := code.Code{code.Step([]byte("z"))}
	for i := 0; i < 99; i++ {
		pid := []byte{'p', byte('a' + i/26), byte('a' + i%26)}
		prog = append(prog, code.Branch(pid, []code.Item{code.Step([]byte("n"))}, oFull))
	}
	prog = append(prog, code.Branch([]byte("pzz"), []code.Item{code.Step([]byte("n"))}, oFull))
	_, _, err := r.Run(wf, prog)
	if !errors.Is(err, history.ErrCapacity) {
		t.Fatalf("want ErrCapacity, got %v", err)
	}
	if got := len(store.Snapshot(wf)); got != preLen {
		t.Fatalf("history changed after ErrCapacity: len=%d", got)
	}
}

func TestComparisonCounter(t *testing.T) {
	store := &history.Store{}
	r := NewRunner(store)

	wf := []byte("counted")
	if _, _, err := r.Run(wf, v2()); err != nil { // 续跑 4
		t.Fatalf("run1: %v", err)
	}
	if c := r.comparisons(wf); c != 4 {
		t.Fatalf("counter after continuation = %d, want 4", c)
	}
	if _, _, err := r.Run(wf, v2()); err != nil { // 消费 4，续跑 0
		t.Fatalf("run2: %v", err)
	}
	if c := r.comparisons(wf); c != 8 {
		t.Fatalf("counter after replay = %d, want 8", c)
	}

	// 其他工作流的运行不影响本工作流计数。
	if _, _, err := r.Run([]byte("other"), v2()); err != nil {
		t.Fatalf("other run: %v", err)
	}
	if c := r.comparisons(wf); c != 8 {
		t.Fatalf("counter must not move with other workflows, got %d", c)
	}

	// 失败的 Run 不增加计数。
	badHist := []history.Event{history.Step([]byte("a")), history.Step([]byte("x"))}
	s2, w2 := seed(t, "counted-fail", badHist)
	r2 := NewRunner(s2)
	if _, _, err := r2.Run(w2, v2()); !errors.Is(err, ErrMismatch) {
		t.Fatalf("want mismatch, got %v", err)
	}
	if c := r2.comparisons(w2); c != 0 {
		t.Fatalf("failed run must not count, got %d", c)
	}
}

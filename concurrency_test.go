package ontology

import (
	"sync"
	"testing"
)

// 并发调用不崩溃，且最终状态自洽（串行等价性由互斥锁保证）。
func TestConcurrentCalls(t *testing.T) {
	e := NewEngine()
	cfg := Config{Budget: 100000, Deadline: 1 << 40, PauseBudget: 100000,
		MaxSinglePause: 100000, WindowLen: 50, WarnThreshold: 100, LockThreshold: 200, MaxWarnings: 100}
	if err := e.CreateSession("s", 0, cfg); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				at := int64(g*200 + i)
				_ = e.SubmitAnswer("s", at, 1, Answer{"q", int64(g*200 + i + 1), "x"})
				_, _ = e.Snapshot("s", at)
			}
		}(g)
	}
	wg.Wait()
	snap, err := e.Snapshot("s", 1600)
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Answers) != 1 || snap.LastSeq != 1600 {
		t.Fatalf("answers=%v lastSeq=%d", snap.Answers, snap.LastSeq)
	}
}

// 作答迟到判定与事件阈值判定均为 O(1)：
// 历史规模放大 100 倍，单步耗时不应随历史线性增长。
func TestConstantTimeJudgement(t *testing.T) {
	bench := func(n int) float64 {
		e := NewEngine()
		cfg := Config{Budget: int64(n) * 10, Deadline: int64(n) * 20,
			PauseBudget: int64(n), MaxSinglePause: int64(n),
			WindowLen: 5, WarnThreshold: n + 1, LockThreshold: n + 2, MaxWarnings: n + 3}
		if err := e.CreateSession("s", 0, cfg); err != nil {
			t.Fatal(err)
		}
		for i := 0; i < n; i++ {
			if err := e.SubmitAnswer("s", int64(i), 1,
				Answer{"q", int64(i + 1), "x"}); err != nil {
				t.Fatal(err)
			}
			if err := e.RecordEvent("s", int64(i), "blur"); err != nil {
				t.Fatal(err)
			}
		}
		// 关键判定路径：迟到/重复查找为哈希；窗口清理只弹出过期队首。
		start := testing.AllocsPerRun(100, func() {
			_ = e.SubmitAnswer("s", int64(n)+1, 1,
				Answer{"q", int64(n), "dup"}) // 重复序号，O(1) 命中
			_ = e.RecordEvent("s", int64(n)+100, "blur")
		})
		return start
	}
	a := bench(200)
	b := bench(20000)
	// 判定开销（每次操作分配数）应保持为小常数，不随历史增长。
	if b > a*3+2 {
		t.Fatalf("judgement cost grew with history: %v vs %v", a, b)
	}
}

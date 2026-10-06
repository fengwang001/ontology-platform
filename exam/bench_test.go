package exam

import (
	"sync"
	"testing"
)

func benchConfig() Config {
	return Config{
		AnswerBudget:   1 << 50,
		PauseBudget:    10,
		SinglePauseMax: 20,
		Window:         100,
		WarnThreshold:  1 << 30,
		LockThreshold:  1 << 30,
		WarnLimit:      1 << 30,
	}
}

// 作答落定与迟到判定的开销不随已接受作答总数增长。
func BenchmarkAnswerLanding(b *testing.B) {
	e, err := NewEngine(benchConfig())
	if err != nil {
		b.Fatal(err)
	}
	if _, err := e.StartSession("s", 0, 1<<50); err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := e.SubmitAnswer("s", int64(i), Answer{QuestionID: "q", Seq: int64(i), Generation: 1, Payload: "v"}); err != nil {
			b.Fatal(err)
		}
	}
}

// 窗口阈值判定的开销不随历史事件总数增长。
func BenchmarkEventWindow(b *testing.B) {
	e, err := NewEngine(benchConfig())
	if err != nil {
		b.Fatal(err)
	}
	if _, err := e.StartSession("s", 0, 1<<50); err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := e.RecordEvent("s", int64(i), EventLeavePage, 0); err != nil {
			b.Fatal(err)
		}
	}
}

// 并发调用冒烟测试：结果等价于某个串行顺序，
// 每题最终作答是该题已接受作答中序号最大者。
func TestConcurrentSmoke(t *testing.T) {
	e, err := NewEngine(benchConfig())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.StartSession("s", 0, 1<<50); err != nil {
		t.Fatal(err)
	}
	const workers = 8
	const perWorker = 500
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < perWorker; i++ {
				seq := int64(w*perWorker + i)
				_ = e.SubmitAnswer("s", seq, Answer{QuestionID: "q", Seq: seq, Generation: 1, Payload: "v"})
				_ = e.RecordEvent("s", seq, EventLeavePage, 0)
			}
		}(w)
	}
	wg.Wait()
	s := e.sessions["s"]
	if got := s.maxSeq["q"]; got != workers*perWorker-1 {
		t.Fatalf("maxSeq=%d, want %d", got, workers*perWorker-1)
	}
}

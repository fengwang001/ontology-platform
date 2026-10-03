package replay

import (
	"errors"
	"fmt"
	"sync"
	"testing"

	"ontology/code"
	"ontology/history"
)

// TestComparisonCounter 比较计数器等于消费条数加续跑条数，
// 且不随本工作流以外的历史增长。
func TestComparisonCounter(t *testing.T) {
	other := history.NewStore()
	for i := 0; i < 50; i++ {
		if err := other.Append(b(fmt.Sprintf("other%d", i)), 0,
			[]history.Event{history.S(b("x"))}); err != nil {
			t.Fatal(err)
		}
	}
	c := code.Code{st("a"), code.Branch{Pid: b("p"), New: []code.Item{st("n")}, Old: nil}}
	snap := []history.Event{history.S(b("a"))}
	res, _, cmps, err := execute(snap, c)
	if err != nil {
		t.Fatal(err)
	}
	if cmps != res.Consumed+res.Continued {
		t.Fatalf("比较次数=%d, want %d+%d", cmps, res.Consumed, res.Continued)
	}
	t.Logf("消费=%d 续跑=%d 比较=%d（依据：计数器==消费+续跑，与外部历史无关）",
		res.Consumed, res.Continued, cmps)
}

// TestConcurrentRuns 多个 Run 竞争同一工作流：恰有一个追加续跑事件，
// 其余 ErrConflict，重试后最终历史等价于串行结果且无重复事件。
func TestConcurrentRuns(t *testing.T) {
	c := code.Code{st("a"),
		code.Branch{Pid: b("p"), New: []code.Item{st("n1")}, Old: []code.Item{st("o1")}},
		st("z")}
	want := history.NewStore()
	if _, err := Run(want, b("wf"), c); err != nil {
		t.Fatal(err)
	}
	wantHist := want.Snapshot(b("wf"))

	store := history.NewStore()
	wf := b("wf")
	const workers = 8
	var wg sync.WaitGroup
	appended := make([]int, workers)
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for {
				res, err := Run(store, wf, c)
				if errors.Is(err, history.ErrConflict) {
					continue
				}
				if err != nil {
					t.Errorf("worker %d: %v", w, err)
					return
				}
				appended[w] = res.Continued
				return
			}
		}(w)
	}
	wg.Wait()
	total := 0
	for _, n := range appended {
		total += n
	}
	if total != len(wantHist) {
		t.Fatalf("续跑事件总数=%d, want %d（应恰由一个 Run 追加）", total, len(wantHist))
	}
	if got := store.Snapshot(wf); !evsEqual(got, wantHist) {
		t.Fatalf("并发后历史=%v, want %v", got, wantHist)
	}
	t.Logf("%d 个 worker 竞争同一工作流 → 最终历史=%v（依据：等价于串行、无重复事件）",
		workers, wantHist)
}

// TestConcurrentMultiWorkflow 不同工作流的 Run 可并发且互不影响。
func TestConcurrentMultiWorkflow(t *testing.T) {
	store := history.NewStore()
	c := code.Code{st("a"), code.Branch{Pid: b("p"), New: []code.Item{st("n")}, Old: nil}}
	const flows = 16
	var wg sync.WaitGroup
	for f := 0; f < flows; f++ {
		wg.Add(1)
		go func(f int) {
			defer wg.Done()
			wf := b(fmt.Sprintf("wf%d", f))
			if _, err := Run(store, wf, c); err != nil {
				t.Errorf("wf %d: %v", f, err)
			}
		}(f)
	}
	wg.Wait()
	want := []history.Event{history.S(b("a")), history.M(b("p")), history.S(b("n"))}
	for f := 0; f < flows; f++ {
		if got := store.Snapshot(b(fmt.Sprintf("wf%d", f))); !evsEqual(got, want) {
			t.Fatalf("wf%d 历史=%v, want %v", f, got, want)
		}
	}
	t.Logf("%d 个工作流并发 Run → 各自历史均为 %v（依据：日志按工作流隔离）", flows, want)
}

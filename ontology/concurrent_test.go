package ontology

import (
	"fmt"
	"sync"
	"testing"
)

// 单个剩余名额：任意数量并发更新必须恰好一方成功，其余因基数不满足被拒绝，
// 名额不得被重复占用，也不得出现“全部被拒绝但名额仍空”。
func TestConcurrentSingleSlotExactlyOneWinner(t *testing.T) {
	for _, goroutines := range []int{2, 8, 32} {
		t.Run(fmt.Sprintf("G=%d", goroutines), func(t *testing.T) {
			env := newConstrainedEnv(t, 5, nil, 1)
			// 不留预占：名额初始空闲。所有请求以基线 0 并发抢占唯一名额。

			start := make(chan struct{})
			var wg sync.WaitGroup
			results := make([]*Result, goroutines)
			wg.Add(goroutines)
			for i := 0; i < goroutines; i++ {
				i := i
				go func() {
					defer wg.Done()
					other := fmt.Sprintf("c%02d", i)
					if err := env.store.CreateInstance(other); err != nil {
						// 并发创建同名不会发生（名字不同）；实例存在性检查在引擎外完成。
						t.Error(err)
						return
					}
					<-start
					res, err := env.eng.Update(Request{
						Instance: "x",
						Baseline: 0,
						Ops:      []Op{{TypeID: testLinkType, Side: SideA, Other: other, Add: true}},
					})
					if err != nil {
						t.Error(err)
						return
					}
					results[i] = res
				}()
			}
			close(start)
			wg.Wait()

			commits, cardinality, exhausted := 0, 0, 0
			for _, r := range results {
				switch {
				case r.Committed:
					commits++
				case r.Reject.Code == CodeCardinality:
					cardinality++
				case r.Reject.Code == CodeRetriesExhausted:
					exhausted++
				}
			}
			if commits != 1 {
				t.Fatalf("want exactly 1 winner, got commits=%d card=%d exhausted=%d", commits, cardinality, exhausted)
			}
			if commits+cardinality+exhausted != goroutines {
				t.Fatal("every request must terminate with a definitive outcome")
			}
			links, err := env.store.LinkSnapshot("x")
			if err != nil {
				t.Fatal(err)
			}
			if len(links) != 1 {
				t.Fatalf("slot must not be double-occupied nor left empty, links=%+v", links)
			}
		})
	}
}

// 高竞争 + 小预算：连续被后来者抢先的次数受预算严格约束；
// 每个请求最多留下 maxAttempts 条尝试记录，且终态分类合法。
func TestConcurrentBoundedRetriesNoStarvation(t *testing.T) {
	const budget = 3
	env := newConstrainedEnv(t, budget, nil, 1000)
	const g = 40
	start := make(chan struct{})
	var wg sync.WaitGroup
	all := make([]*Result, g)
	wg.Add(g)
	for i := 0; i < g; i++ {
		i := i
		go func() {
			defer wg.Done()
			other := fmt.Sprintf("q%02d", i)
			_ = env.store.CreateInstance(other)
			<-start
			r, err := env.eng.Update(Request{
				Instance: "x", Baseline: 0,
				Ops: []Op{{TypeID: testLinkType, Side: SideA, Other: other, Add: true}},
			})
			if err != nil {
				t.Error(err)
				return
			}
			all[i] = r
		}()
	}
	close(start)
	wg.Wait()

	commits := 0
	for i, r := range all {
		if r == nil {
			t.Fatalf("goroutine %d produced no result", i)
		}
		if len(r.Attempts) > budget {
			t.Fatalf("goroutine %d exceeded budget: %d attempts", i, len(r.Attempts))
		}
		for _, a := range r.Attempts {
			if a.Outcome > OutcomeCommitted {
				t.Fatalf("invalid outcome %v", a.Outcome)
			}
		}
		if r.Committed {
			commits++
			continue
		}
		switch r.Reject.Code {
		case CodeCardinality, CodeRetriesExhausted:
		default:
			t.Fatalf("unexpected rejection %s", r.Reject.Code)
		}
	}
	n, _ := env.store.LinkCount("x")
	if n != commits {
		t.Fatalf("visible links (%d) must equal committed calls (%d)", n, commits)
	}
	if n > 1000 {
		t.Fatalf("cardinality ceiling breached: %d", n)
	}
}

package replay

import (
	"errors"
	"fmt"
	"sync"
	"testing"

	"ontology/code"
	"ontology/history"
)

// 同一工作流上多个 Run 并发，全部从空历史快照开始：恰有一个追加成功，
// 其余得到 ErrConflict，历史无重复/无丢失。失败者重试直到成功，
// 最终历史必须与一次串行续跑完全相同。
func TestConcurrentRunSameWorkflow(t *testing.T) {
	store := &history.Store{}
	// 900 步的重续跑：拉大“快照→追加”窗口，使多协程大概率看到同一份空快照。
	heavy := make(code.Code, 900)
	for i := range heavy {
		heavy[i] = code.Step([]byte("s"))
	}

	const goroutines = 24
	const rounds = 20
	totalConflicts := 0
	for round := 0; round < rounds; round++ {
		wf := []byte(fmt.Sprintf("race-same-%d", round))
		var wg sync.WaitGroup
		var mu sync.Mutex
		appenders, conflicts, others := 0, 0, 0

		start := make(chan struct{})
		wg.Add(goroutines)
		for i := 0; i < goroutines; i++ {
			go func() {
				defer wg.Done()
				<-start // 就绪后同时起跑，尽量看到同一份空快照
				for {
					r := NewRunner(store)
					_, cont, err := r.Run(wf, heavy)
					switch {
					case err == nil:
						mu.Lock()
						if cont > 0 {
							appenders++
						}
						mu.Unlock()
						return
					case errors.Is(err, history.ErrConflict):
						mu.Lock()
						conflicts++
						mu.Unlock()
						// 重新 Run：先写入者的事件被消费，续跑只补其余部分。
					default:
						mu.Lock()
						others++
						t.Errorf("round %d: unexpected error: %v", round, err)
						mu.Unlock()
						return
					}
				}
			}()
		}
		close(start)
		wg.Wait()

		if appenders != 1 {
			t.Fatalf("round %d: exactly one append expected, got %d", round, appenders)
		}
		if others != 0 {
			t.Fatalf("round %d: unexpected errors: %d", round, others)
		}
		totalConflicts += conflicts

		final := store.Snapshot(wf)
		if len(final) != len(heavy) {
			t.Fatalf("round %d: final len=%d, want %d", round, len(final), len(heavy))
		}

		// 收敛后再用同代码 Run：消费全部、零续跑。
		r := NewRunner(store)
		consumed, cont, err := r.Run(wf, heavy)
		if err != nil || consumed != len(heavy) || cont != 0 {
			t.Fatalf("round %d: post-race run = (%d,%d,%v)", round, consumed, cont, err)
		}
	}
	if totalConflicts == 0 {
		t.Fatalf("expected at least one ErrConflict across %d rounds", rounds)
	}
	t.Logf("races: rounds=%d goroutines/round=%d total_conflicts=%d",
		rounds, goroutines, totalConflicts)
}

// 不同工作流的 Run 并发互不干扰：每个工作流都独立产生自己的完整历史。
func TestConcurrentRunDifferentWorkflows(t *testing.T) {
	store := &history.Store{}
	prog := v2()

	const workflows = 32
	var wg sync.WaitGroup
	errCh := make(chan error, workflows)
	wg.Add(workflows)
	for i := 0; i < workflows; i++ {
		i := i
		go func() {
			defer wg.Done()
			wf := []byte("wf-" + string(rune('A'+i%26)) + string(rune('a'+i/26)))
			r := NewRunner(store)
			_, _, err := r.Run(wf, prog)
			errCh <- err
		}()
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		if err != nil {
			t.Fatalf("independent workflow run: %v", err)
		}
	}

	for i := 0; i < workflows; i++ {
		wf := []byte("wf-" + string(rune('A'+i%26)) + string(rune('a'+i/26)))
		got := store.Snapshot(wf)
		if len(got) != 4 {
			t.Fatalf("workflow %s: len=%d, want 4", wf, len(got))
		}
	}
}

package saga

import (
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"

	"ontology/step"
)

func TestConcurrentInstances(t *testing.T) {
	const N = 24
	const M = 16
	o, _ := newO(t, Config{})

	expectedFwd := make([]int, N)
	expectedCmp := make([]int, N)

	// 先注册全部实例（带定义、无记录），消除查询者看到「不存在」的时间窗。
	type planned struct {
		steps []step.Step
		c     *counters
	}
	plans := make([]planned, N)
	for g := 0; g < N; g++ {
		c := newCounters("a", "b", "c", "d")
		steps := []step.Step{
			mkStep("a", c, nil, nil, g%2 == 0),
			mkStep("b", c, nil, nil, false),
			mkStep("c", c, nil, nil, false),
		}
		if g%2 == 0 {
			steps = append(steps, mkStep("d", c, defFail(), nil, false))
			expectedCmp[g] = 3
		} else {
			steps = append(steps, mkStep("d", c, nil, nil, false))
		}
		expectedFwd[g] = 4
		plans[g] = planned{steps: steps, c: c}
		it := &inst{
			steps: steps,
			calls: Calls{Forward: map[string]int{}, Compensate: map[string]int{}},
		}
		if !o.register(fmt.Sprintf("inst-%d", g), it) {
			t.Fatalf("register inst %d", g)
		}
	}

	var runners sync.WaitGroup
	for g := 0; g < N; g++ {
		g := g
		runners.Add(1)
		go func() {
			defer runners.Done()
			id := fmt.Sprintf("inst-%d", g)
			it := o.insts[id]
			if _, err := o.start(id, it); g%2 == 0 && err == nil {
				t.Errorf("inst %d expected forward error", g)
			}
		}()
	}

	// 查询者在运行全程反复读取；runners 全部完成后通过 stop 收尾。
	stop := make(chan struct{})
	var queryWG sync.WaitGroup
	for q := 0; q < M; q++ {
		queryWG.Add(1)
		go func() {
			defer queryWG.Done()
			for {
				select {
				case <-stop:
					return
				default:
					for g := 0; g < N; g++ {
						if _, err := o.State(fmt.Sprintf("inst-%d", g)); err != nil {
							t.Errorf("state query: %v", err)
						}
					}
				}
			}
		}()
	}

	runners.Wait()
	close(stop)
	queryWG.Wait()
	for g := 0; g < N; g++ {
		id := fmt.Sprintf("inst-%d", g)
		if err := o.SelfCheck(id); err != nil {
			t.Fatalf("SelfCheck %s: %v", id, err)
		}
		st, err := o.State(id)
		if err != nil {
			t.Fatal(err)
		}
		calls, err := o.Calls(id)
		if err != nil {
			t.Fatal(err)
		}
		if calls.ForwardAll != expectedFwd[g] {
			t.Fatalf("inst %d fwd=%d want %d", g, calls.ForwardAll, expectedFwd[g])
		}
		if calls.CompensateAll != expectedCmp[g] {
			t.Fatalf("inst %d cmp=%d want %d", g, calls.CompensateAll, expectedCmp[g])
		}
		if g%2 == 0 && st.Status != Compensated {
			t.Fatalf("inst %d status=%s want compensated", g, st.Status)
		}
		if g%2 == 1 && st.Status != Succeeded {
			t.Fatalf("inst %d status=%s want succeeded", g, st.Status)
		}
	}
}

func TestConcurrentResumeWhileRunning(t *testing.T) {
	o, _ := newO(t, Config{})
	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	var seenRunning atomic.Int32
	c := newCounters("b")
	steps := []step.Step{
		{Key: "a", Forward: func() error {
			entered <- struct{}{}
			<-release
			return nil
		}, Compensate: func() error { return nil }},
		mkStep("b", c, nil, nil, false),
	}

	runDone := make(chan struct{})
	go func() {
		_, _ = o.Run("s1", steps) // Run 持锁直到全部步骤完成
		close(runDone)
	}()
	<-entered // 第一步在 Run 持有的实例锁内暂停

	_, err := o.Resume("s1") // 此时 Run 必然仍在运行
	if errors.Is(err, ErrInstanceRunning) {
		seenRunning.Add(1)
	}
	close(release)
	<-runDone
	if seenRunning.Load() != 1 {
		t.Fatalf("concurrent Resume must yield ErrInstanceRunning, got %d", seenRunning.Load())
	}
}

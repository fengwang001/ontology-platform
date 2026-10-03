package activity

import (
	"sync"
	"testing"
)

// probeBudget 用一条只到 Running、无任何到期的 Status 验证：
// 0 个到期事件时探查恰为 1，且与无关活动数量无关（1 / 10000 两档）。
func TestProbeBudgetUnrelated(t *testing.T) {
	for _, n := range []int{1, 10000} {
		e := NewExecutor()
		for i := 0; i < n; i++ {
			id := []byte{byte(i / 251), byte(i % 251)}
			if err := e.Schedule(id, 0, 100, 0, 0, 3, 1, 10, 0); err != nil {
				t.Fatalf("n=%d schedule: %v", n, err)
			}
		}
		target := []byte{0, 0}
		startMust(t, e, target, 0)
		if _, err := e.Status(target, 50); err != nil {
			t.Fatal(err)
		}
		if e.probes != 1 {
			t.Fatalf("n=%d probes=%d want 1", n, e.probes)
		}
	}
}

func TestProbeBudgetEvents(t *testing.T) {
	// s2c=2, d0=1, sc=0：失败@2 -> wake@3 -> s2s 不存在（s2s=0），
	// 第 2 次在 3 排队；推到足够晚只产生 1 个失败事件 + 1 次 wake。
	e := NewExecutor()
	id := []byte("q")
	must(t, e.Schedule(id, 0, 2, 0, 1000, 3, 1, 10, 0))
	startMust(t, e, id, 0)
	st, _ := e.Status(id, 3)
	if st.State != Scheduled || st.Attempt != 2 {
		t.Fatalf("%+v", st)
	}
	if e.probes > 2 {
		t.Fatalf("probes=%d want <=2 (events+1)", e.probes)
	}
}

func TestConcurrentActivities(t *testing.T) {
	e := NewExecutor()
	var startWG, doneWG sync.WaitGroup
	const n = 64
	for i := 0; i < n; i++ {
		id := []byte{byte(i)}
		if err := e.Schedule(id, 0, 50, 0, 1000, 2, 1, 10, 0); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < n; i++ {
		startWG.Add(1)
		doneWG.Add(1)
		go func(i int) {
			defer doneWG.Done()
			id := []byte{byte(i)}
			if _, _, err := e.Start(id, 1); err != nil {
				t.Errorf("start: %v", err)
				startWG.Done()
				return
			}
			startWG.Done()
			// 等待所有活动进入 Running，再在 t=2 并发收尾。
			startWG.Wait()
			if err := e.Complete(id, 1, 2); err != nil {
				t.Errorf("complete: %v", err)
			}
		}(i)
	}
	doneWG.Wait()
}

func TestRejectedOpHasNoSideEffect(t *testing.T) {
	e := NewExecutor()
	id := []byte("r")
	must(t, e.Schedule(id, 0, 100, 0, 1000, 3, 1, 10, 0))
	// 时钟倒退：拒绝且不推进时钟。
	if _, _, err := e.Start(id, 0); err != nil {
		t.Fatal(err)
	}
	if _, _, err := e.Start(id, 5); !isErr(err, ErrState) {
		t.Fatalf("restart=%v", err)
	}
	// 到期当刻 HB 被拒后，时钟仍是 0；now=0 的合法 HB 仍可用，
	// 且 s2c 未因被拒操作而提前处理。
	must(t, e.Heartbeat(id, 1, 0, 3))
	st, _ := e.Status(id, 0)
	if st.State != Running || st.Attempt != 1 {
		t.Fatalf("side effect: %+v", st)
	}
}

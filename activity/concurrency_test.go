package activity

import (
	"strconv"
	"sync"
	"testing"
)

func TestConcurrentActivities(t *testing.T) {
	e := NewDiscard()
	cfg := Config{S2S: 0, S2C: 1_000_000_000, HB: 0, SC: 1_000_000_000, M: 3, D0: 5, Cap: 20}
	const n = 200
	// 先串行创建全部活动（t=0），避免并发交错中各操作时间戳与单调时钟冲突。
	for i := 0; i < n; i++ {
		id := []byte(strconv.Itoa(i))
		if err := e.Schedule(id, cfg, 0); err != nil {
			t.Fatalf("schedule %d: %v", i, err)
		}
	}
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		id := []byte(strconv.Itoa(i))
		wg.Add(1)
		go func() {
			defer wg.Done()
			// 同刻 Start(0) 对不同活动是合法的并发（等价于某一串行顺序）。
			if _, _, err := e.Start(id, 0); err != nil {
				t.Errorf("start: %v", err)
			}
		}()
	}
	wg.Wait()
	// 后续操作串行执行，使用严格递增的全局时刻，避免与单调时钟冲突。
	var tick int64 = 1
	for i := 0; i < n; i++ {
		id := []byte(strconv.Itoa(i))
		if err := e.Heartbeat(id, 1, tick, int64(i)); err != nil {
			t.Fatalf("hb %d: %v", i, err)
		}
		tick++
		if err := e.Complete(id, 1, tick); err != nil {
			t.Fatalf("complete %d: %v", i, err)
		}
		tick++
	}
	for i := 0; i < n; i++ {
		st, err := e.Status([]byte(strconv.Itoa(i)), tick)
		if err != nil {
			t.Fatal(err)
		}
		if st.State != StateTerminal || st.Terminal != TermCompleted {
			t.Fatalf("activity %d = %+v", i, st)
		}
	}
}

// 无关活动数量（1 与 10000）不影响单个活动的堆探针规模：堆是每活动独立、至多 4 项。
func TestProbeIndependentOfOtherActivities(t *testing.T) {
	measure := func(t *testing.T, others int) int {
		t.Helper()
		e := NewDiscard()
		cfg := Config{S2S: 5, S2C: 100, HB: 4, SC: 1000, M: 3, D0: 5, Cap: 20}
		for i := 0; i < others; i++ {
			id := []byte("o" + strconv.Itoa(i))
			if err := e.Schedule(id, cfg, 0); err != nil {
				t.Fatal(err)
			}
		}
		target := []byte("target")
		if err := e.Schedule(target, cfg, 0); err != nil {
			t.Fatal(err)
		}
		// now=5 恰为 s2s 到期：1 个到期事件，探针 = 2（事件 + 停止前 Peek 不需要，终局即止 → 1）。
		if _, err := e.Status(target, 5); err != nil {
			t.Fatal(err)
		}
		// Status 不回写探针；用一个写操作让 advance 落库后读取。
		if _, _, err := e.Start(target, 6); err != nil && err != ErrTerminal {
			t.Fatal(err)
		}
		probe, ok := e.probeOf(target)
		if !ok {
			t.Fatal("target missing")
		}
		return probe
	}
	p1 := measure(t, 0)
	p2 := measure(t, 10000)
	if p1 != p2 {
		t.Fatalf("probe differs with unrelated activities: %d vs %d", p1, p2)
	}
	// s2s 在 advance 首次 Peek 即终局返回，仅 1 次探测。
	if p1 != 1 {
		t.Fatalf("probe = %d, want 1", p1)
	}
}

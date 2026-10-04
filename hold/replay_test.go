package hold

import (
	"fmt"
	"sync"
	"testing"

	"ontology/routing"
)

func buildReplayWorld(t *testing.T, tag string) (*Manager, []func() error) {
	t.Helper()
	rm := routing.NewManager()
	if err := rm.Define("rt", 3, []int{1, 2, 2}, []bool{false, true, false}, 1); err != nil {
		t.Fatal(err)
	}
	wm, hm, err := Config(rm, 90, 50, func(string) bool { return true })
	if err != nil {
		t.Fatal(err)
	}
	wo := "wo" + tag
	if err := hm.Open(wo, "rt", 100); err != nil {
		t.Fatal(err)
	}
	ops := []func() error{
		func() error { return wm.Report(wo, 1, 0, 100, 0, 0) },
		func() error { return wm.Report(wo, 2, 0, 90, 4, 6) },
		func() error { return wm.Report(wo, 2, 1, 5, 1, 0) },
		func() error { return wm.Report(wo, 3, 0, 80, 0, 10) },
		func() error { return wm.Report(wo, 3, 1, 5, 0, 0) },
		func() error { return wm.Report(wo, 2, 1, 10, 0, 0) },
		func() error { return wm.Report(wo, 3, 1, 9, 1, 0) },
		func() error {
			_, err := wm.Close(wo)
			return err
		},
	}
	return hm, ops
}

func snapshotAll(t *testing.T, hm *Manager, tag string) string {
	t.Helper()
	s, err := hm.WIP().State("wo" + tag)
	if err != nil {
		t.Fatal(err)
	}
	return fmt.Sprintf("Q=%d done=%d scrap=%d closed=%v held=%v q=%v",
		s.Q, s.Done, s.Scrapped, s.Closed, s.Held, s.Queue)
}

func TestDeterministicReplay(t *testing.T) {
	hm1, ops1 := buildReplayWorld(t, "a")
	var s1 string
	for i, fn := range ops1 {
		if err := fn(); err != nil {
			t.Fatalf("run1 op %d: %v", i, err)
		}
	}
	s1 = snapshotAll(t, hm1, "a")

	hm2, ops2 := buildReplayWorld(t, "b")
	for i, fn := range ops2 {
		if err := fn(); err != nil {
			t.Fatalf("run2 op %d: %v", i, err)
		}
	}
	s2 := snapshotAll(t, hm2, "b")
	if s1 != s2 {
		t.Fatalf("replay mismatch:\n%s\n%s", s1, s2)
	}
	t.Logf("replay snapshot: %s", s1)
}

func TestConcurrentSerialEquivalence(t *testing.T) {
	// 多 goroutine 对不同工单做同类序列，-race 下反复跑；
	// 每张工单互相独立，最终快照必须与串行一致。
	hm, _ := buildReplayWorld(t, "base")
	const workers = 16
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			tag := fmt.Sprintf("c%d", w)
			// 复用 buildReplayWorld 的路线定义已在 hm 的 rm 中，但工单需自行开。
			id := "wo" + tag
			if err := hm.Open(id, "rt", 100); err != nil {
				t.Error(err)
				return
			}
			wm := hm.WIP()
			if err := wm.Report(id, 1, 0, 100, 0, 0); err != nil {
				t.Error(err)
			}
			if err := wm.Report(id, 2, 0, 90, 4, 6); err != nil {
				t.Error(err)
			}
			if err := wm.Report(id, 2, 1, 5, 1, 0); err != nil {
				t.Error(err)
			}
			if err := wm.Report(id, 3, 0, 80, 0, 10); err != nil {
				t.Error(err)
			}
			if err := wm.Report(id, 3, 1, 5, 0, 0); err != nil {
				t.Error(err)
			}
			if err := wm.Report(id, 2, 1, 10, 0, 0); err != nil {
				t.Error(err)
			}
			if err := wm.Report(id, 3, 1, 9, 1, 0); err != nil {
				t.Error(err)
			}
			if _, err := wm.Close(id); err != nil {
				t.Error(err)
			}
		}(w)
	}
	wg.Wait()

	wm := hm.WIP()
	for w := 0; w < workers; w++ {
		id := fmt.Sprintf("woc%d", w)
		s, err := wm.State(id)
		if err != nil {
			t.Fatal(err)
		}
		if !s.Closed || s.Done != 94 || s.Scrapped != 6 || len(s.Queue) != 0 {
			t.Fatalf("worker %d bad final: %+v", w, s)
		}
	}
}

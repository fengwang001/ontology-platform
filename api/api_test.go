package api_test

import (
	"errors"
	"sync"
	"testing"

	"ontology/api"
	"ontology/es"
	"ontology/replay"
)

// 多组事件序列，循环生成。
func seqSets() [][]es.Event {
	sets := [][]es.Event{{{Seq: 1, Delta: 10}, {Seq: 2, Delta: -3}, {Seq: 3, Delta: 7},
		{Seq: 4, Delta: -20}, {Seq: 5, Delta: 2}}}
	for _, m := range []int{10, 100, 1000} { // 多档规模
		evs := make([]es.Event, m)
		for i := range evs {
			d := int64(i%7) - 3 // 正负交替，反复触底
			evs[i] = es.Event{Seq: int64(i + 1), Delta: d}
		}
		sets = append(sets, evs)
	}
	return sets
}

func fullReplay(evs []es.Event) int64 {
	b := int64(0)
	for _, ev := range evs {
		b = es.Apply(b, ev)
	}
	return b
}

func feed(s *api.Store, evs []es.Event) {
	for _, ev := range evs {
		if err := s.Append(ev); err != nil {
			panic(err)
		}
	}
}

// TestReplayConsistency 不变量1：Replay(Snapshot(), 全部事件) == 从 0 全量重放。
func TestReplayConsistency(t *testing.T) {
	for i, evs := range seqSets() {
		s := api.New()
		feed(s, evs)
		got, err := s.Replay(s.Snapshot(), evs)
		if err != nil || got != fullReplay(evs) || got != s.State().Total {
			t.Errorf("set%d: got=%d err=%v want=%d", i, got, err, fullReplay(evs))
		}
	}
}

// TestIdempotent 不变量2：Seq<=快照.Seq 跳过；重复重放结果不变；重复 Seq 不重复生效。
func TestIdempotent(t *testing.T) {
	for i, evs := range seqSets() {
		s := api.New()
		feed(s, evs)
		snap := s.Snapshot()
		withOld := append([]es.Event{{Seq: 1, Delta: 999}}, evs...) // 混入旧事件
		dup := append(append([]es.Event{}, evs...), es.Event{Seq: snap.Seq, Delta: 999})
		a, _ := s.Replay(snap, evs)
		b, _ := s.Replay(snap, evs)
		c, err1 := s.Replay(snap, withOld)
		d, err2 := s.Replay(snap, dup)
		if a != b || a != c || a != d || err1 != nil || err2 != nil {
			t.Errorf("set%d: a=%d b=%d c=%d d=%d err=%v/%v", i, a, b, c, d, err1, err2)
		}
	}
}

// TestSnapshotBoundary 不变量3：重放从 快照.Seq+1 起，Seq=快照.Seq 不再应用。
func TestSnapshotBoundary(t *testing.T) {
	evs := seqSets()[0]
	s := api.New()
	got, err := s.Replay(es.Snapshot{Seq: 3, Total: 14}, evs)
	if err != nil || got != 2 {
		t.Fatalf("got=%d err=%v want 2", got, err)
	}
	// 只含 Seq<=快照.Seq 的事件：全部跳过，余额即快照值。
	got, _ = s.Replay(es.Snapshot{Seq: 3, Total: 14}, evs[:3])
	if got != 14 {
		t.Fatalf("boundary: got=%d want 14", got)
	}
}

// TestFailureAtomic 不变量4：三类可判定错误互不相同，被拒后状态不变且可继续用。
func TestFailureAtomic(t *testing.T) {
	s := api.New()
	feed(s, seqSets()[0])
	before := s.State()
	if err := s.Append(es.Event{Seq: 0, Delta: 5}); !errors.Is(err, es.ErrBadEvent) {
		t.Fatal("Seq=0 应报 ErrBadEvent")
	}
	if err := s.Append(es.Event{Seq: 3, Delta: 5}); !errors.Is(err, es.ErrBadEvent) {
		t.Fatal("乱序追加应报 ErrBadEvent")
	}
	if _, err := s.Replay(es.Snapshot{Seq: -1}, nil); !errors.Is(err, es.ErrBadSnapshot) {
		t.Fatal("坏快照应报 ErrBadSnapshot")
	}
	if _, err := s.Replay(es.Snapshot{}, []es.Event{{Seq: 2, Delta: 1}, {Seq: 1, Delta: 1}}); !errors.Is(err, replay.ErrBadReplay) {
		t.Fatal("乱序重放应报 ErrBadReplay")
	}
	if es.ErrBadEvent == es.ErrBadSnapshot || es.ErrBadSnapshot == replay.ErrBadReplay ||
		es.ErrBadEvent == replay.ErrBadReplay {
		t.Fatal("三类错误必须互不相同")
	}
	if s.State() != before {
		t.Fatal("被拒操作改变了状态")
	}
	if err := s.Append(es.Event{Seq: 6, Delta: 1}); err != nil || s.State().Total != 3 {
		t.Fatal("被拒后无法继续正常使用")
	}
	if err := s.SelfCheck(); err != nil {
		t.Fatal(err)
	}
}

// TestConcurrentRead 并发只读：N 个 goroutine 拿到的 State 逐字段相同。
func TestConcurrentRead(t *testing.T) {
	s := api.New()
	feed(s, seqSets()[3])
	want := s.State()
	const n = 64
	var wg sync.WaitGroup
	res := make([]es.Snapshot, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_ = s.SelfCheck()
			_ = s.Snapshot()
			res[i] = s.State()
		}(i)
	}
	wg.Wait()
	for i, got := range res {
		if got != want {
			t.Fatalf("goroutine %d: %+v != %+v", i, got, want)
		}
	}
}

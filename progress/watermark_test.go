package progress

import (
	"errors"
	"fmt"
	"sync"
	"testing"
)

// logState 打印操作、各分区位点、全局位点与判定依据，
// 保证测试结果可以仅凭日志逐步复现。
func logState(t *testing.T, op, reason string, tr *Tracker) {
	t.Helper()
	s := tr.Snapshot()
	line := fmt.Sprintf("op=%-24s | partitions:", op)
	for _, id := range stableIDs(s) {
		p := s.Partitions[id]
		state := "active"
		if p.Finished {
			state = fmt.Sprintf("finished(final=%d)", p.FinalPos)
		}
		line += fmt.Sprintf(" %s[start=%d reported=%d %s]", id, p.Start, p.Reported, state)
	}
	global := fmt.Sprintf("global=%d", s.Global)
	if s.AllDone {
		global = "global=+Inf"
	}
	line += " | " + global + " | basis: " + reason
	t.Log(line)
}

func TestStallBlocksGlobalAndFinishReleases(t *testing.T) {
	tr := NewTracker(0)

	if err := tr.Register("A", 0); err != nil {
		t.Fatal(err)
	}
	if err := tr.Register("B", 0); err != nil {
		t.Fatal(err)
	}
	logState(t, "register A@0,B@0", "min(active A,B)=min(0,0)=0", tr)

	if err := tr.Report("A", 10); err != nil {
		t.Fatal(err)
	}
	logState(t, "report A=10", "B stalls at 0 -> global pinned to 0", tr)
	if g, _ := tr.Global(); g != 0 {
		t.Fatalf("global = %d, want 0 (stalled partition pins it)", g)
	}

	if err := tr.Report("B", 5); err != nil {
		t.Fatal(err)
	}
	logState(t, "report B=5", "min(active A=10,B=5)=5", tr)
	if g, _ := tr.Global(); g != 5 {
		t.Fatalf("global = %d, want 5", g)
	}

	if err := tr.Finish("B"); err != nil {
		t.Fatal(err)
	}
	logState(t, "finish B(final=5)", "B removed from min-set; min(active A=10)=10", tr)
	if g, _ := tr.Global(); g != 10 {
		t.Fatalf("global = %d, want 10 after stalled partition finishes", g)
	}
	if f, ok := tr.Final("B"); !ok || f != 5 {
		t.Fatalf("final B = (%d,%v), want (5,true)", f, ok)
	}

	if err := tr.Finish("A"); err != nil {
		t.Fatal(err)
	}
	logState(t, "finish A(final=10)", "no active partition -> global=+Inf", tr)
	g, inf := tr.Global()
	if !inf || g != MaxPosition {
		t.Fatalf("global = (%d,inf=%v), want (MaxPosition,true)", g, inf)
	}
}

func TestIdempotentReport(t *testing.T) {
	tr := NewTracker(0)
	if err := tr.Register("A", 3); err != nil {
		t.Fatal(err)
	}
	before := tr.Snapshot()

	if err := tr.Report("A", 3); err != nil {
		t.Fatalf("equal report must be idempotent, got %v", err)
	}
	if err := tr.Report("A", 3); err != nil {
		t.Fatalf("repeat equal report must be idempotent, got %v", err)
	}
	logState(t, "register A@3; report A=3 x2", "equal position is a no-op; history/positions unchanged", tr)

	after := tr.Snapshot()
	if !EqualSnapshot(before, after) {
		t.Fatal("idempotent report changed state")
	}
	if h := tr.History(); len(h) != 1 || h[0].Kind != OpRegister {
		t.Fatalf("idempotent report must not be recorded, history=%v", h)
	}
}

func TestRollbackRejectedAndAtomic(t *testing.T) {
	tr := NewTracker(0)
	_ = tr.Register("A", 0)
	_ = tr.Report("A", 10)
	before := tr.Snapshot()

	err := tr.Report("A", 9)
	logState(t, "report A=9 (current 10)", "9 < 10 -> reject ErrPositionRolledBack, state untouched", tr)
	if !errors.Is(err, ErrPositionRolledBack) {
		t.Fatalf("err = %v, want ErrPositionRolledBack", err)
	}
	if !EqualSnapshot(before, tr.Snapshot()) {
		t.Fatal("failed report must not change any partition or global position")
	}

	if g, _ := tr.Global(); g != 10 {
		t.Fatalf("global = %d, want 10", g)
	}
}

func TestDistinguishableRejectionsAndAtomicity(t *testing.T) {
	tr := NewTracker(2)
	_ = tr.Register("A", 0)
	_ = tr.Register("B", 0)

	cases := []struct {
		name string
		fn   func() error
		want error
	}{
		{"report unregistered", func() error { return tr.Report("X", 1) }, ErrPartitionNotRegistered},
		{"finish unregistered", func() error { return tr.Finish("X") }, ErrPartitionNotRegistered},
		{"register duplicate", func() error { return tr.Register("A", 0) }, ErrPartitionExists},
		{"register over limit", func() error { return tr.Register("C", 0) }, ErrTooManyActivePartitions},
		{"report rolled back", func() error { return tr.Report("A", -1) }, ErrPositionRolledBack},
		{"empty id", func() error { return tr.Register("", 0) }, ErrInvalidArgument},
	}
	for _, tc := range cases {
		before := tr.Snapshot()
		err := tc.fn()
		if !errors.Is(err, tc.want) {
			t.Fatalf("%s: err = %v, want %v", tc.name, err, tc.want)
		}
		logState(t, tc.name+" -> rejected", "reject "+tc.want.Error()+"; all state unchanged", tr)
		if !EqualSnapshot(before, tr.Snapshot()) {
			t.Fatalf("%s: failed op mutated state", tc.name)
		}
	}

	// 完结一个分区腾出名额后，超限的注册才生效。
	if err := tr.Finish("A"); err != nil {
		t.Fatal(err)
	}
	if err := tr.Register("C", 0); err != nil {
		t.Fatalf("register after a slot freed: %v", err)
	}
	logState(t, "finish A; register C@0", "active={B,C}; finished A removed from min-set", tr)

	// 对已完结分区的任何写操作都必须被区分拒绝。
	if err := tr.Report("A", 1); !errors.Is(err, ErrPartitionFinished) {
		t.Fatalf("report finished: %v, want ErrPartitionFinished", err)
	}
	if err := tr.Finish("A"); !errors.Is(err, ErrPartitionFinished) {
		t.Fatalf("finish finished: %v, want ErrPartitionFinished", err)
	}
	if _, ok := tr.Final("B"); ok {
		t.Fatal("B is active, Final must report not-found")
	}
}

func TestReplayReproducesExactly(t *testing.T) {
	tr := NewTracker(3)
	ops := []struct {
		fn func() error
	}{
		{func() error { return tr.Register("A", 0) }},
		{func() error { return tr.Register("B", 2) }},
		{func() error { return tr.Register("C", 5) }},
		{func() error { return tr.Report("A", 8) }},
		{func() error { return tr.Report("B", 2) }}, // 幂等，不进历史
		{func() error { return tr.Finish("C") }},
		{func() error { return tr.Report("B", 9) }},
	}
	for _, op := range ops {
		if err := op.fn(); err != nil {
			t.Fatal(err)
		}
	}
	want := tr.Snapshot()

	replayed, got, err := Replay(tr.History(), 3)
	if err != nil {
		t.Fatal(err)
	}
	logState(t, "replay "+fmt.Sprint(len(tr.History()))+" effective ops", "rebuild from history and compare every field", replayed)
	if !EqualSnapshot(want, got) {
		t.Fatalf("replay mismatch\nwant=%+v\ngot =%+v", want, got)
	}
	if h1, h2 := tr.History(), replayed.History(); len(h1) != len(h2) {
		t.Fatalf("replayed history len = %d, want %d", len(h2), len(h1))
	}
}

func TestConcurrentReadsConsistentAndMonotonic(t *testing.T) {
	tr := NewTracker(4)
	_ = tr.Register("A", 0)
	_ = tr.Register("B", 0)

	var wg sync.WaitGroup
	stop := make(chan struct{})

	// 写者：A 快、B 慢——B 的停滞把全局位点钉住。
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 1; i <= 50; i++ {
			_ = tr.Report("A", Position(i*2))
			if i%5 == 0 {
				_ = tr.Report("B", Position(i))
			}
		}
		_ = tr.Finish("A")
		_ = tr.Finish("B")
		close(stop)
	}()

	var (
		mu       sync.Mutex
		snaps    []Snapshot
		readerWG sync.WaitGroup
	)

	observe := func() {
		defer readerWG.Done()
		var prev Position = -1
		for {
			select {
			case <-stop:
				return
			default:
				s := tr.Snapshot()
				// 快照内部必须自洽：global 等于各未完结分区 reported 的最小值。
				min := MaxPosition
				for _, p := range s.Partitions {
					if !p.Finished && p.Reported < min {
						min = p.Reported
					}
				}
				if s.Global != min {
					t.Errorf("snapshot inconsistent: global=%d recomputed=%d", s.Global, min)
					return
				}
				if s.AllDone != (min == MaxPosition) {
					t.Errorf("AllDone=%v but min=%d", s.AllDone, min)
				}
				// 单个读者观察到的全局位点序列必须单调不减。
				if s.Global < prev {
					t.Errorf("global went backwards: %d -> %d", prev, s.Global)
					return
				}
				prev = s.Global
				mu.Lock()
				snaps = append(snaps, s)
				mu.Unlock()
			}
		}
	}
	readerWG.Add(3)
	for i := 0; i < 3; i++ {
		go observe()
	}

	wg.Wait()
	readerWG.Wait()

	final := tr.Snapshot()
	logState(t, "concurrent writers/readers finished",
		"all snapshots internally consistent; each reader saw non-decreasing global", tr)
	if !final.AllDone {
		t.Fatal("final snapshot should report AllDone")
	}
	// 同一时刻并发读取：多个读者拿同一份稳定终态，必须逐字段相同。
	for i := 0; i < 3; i++ {
		s := tr.Snapshot()
		if !EqualSnapshot(s, final) {
			t.Fatal("concurrent reads of the settled instance differ field-by-field")
		}
	}
	// 所有历史观测都不得超过最终状态（全局位点单调不减的跨读者核对）。
	for _, s := range snaps {
		if s.Global > final.Global {
			t.Fatalf("observed global %d exceeds final %d", s.Global, final.Global)
		}
	}
}

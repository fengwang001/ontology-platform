package resume_test

import (
	"sync"
	"testing"

	"ontology/resume"
)

func sizeTick(tick int64) uint64 { return uint64(tick) }

func sumTick(from, to int64) uint64 {
	if from > to {
		return 0
	}
	return uint64((from + to) * (to - from + 1) / 2)
}

func feed(t *testing.T, pl *resume.Planner, to int64) {
	t.Helper()
	for tick := pl.Cur() + 1; tick <= to; tick++ {
		if err := pl.Append(tick*10, sizeTick(tick)); err != nil {
			t.Fatalf("feed %d: %v", tick, err)
		}
	}
}

func reconnectSetup(t *testing.T, cur int64) (*resume.Planner, int) {
	t.Helper()
	pl := resume.New(10, 16, 4, 5000, 4)
	feed(t, pl, cur)
	if err := pl.Join(cur*10+1, "x"); err != nil {
		t.Fatal(err)
	}
	tok, err := pl.Disconnect(cur*10+2, "x")
	if err != nil {
		t.Fatal(err)
	}
	return pl, tok
}

func mustJoinDisc(t *testing.T, pl *resume.Planner, cur int64) {
	t.Helper()
	if err := pl.Join(cur*10+10, "x"); err != nil {
		t.Fatal(err)
	}
	if _, err := pl.Disconnect(cur*10+11, "x"); err != nil {
		t.Fatal(err)
	}
}

// TestSpecExamples 覆盖题目给出的全部手算例子与关键边界。
func TestSpecExamples(t *testing.T) {
	type tc struct {
		name      string
		cur       int64
		have      int64
		wantKind  resume.Kind
		wantSnap  int64
		wantFrom  int64
		wantTo    int64
		wantBytes uint64
		wantErr   error
	}
	cases := []tc{
		{"have33-delta", 37, 33, resume.Delta, 0, 34, 37, sumTick(34, 37), nil},
		{"cost-tie-delta", 37, 26, resume.Delta, 0, 27, 37, sumTick(27, 37), nil},
		{"cost-over-snapshot", 37, 25, resume.SnapshotKind, 30, 31, 37,
			sumTick(21, 30) + sumTick(31, 37), nil},
		{"oldest-minus-one", 37, 21, resume.SnapshotKind, 30, 31, 37,
			sumTick(21, 30) + sumTick(31, 37), nil},
		{"oldest-minus-two-evicted", 37, 20, resume.SnapshotKind, 30, 31, 37,
			sumTick(21, 30) + sumTick(31, 37), nil},
		{"have-equals-s", 37, 30, resume.Delta, 0, 31, 37, sumTick(31, 37), nil},
		{"have-equals-cur-none", 37, 37, resume.None, 0, 38, 37, 0, nil},
		{"ahead", 37, 38, resume.None, 0, 0, 0, 0, resume.ErrAhead},
		{"no-snapshot-yet", 7, 0, resume.Delta, 0, 1, 7, sumTick(1, 7), nil},
		{"snapshot-is-cur-empty-range", 40, 10, resume.SnapshotKind, 40, 41, 40,
			sumTick(31, 40), nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			pl, tok := reconnectSetup(t, c.cur)
			plan, err := pl.Reconnect(c.cur*10+3, "x", tok, c.have)
			if err != c.wantErr {
				t.Fatalf("input Reconnect(have=%d): want err %v, got %v (%s)",
					c.have, c.wantErr, err, pl.LastReason())
			}
			if c.wantErr != nil {
				t.Logf("input: cur=%d have=%d; output: %v; basis: %s",
					c.cur, c.have, err, pl.LastReason())
				return
			}
			if plan.Kind != c.wantKind || plan.SnapTick != c.wantSnap ||
				plan.From != c.wantFrom || plan.To != c.wantTo ||
				plan.Bytes != c.wantBytes {
				t.Fatalf("input: cur=%d have=%d; got plan %+v; want kind=%s snap=%d [%d,%d] bytes=%d",
					c.cur, c.have, plan, c.wantKind, c.wantSnap, c.wantFrom, c.wantTo, c.wantBytes)
			}
			t.Logf("input: cur=%d low=%d have=%d; output: %s snap=%d [%d,%d] bytes=%d; basis: %s",
				c.cur, pl.Low(), c.have, plan.Kind, plan.SnapTick, plan.From, plan.To,
				plan.Bytes, pl.LastReason())
		})
	}

	// 缓冲边界在“大代价 C”下：low-1 仍可走 Delta，再前一格帧被淘汰走快照。
	pl := resume.New(10, 16, 100, 5000, 4)
	feed(t, pl, 40)
	if err := pl.Join(410, "x"); err != nil {
		t.Fatal(err)
	}
	if _, err := pl.Disconnect(411, "x"); err != nil {
		t.Fatal(err)
	}
	if p, err := pl.Reconnect(413, "x", 0, 24); err != nil ||
		p.Kind != resume.Delta || p.From != 25 || p.To != 40 ||
		p.Bytes != sumTick(25, 40) {
		t.Fatalf("low-1 delta case: %+v err=%v (%s)", p, err, pl.LastReason())
	}
	tok2, err := pl.Disconnect(420, "x")
	if err != nil {
		t.Fatal(err)
	}
	p, err := pl.Reconnect(422, "x", tok2, 23)
	if err != nil || p.Kind != resume.SnapshotKind || p.SnapTick != 40 ||
		p.From != 41 || p.To != 40 || p.Bytes != sumTick(31, 40) {
		t.Fatalf("low-2 evicted case: %+v err=%v (%s)", p, err, pl.LastReason())
	}
	t.Logf("buffer-edge inputs: have=24 -> Delta[25,40]; have=23 -> Snapshot(40) empty-tail bytes=%d; basis: %s",
		p.Bytes, pl.LastReason())
}

// TestTouchedIndependentOfK：一次 Reconnect 触碰帧记录数不超过 2，
// K=64 与 K=65536 两档计划一致、触碰数一致；快照空区间触碰为 0。
func TestTouchedIndependentOfK(t *testing.T) {
	run := func(k int) (resume.Plan, int) {
		pl := resume.New(10, k, 100, 1_000_000_000, 4)
		feed(t, pl, 1000)
		if err := pl.Join(10001, "x"); err != nil {
			t.Fatal(err)
		}
		if _, err := pl.Disconnect(10002, "x"); err != nil {
			t.Fatal(err)
		}
		plan, err := pl.Reconnect(10003, "x", 0, 950)
		if err != nil {
			t.Fatal(err)
		}
		return plan, pl.Touched()
	}
	p64, touched64 := run(64)
	pBig, touchedBig := run(65536)
	if p64 != pBig {
		t.Fatalf("plans differ by K: %+v vs %+v", p64, pBig)
	}
	if touched64 > 2 || touchedBig > 2 {
		t.Fatalf("touched must not exceed 2, got %d (K=64) and %d (K=65536)", touched64, touchedBig)
	}
	t.Logf("input: cur=1000 have=950; output: %s [%d,%d] bytes=%d; touched=%d (K=64) and %d (K=65536), both <= 2",
		p64.Kind, p64.From, p64.To, p64.Bytes, touched64, touchedBig)

	// 快照帧恰为 cur：补帧区间为空，不触碰任何帧记录。
	pl := resume.New(10, 16, 4, 5000, 4)
	feed(t, pl, 40)
	if err := pl.Join(401, "x"); err != nil {
		t.Fatal(err)
	}
	tok, err := pl.Disconnect(402, "x")
	if err != nil {
		t.Fatal(err)
	}
	p, err := pl.Reconnect(407, "x", tok, 10)
	if err != nil || p.Kind != resume.SnapshotKind || p.Bytes != sumTick(31, 40) {
		t.Fatalf("snap-at-cur: %+v err=%v", p, err)
	}
	if got := pl.Touched(); got != 0 {
		t.Fatalf("empty snapshot range must touch 0 records, got %d", got)
	}
	t.Logf("input: cur=40 have=10; output: Snapshot(40) empty-tail bytes=%d; touched=0; basis: %s",
		p.Bytes, pl.LastReason())
}

// TestConcurrentOps 用 -race 保证并发调用等价于某串行顺序、无数据竞争。
func TestConcurrentOps(t *testing.T) {
	pl := resume.New(10, 64, 4, 1_000_000_000, 64)
	var wg sync.WaitGroup
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			name := string(rune('a' + id))
			for i := 0; i < 200; i++ {
				now := int64(id*10000 + i)
				_ = pl.Append(now, 1)
				_ = pl.Join(now, name)
				_ = pl.Ack(now, name, pl.Cur())
				if tok, err := pl.Disconnect(now, name); err == nil {
					_, _ = pl.Reconnect(now, name, tok, pl.Cur())
				}
			}
		}(w)
	}
	wg.Wait()
}

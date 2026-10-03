package ontology

import (
	"errors"
	"sync"
	"testing"
)

func mustNew(t *testing.T, n int, b, g int64) *Funnel {
	t.Helper()
	f, err := NewFunnel(n, b, g)
	if err != nil {
		t.Fatalf("NewFunnel(%d,%d,%d) error: %v", n, b, g, err)
	}
	return f
}

func activeIDSet(f *Funnel) map[int64]bool {
	m := map[int64]bool{}
	for _, s := range f.Snapshot() {
		m[s.ID] = s.Active
	}
	return m
}

// TestExampleFromSpec 复现题目给出的 n=4,b=2,G=3 完整示例。
func TestExampleFromSpec(t *testing.T) {
	f := mustNew(t, 4, 2, 3)

	wantSeq := []int64{0, 1, 2, 3, 0, 1, 2, 3}
	clicksAfter := map[int][]int64{3: {0, 1}, 4: {0}}

	for step, want := range wantSeq {
		got, err := f.Next()
		if err != nil {
			t.Fatalf("step %d: Next error %v", step, err)
		}
		if got != want {
			t.Fatalf("step %d: Next=%d want %d (sR tie picks smaller id)", step, got, want)
		}
		for _, id := range clicksAfter[step] {
			if err := f.Click(id); err != nil {
				t.Fatalf("step %d: Click(%d) error %v", step, id, err)
			}
		}
	}

	if f.Round() != 2 {
		t.Fatalf("round=%d want 2", f.Round())
	}
	evs := f.EliminationEvents()
	if len(evs) != 2 || evs[0] != (ElimEvent{1, 2, ReasonRound}) ||
		evs[1] != (ElimEvent{1, 3, ReasonRound}) {
		t.Fatalf("events=%v want [{1 2 round} {1 3 round}]", evs)
	}

	if err := f.Join(4); err != nil {
		t.Fatalf("Join(4): %v", err)
	}

	wantR2 := []int64{0, 1, 4, 0, 1, 4, 0, 1, 4, 0, 1, 4}
	r2extra := map[int64]int{0: 2} // 0 在 r2 再点 2 次 -> 总 cT=4
	for step, want := range wantR2 {
		got, err := f.Next()
		if err != nil {
			t.Fatalf("r2 step %d: %v", step, err)
		}
		if got != want {
			t.Fatalf("r2 step %d: Next=%d want %d (joined arm with sR=0 first)", step, got, want)
		}
		if r2extra[got] > 0 {
			if err := f.Click(got); err != nil {
				t.Fatalf("r2 step %d extra Click(%d): %v", step, got, err)
			}
			r2extra[got]--
		}
		if got == 4 {
			s4 := snapshotOf(f, 4)
			if s4.Clicks < 3 { // 前 3 次曝光各点击一次，收轮时 cT=3
				if err := f.Click(4); err != nil {
					t.Fatalf("r2 step %d Click(4): %v", step, err)
				}
			}
		}
	}

	snap := map[int64]ArmSnapshot{}
	for _, s := range f.Snapshot() {
		snap[s.ID] = s
	}
	if snap[4].Clicks != 3 || snap[4].Exposure != 4 {
		t.Fatalf("arm4=%+v want cT=3 sT=4", snap[4])
	}
	if snap[0].Clicks != 4 || snap[0].Exposure != 6 {
		t.Fatalf("arm0=%+v want cT=4 sT=6", snap[0])
	}
	evs = f.EliminationEvents()
	last := evs[len(evs)-1]
	if last != (ElimEvent{2, 1, ReasonRound}) {
		t.Fatalf("last event=%v want {2 1 round} (3*6 > 4*4)", last)
	}
	act := activeIDSet(f)
	if !act[0] || !act[4] || act[1] {
		t.Fatalf("active=%v want 0,4", act)
	}
}

// TestGuardrailExactBoundary: guardrail fires exactly at sT=G,cT=0; not at G-1.
func TestGuardrailExactBoundary(t *testing.T) {
	f := mustNew(t, 2, 2, 3)
	for i := 0; i < 4; i++ {
		if _, err := f.Next(); err != nil {
			t.Fatal(err)
		}
	}
	evs := f.EliminationEvents()
	if len(evs) != 1 || evs[0].Reason != ReasonRound {
		t.Fatalf("sT=G-1 events=%v want one round event", evs)
	}

	f2 := mustNew(t, 3, 1, 3)
	for i := 0; i < 3; i++ {
		if _, err := f2.Next(); err != nil {
			t.Fatal(err)
		}
	}
	if got, _ := f2.Next(); got != 0 {
		t.Fatalf("r2 first got %d want 0", got)
	}
	if got, _ := f2.Next(); got != 1 {
		t.Fatalf("r2 second got %d want 1", got)
	}
	got, err := f2.Next()
	if err != nil || got != 0 {
		t.Fatalf("guardrail Next=(%d,%v) want 0,nil", got, err)
	}
	evs2 := f2.EliminationEvents()
	if evs2[0] != (ElimEvent{1, 2, ReasonRound}) {
		t.Fatalf("first event=%v want {1 2 round}", evs2[0])
	}
	if evs2[1] != (ElimEvent{2, 0, ReasonGuardrail}) {
		t.Fatalf("guardrail event=%v want {2 0 guardrail} at sT=G=3", evs2[1])
	}
	if w, ok := f2.Winner(); !ok || w != 1 {
		t.Fatalf("winner=(%d,%v) want 1", w, ok)
	}
}

// TestGuardrailRemovesLastUnderQuotaArm: after guardrail removes the only
// under-quota active arm, all survivors are full -> round closes immediately
// inside the same Next.
func TestGuardrailRemovesLastUnderQuotaArm(t *testing.T) {
	f := mustNew(t, 3, 2, 4)
	// r1 顺序 0,1,2,0,1,2：给 0 两次点击、1 一次点击，保证二者在 r2
	// 累计到 sT=4=G 时 cT>0，不会触发护栏；2 零点击，收轮淘汰。
	r1clicks := map[int]int64{0: 0, 1: 1, 3: 0}
	for i := 0; i < 6; i++ {
		if _, err := f.Next(); err != nil {
			t.Fatal(err)
		}
		if id, ok := r1clicks[i]; ok {
			if err := f.Click(id); err != nil {
				t.Fatal(err)
			}
		}
	}
	want7 := []int64{0, 1, 0, 1, 0, 1, 0}
	for i, want := range want7 {
		got, err := f.Next()
		if err != nil || got != want {
			t.Fatalf("r2 step %d got=(%d,%v) want %d", i, got, err, want)
		}
	}
	if err := f.Join(4); err != nil {
		t.Fatal(err)
	}
	// Join 后 sR：0=4,1=3,4=0。顺序为 4,4,4（sR 最小），之后
	// 4 与 1 同为 3，取编号小者 1（1 补满），再选 4：其 sT=4=G、
	// cT=0 -> 护栏淘汰 4，此时 0、1 均满额 -> 同一次 Next 内立即收轮。
	wantAfterJoin := []int64{4, 4, 4, 1, 4}
	for i, want := range wantAfterJoin {
		got, err := f.Next()
		if err != nil || got != want {
			t.Fatalf("after join step %d got=(%d,%v) want %d", i, got, err, want)
		}
		if got == 1 {
			// 1 满额后再给三次点击 -> 收轮时 1=4/6 高于 0=2/6。
			for k := 0; k < 3; k++ {
				if err := f.Click(1); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
	evs := f.EliminationEvents()
	last2 := evs[len(evs)-2:]
	if last2[0] != (ElimEvent{2, 4, ReasonGuardrail}) ||
		last2[1] != (ElimEvent{2, 0, ReasonRound}) {
		t.Fatalf("last events=%v want guardrail(4) then round(0) in same Next", last2)
	}
	if w, ok := f.Winner(); !ok || w != 1 {
		t.Fatalf("winner=(%d,%v) want 1", w, ok)
	}
}

// TestGuardrailImmediateFinish: guardrail elimination leaves one active arm.
func TestGuardrailImmediateFinish(t *testing.T) {
	f := mustNew(t, 2, 3, 2)
	// r1 顺序 0,1,0：第三次选中 0 时 sT(0)=2=G、cT=0，护栏淘汰 0，
	// 此时只剩 1（其 sR=1<q=3），立即结束，1 胜出。
	for i, want := range []int64{0, 1, 0} {
		got, err := f.Next()
		if err != nil || got != want {
			t.Fatalf("step %d got=(%d,%v) want %d", i, got, err, want)
		}
	}
	if w, ok := f.Winner(); !ok || w != 1 {
		t.Fatalf("winner=(%d,%v) want 1", w, ok)
	}
	if _, err := f.Next(); !errors.Is(err, ErrFinished) {
		t.Fatalf("Next after finish err=%v want ErrFinished", err)
	}
	if err := f.Join(9); !errors.Is(err, ErrFinished) {
		t.Fatalf("Join after finish err=%v want ErrFinished", err)
	}
	if err := f.Click(0); err != nil {
		t.Fatalf("Click after finish should still bookkeep: %v", err)
	}
}

// TestOddActiveCount keeps ceil(a/2) arms when active count is odd.
func TestOddActiveCount(t *testing.T) {
	f := mustNew(t, 5, 1, 1_000_000_000)
	for i := 0; i < 5; i++ {
		_, _ = f.Next()
	}
	if f.ActiveCount() != 3 {
		t.Fatalf("active=%d want 3 = ceil(5/2)", f.ActiveCount())
	}
	act := activeIDSet(f)
	for _, id := range []int64{0, 1, 2} {
		if !act[id] {
			t.Fatalf("arm %d should survive tie-by-id", id)
		}
	}
}

func snapshotOf(f *Funnel, id int64) ArmSnapshot {
	for _, s := range f.Snapshot() {
		if s.ID == id {
			return s
		}
	}
	return ArmSnapshot{}
}

// TestEqualCTRTieByID: equal CTR with different denominators uses cross
// multiplication; ties keep the smaller id.
func TestEqualCTRTieByID(t *testing.T) {
	// All 0/1 equal -> smallest ids survive.
	f := mustNew(t, 4, 1, 1_000_000_000)
	for i := 0; i < 4; i++ {
		_, _ = f.Next()
	}
	act := activeIDSet(f)
	if !act[0] || !act[1] || f.ActiveCount() != 2 {
		t.Fatalf("active=%v want 0,1 (equal 0/N tie by id)", act)
	}

	// Nonzero equality across denominators: at the round-2 close
	// arm 1 = 3/6 and joined arm 4 = 1/2 (1*6 == 3*2); arm 0 wins.
	// Tie removes 4 because 1 has the smaller id.
	g := mustNew(t, 3, 2, 1_000_000_000)
	clickAfter := map[int]int64{0: 0, 1: 1, 3: 0}
	for i := 0; i < 6; i++ {
		_, _ = g.Next()
		if id, ok := clickAfter[i]; ok {
			if err := g.Click(id); err != nil {
				t.Fatal(err)
			}
		}
	}
	if g.ActiveCount() != 2 {
		t.Fatalf("after r1 active=%d want 2", g.ActiveCount())
	}
	if err := g.Join(4); err != nil {
		t.Fatal(err)
	}
	// r2 order 0,1,4 cycling, 12 exposures. Target totals at close:
	// 0: 6/6, 1: 3/6, 4: 1/2.
	targetClicks := map[int64]int64{0: 6, 1: 3, 4: 1}
	for i := 0; i < 12; i++ {
		x, err := g.Next()
		if err != nil {
			t.Fatal(err)
		}
		sx := snapshotOf(g, x)
		for sx.Clicks < targetClicks[x] && sx.Clicks < sx.Exposure {
			if err := g.Click(x); err != nil {
				t.Fatalf("click %d at step %d: %v", x, i, err)
			}
			sx = snapshotOf(g, x)
		}
	}
	evs := g.EliminationEvents()
	last := evs[len(evs)-1]
	if last != (ElimEvent{2, 4, ReasonRound}) {
		t.Fatalf("last event=%v want {2 4 round}: 1/2 == 3/6 keeps smaller id 1", last)
	}
	if g.Round() != 3 || g.ActiveCount() != 2 {
		t.Fatalf("after tie round round=%d active=%d want 3/2", g.Round(), g.ActiveCount())
	}
	gact := activeIDSet(g)
	if !gact[0] || !gact[1] || gact[4] {
		t.Fatalf("active=%v want 0,1", gact)
	}
}

// TestLateClickOnEliminatedArm: clicks on eliminated arms only bookkeep.
func TestLateClickOnEliminatedArm(t *testing.T) {
	f := mustNew(t, 2, 1, 1_000_000_000)
	_, _ = f.Next()
	_, _ = f.Next()
	s1 := snapshotOf(f, 1)
	if s1.Active {
		t.Fatal("arm 1 should be eliminated at r1 close")
	}
	if err := f.Click(1); err != nil {
		t.Fatalf("late click on eliminated arm: %v", err)
	}
	s1 = snapshotOf(f, 1)
	if s1.Clicks != 1 || s1.Active {
		t.Fatalf("arm1=%+v want cT=1 and inactive", s1)
	}
	if err := f.Click(1); !errors.Is(err, ErrNoExposure) {
		t.Fatalf("second late click err=%v want ErrNoExposure", err)
	}
}

// TestClickBeforeExposureRejected: click with cT >= sT is rejected.
func TestClickBeforeExposureRejected(t *testing.T) {
	f := mustNew(t, 2, 1, 1_000_000_000)
	if err := f.Click(0); !errors.Is(err, ErrNoExposure) {
		t.Fatalf("Click before any Next err=%v want ErrNoExposure", err)
	}
	_, _ = f.Next()
	if err := f.Click(0); err != nil {
		t.Fatal(err)
	}
	if err := f.Click(0); !errors.Is(err, ErrNoExposure) {
		t.Fatalf("second click on one exposure err=%v want ErrNoExposure", err)
	}
	if err := f.Click(100); !errors.Is(err, ErrUnknownArm) {
		t.Fatalf("Click unknown arm err=%v want ErrUnknownArm", err)
	}
}

// TestRejectionsAndPriority covers rejection precedence and no-state-change.
func TestRejectionsAndPriority(t *testing.T) {
	if _, err := NewFunnel(1, 1, 1); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("n=1 err=%v want ErrInvalidArgument", err)
	}
	if _, err := NewFunnel(65, 1, 1); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("n=65 err=%v want ErrInvalidArgument", err)
	}
	if _, err := NewFunnel(2, 0, 1); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("b=0 err=%v want ErrInvalidArgument", err)
	}
	if _, err := NewFunnel(2, 1, 0); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("G=0 err=%v want ErrInvalidArgument", err)
	}

	f := mustNew(t, 2, 1, 1_000_000_000)
	if err := f.Join(-1); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("Join(-1) err=%v want ErrInvalidArgument", err)
	}
	if err := f.Join(1001); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("Join(1001) err=%v want ErrInvalidArgument", err)
	}
	before := len(f.Snapshot())
	if err := f.Join(0); !errors.Is(err, ErrArmExists) {
		t.Fatalf("Join(0) err=%v want ErrArmExists", err)
	}
	if len(f.Snapshot()) != before {
		t.Fatal("rejected Join must not change state")
	}
	if err := f.Click(1001); !errors.Is(err, ErrUnknownArm) {
		t.Fatalf("rejected Join must not register arm: err=%v want ErrUnknownArm", err)
	}

	full := mustNew(t, 64, 1, 1_000_000_000)
	if err := full.Join(500); !errors.Is(err, ErrCapacity) {
		t.Fatalf("Join on 64 arms err=%v want ErrCapacity", err)
	}
	if err := full.Join(0); !errors.Is(err, ErrArmExists) {
		t.Fatalf("Join existing id when full err=%v want ErrArmExists", err)
	}

	done := mustNew(t, 2, 1, 1_000_000_000)
	_, _ = done.Next()
	_, _ = done.Next()
	if !done.Finished() {
		t.Fatal("expected finish after r1 with two 0/1 arms")
	}
	if _, err := done.Next(); !errors.Is(err, ErrFinished) {
		t.Fatalf("Next after finish err=%v want ErrFinished", err)
	}
	if err := done.Join(77); !errors.Is(err, ErrFinished) {
		t.Fatalf("Join fresh id after finish err=%v want ErrFinished", err)
	}
	if err := done.Join(0); !errors.Is(err, ErrFinished) {
		t.Fatalf("Join duplicate after finish err=%v want ErrFinished", err)
	}
	if err := done.Click(77); !errors.Is(err, ErrUnknownArm) {
		t.Fatalf("Click unknown after finish err=%v want ErrUnknownArm", err)
	}
}

// TestQuotaDoublingAndCap: q_r doubles each round and caps at 2^20 factor.
func TestQuotaDoublingAndCap(t *testing.T) {
	if q := quota(3, 1); q != 3 {
		t.Fatalf("q1=%d want 3", q)
	}
	if q := quota(3, 2); q != 6 {
		t.Fatalf("q2=%d want 6", q)
	}
	if q := quota(1, 21); q != 1<<20 {
		t.Fatalf("q21=%d want %d", q, int64(1)<<20)
	}
	if q := quota(7, 30); q != 7<<20 {
		t.Fatalf("q30=%d want capped %d", q, int64(7)<<20)
	}

	g := mustNew(t, 3, 1, 1_000_000_000)
	for i := 0; i < 3; i++ {
		_, _ = g.Next()
	}
	if g.Round() != 2 {
		t.Fatalf("round=%d want 2", g.Round())
	}
	for i := 0; i < 4; i++ {
		_, _ = g.Next()
	}
	for _, s := range g.Snapshot() {
		if s.Active && s.Exposure != 1+2 {
			t.Fatalf("arm %d sT=%d want 3 (q1=1,q2=2)", s.ID, s.Exposure)
		}
	}
}

// TestInvariantsUnderConcurrency hammers Next/Click/Join concurrently.
// The race detector plus post-run invariants validate serialized semantics.
func TestInvariantsUnderConcurrency(t *testing.T) {
	f := mustNew(t, 8, 50, 30)
	var wg sync.WaitGroup
	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func(seed int) {
			defer wg.Done()
			for k := 0; k < 400; k++ {
				id, err := f.Next()
				if err == nil && (seed+k)%3 == 0 {
					_ = f.Click(id)
				}
			}
		}(w)
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for id := int64(100); id < 110; id++ {
			_ = f.Join(id)
		}
	}()
	wg.Wait()

	var sum int64
	active := 0
	for _, s := range f.Snapshot() {
		if s.Clicks > s.Exposure {
			t.Fatalf("arm %d cT=%d > sT=%d", s.ID, s.Clicks, s.Exposure)
		}
		sum += s.Exposure
		if s.Active {
			active++
		}
	}
	if sum != f.TotalExposures() {
		t.Fatalf("sum(sT)=%d != successful Next count %d", sum, f.TotalExposures())
	}
	if f.Finished() {
		if active != 1 {
			t.Fatalf("finished but active=%d want 1", active)
		}
		if _, ok := f.Winner(); !ok {
			t.Fatal("finished but Winner() not set")
		}
	} else {
		if active < 2 {
			t.Fatalf("not finished but active=%d < 2", active)
		}
	}
}

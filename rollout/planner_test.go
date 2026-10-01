package rollout

import (
	"errors"
	"testing"
)

func mustNew(t *testing.T, N int64, PS, PU int, MR int64, P int) *Planner {
	t.Helper()
	p, err := New(N, PS, PU, MR, P)
	if err != nil {
		t.Fatalf("New(%d,%d,%d,%d,%d) unexpected error: %v", N, PS, PU, MR, P, err)
	}
	return p
}

func (p *Planner) snapLocked(now int64) State {
	return State{
		OldReady:    p.a,
		OldUnready:  p.b,
		NewReady:    p.c,
		NewUnready:  p.d,
		StableReady: p.stableLocked(now),
		StallCount:  p.stall,
		MaxNow:      p.maxNow,
		Done:        p.doneLocked(now),
	}
}

func stepWant(t *testing.T, p *Planner, now int64, want StepResult) StepResult {
	t.Helper()
	got, err := p.Step(now)
	if err != nil {
		t.Fatalf("Step(%d) unexpected error: %v", now, err)
	}
	t.Logf("判定: Step(now=%d) -> (up=%d,r1=%d,r2=%d,stalled=%v) 期望=%+v | 状态 a=%d b=%d c=%d d=%d cs=%d stall=%d",
		now, got.Up, got.Cleaned, got.Removed, got.Stalled, want,
		p.a, p.b, p.c, p.d, p.stableLocked(now), p.stall)
	if got != want {
		t.Fatalf("Step(%d) = %+v, want %+v", now, got, want)
	}
	return got
}

// 题目给定示例：N=10、PS=PU=25、MR=1000，S=3、U=2。
func TestSpecExample(t *testing.T) {
	p := mustNew(t, 10, 25, 25, 1000, 3)
	if p.s != 3 || p.u != 2 {
		t.Fatalf("ceil/floor limits = (S=%d,U=%d), want (3,2)", p.s, p.u)
	}
	stepWant(t, p, 0, StepResult{3, 0, 2, false})
	if err := p.NewReady(3, 100); err != nil {
		t.Fatal(err)
	}
	t.Logf("输入 NewReady(k=3,now=100) 已接受；批次=%v；now=500 时 cs=%d（未稳定不计可用）",
		p.batches, p.stableLocked(500))
	stepWant(t, p, 500, StepResult{2, 0, 0, false})
	stepWant(t, p, 1100, StepResult{0, 0, 3, false})
}

// 向上/向下取整边界。
func TestRoundingLimits(t *testing.T) {
	cases := []struct {
		name             string
		N                int64
		PS, PU           int
		wantS, wantU     int64
		firstUp, firstR2 int64
	}{
		{"N10-25pct", 10, 25, 25, 3, 2, 3, 2},
		{"N1-50pct", 1, 50, 50, 1, 0, 1, 0},
		{"N3-33pct", 3, 33, 33, 1, 0, 1, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := mustNew(t, tc.N, tc.PS, tc.PU, 0, 2)
			t.Logf("判定: N=%d PS=%d PU=%d -> S=%d U=%d",
				tc.N, tc.PS, tc.PU, p.s, p.u)
			if p.s != tc.wantS || p.u != tc.wantU {
				t.Fatalf("(S,U)=(%d,%d), want (%d,%d)", p.s, p.u, tc.wantS, tc.wantU)
			}
			stepWant(t, p, 0, StepResult{tc.firstUp, 0, tc.firstR2, false})
		})
	}
}

// PS=PU=0 时 U 强制为 1；N=1 首步为 (0,0,1,false)。
func TestZeroPercentagesForceUOne(t *testing.T) {
	p := mustNew(t, 1, 0, 0, 0, 2)
	if p.s != 0 || p.u != 1 {
		t.Fatalf("(S,U)=(%d,%d), want (0,1)", p.s, p.u)
	}
	stepWant(t, p, 0, StepResult{0, 0, 1, false})
	st := p.Snapshot(0)
	if st.Total() != 0 || st.Available() != 0 {
		t.Fatalf("after first step state=%+v", st)
	}
}

// 未就绪旧实例清理 r1=b 不占不可用额度；同一步 up 按调用开始时总数计算。
func TestOldUnreadyCleanupFreeAndUpUsesStartTotal(t *testing.T) {
	// N=3, PS=25 -> S=1；PU=50 -> U=1, N-U=2。
	p := mustNew(t, 3, 25, 50, 0, 2)
	stepWant(t, p, 0, StepResult{1, 0, 1, false})
	if err := p.OldUnready(1, 1); err != nil {
		t.Fatal(err)
	}
	// a=1,b=1,d=1 总 3：up=4-3=1（按开始时总数算，不受本步 r1=1 影响）；
	// A(开始)=a+cs=1 <= N-U=2，r2=0；r1=b=1 免费清理。
	stepWant(t, p, 2, StepResult{1, 1, 0, false})
	st := p.Snapshot(2)
	t.Logf("判定: 清理后 a=%d b=%d c=%d d=%d total=%d",
		st.OldReady, st.OldUnready, st.NewReady, st.NewUnready, st.Total())
	if st.OldReady != 1 || st.OldUnready != 0 || st.NewUnready != 2 || st.Total() != 3 {
		t.Fatalf("state=%+v", st)
	}
}

// now-readyAt 恰等于 MR 稳定，差 1 不稳定。
func TestStabilityBoundary(t *testing.T) {
	p := mustNew(t, 10, 25, 25, 1000, 3)
	stepWant(t, p, 0, StepResult{3, 0, 2, false})
	if err := p.NewReady(3, 100); err != nil {
		t.Fatal(err)
	}
	stepWant(t, p, 1099, StepResult{2, 0, 0, false})
	if cs := p.Snapshot(1099).StableReady; cs != 0 {
		t.Fatalf("cs(1099)=%d, want 0（999 < 1000）", cs)
	}
	if cs := p.Snapshot(1100).StableReady; cs != 3 {
		t.Fatalf("cs(1100)=%d, want 3（1000 >= 1000）", cs)
	}
	stepWant(t, p, 1100, StepResult{0, 0, 3, false})
}

// NewFail 先扣最新批；失败使可用数下降后，下一步缩减数随之变小。
func TestNewFailLatestBatchAndSmallerRemoval(t *testing.T) {
	build := func(withFail bool) []int64 {
		p := mustNew(t, 10, 25, 25, 1000, 5) // S=3,U=2,N-U=8
		var removals []int64
		r := stepWant(t, p, 0, StepResult{3, 0, 2, false})
		removals = append(removals, r.Removed)
		if err := p.NewReady(3, 0); err != nil {
			t.Fatal(err)
		}
		// a=8,c=3,total=11：up=13-11=2；A=11，r2=min(8,3)=3。
		r = stepWant(t, p, 1000, StepResult{2, 0, 3, false})
		removals = append(removals, r.Removed)
		if err := p.NewReady(2, 1000); err != nil { // d=2
			t.Fatal(err)
		}
		// c=5：readyAt=0 批 3 个，readyAt=1000 批 2 个；a=5。
		if withFail {
			if err := p.NewFail(4, 1000); err != nil { // 先扣最新批 3 个再扣旧批 1 个
				t.Fatal(err)
			}
			t.Logf("判定: NewFail(4) 后批次=%v cs=%d（稳定可用由 5 降到 1）",
				p.batches, p.stableLocked(1000))
			if len(p.batches) != 1 || p.batches[0].readyAt != 0 || p.batches[0].count != 1 {
				t.Fatalf("latest-first 扣批错误，batches=%v", p.batches)
			}
		}
		if withFail {
			// a=5,cs=1,A=6<=8 -> r2=0；total=10 -> up=13-10=3。
			r = stepWant(t, p, 1000, StepResult{3, 0, 0, false})
		} else {
			// readyAt=1000 批此刻未稳定：cs=3,A=8 -> r2=0；total=10 -> up=3。
			r = stepWant(t, p, 1000, StepResult{3, 0, 0, false})
		}
		removals = append(removals, r.Removed)
		// now=2000：第二批也稳定。失败序列 A=6 -> r2=0；对照 A=10 -> r2=2。
		if withFail {
			r = stepWant(t, p, 2000, StepResult{0, 0, 0, false})
		} else {
			r = stepWant(t, p, 2000, StepResult{0, 0, 2, false})
		}
		removals = append(removals, r.Removed)
		return removals
	}

	rFail := build(true)
	rOK := build(false)
	t.Logf("判定: 失败序列 r2=%v（失败后 r2=%d），对照序列 r2=%v（r2=%d）",
		rFail, rFail[2], rOK, rOK[2])
	if rFail[3] != 0 { // a=5,cs=1,A=6<=8
		t.Fatalf("r2 after NewFail = %d, want 0", rFail[3])
	}
	if rOK[3] != 2 { // a=5,cs=5,A=10 -> min(5,2)
		t.Fatalf("r2 without NewFail = %d, want 2", rOK[3])
	}
}

// 连续无进展恰到 P 步 stalled 为真；NewReady 清零；Done 要求 cs==N。
func TestStallThresholdAndNewReadyReset(t *testing.T) {
	// N=1, S=1, U=0：首步 up=1 后 a=1,d=1 卡满，后续无进展。
	p := mustNew(t, 1, 50, 50, 1000, 3)
	stepWant(t, p, 0, StepResult{1, 0, 0, false})
	stepWant(t, p, 1, StepResult{0, 0, 0, false}) // stall 1
	stepWant(t, p, 2, StepResult{0, 0, 0, false}) // stall 2
	stepWant(t, p, 3, StepResult{0, 0, 0, true})  // stall 3 == P
	if p.Done() {
		t.Fatal("Done 不应成立：c=0,cs=0")
	}
	if err := p.NewReady(1, 4); err != nil { // d=1 -> c=1，批次 readyAt=4
		t.Fatal(err)
	}
	if p.stall != 0 {
		t.Fatalf("NewReady 后 stall=%d, want 0", p.stall)
	}
	if p.Done() {
		t.Fatal("Done 不应成立：readyAt=4 在 now=4 尚未稳定")
	}
	stepWant(t, p, 5, StepResult{0, 0, 0, false})    // 无进展，stall 重新变 1
	stepWant(t, p, 1004, StepResult{0, 0, 1, false}) // cs=1，A=2，r2=1；Done，stall 清零
	if !p.Done() {
		t.Fatal("Done 应成立：a+b=0,c=1,cs(1004)=1")
	}
	if p.stall != 0 {
		t.Fatalf("Done 后 stall=%d, want 0", p.stall)
	}
}

// NewFail 与 OldUnready 不清零停滞计数。
func TestStallNotResetByFailOrOldUnready(t *testing.T) {
	p := mustNew(t, 1, 50, 50, 1000, 2) // S=1,U=0,N-U=1
	stepWant(t, p, 0, StepResult{1, 0, 0, false})
	if err := p.NewReady(1, 0); err != nil {
		t.Fatal(err)
	}
	if err := p.NewFail(1, 1); err != nil {
		t.Fatal(err)
	}
	stepWant(t, p, 1, StepResult{0, 0, 0, false}) // stall 1
	if err := p.NewReady(1, 2); err != nil {
		t.Fatal(err)
	}
	if err := p.NewFail(1, 3); err != nil { // 不清零
		t.Fatal(err)
	}
	stepWant(t, p, 3, StepResult{0, 0, 0, false}) // NewReady 已清零，此处 stall 1
	stepWant(t, p, 4, StepResult{0, 0, 0, true})  // NewFail 不清零：stall 2 == P

	p2 := mustNew(t, 4, 50, 0, 0, 2) // S=2,U=1,N-U=3
	p2.stall = 2
	p2.a = 1
	p2.d = 3
	before := p2.snapLocked(0)
	if err := p2.OldUnready(1, 0); err != nil {
		t.Fatal(err)
	}
	t.Logf("判定: OldUnready 前 stall=%d 后 stall=%d（应不变）", before.StallCount, p2.stall)
	if p2.stall != 2 {
		t.Fatalf("OldUnready 后 stall=%d, want 2", p2.stall)
	}
}

// 非法配置整体拒绝。
func TestInvalidConfig(t *testing.T) {
	bad := [][5]int64{
		{0, 0, 0, 0, 1},
		{1_000_001, 0, 0, 0, 1},
		{10, -1, 0, 0, 1},
		{10, 101, 0, 0, 1},
		{10, 0, -1, 0, 1},
		{10, 0, 101, 0, 1},
		{10, 0, 0, -1, 1},
		{10, 0, 0, 1_000_000_000_001, 1},
		{10, 0, 0, 0, 0},
		{10, 0, 0, 0, 1001},
	}
	for i, c := range bad {
		if _, err := New(c[0], int(c[1]), int(c[2]), c[3], int(c[4])); !errors.Is(err, ErrInvalidConfig) {
			t.Fatalf("case %d config=%v err=%v, want ErrInvalidConfig", i, c, err)
		}
	}
}

// 错误顺序：参数非法 -> 时钟回退 -> 超出范围；被拒绝操作不改状态。
func TestRejectionOrderAndNoStateChange(t *testing.T) {
	p := mustNew(t, 3, 10, 10, 0, 2)
	stepWant(t, p, 5, StepResult{1, 0, 0, false}) // S=1,U=0：up=1,d=1

	// 参数非法优先于时钟回退。
	if err := p.NewReady(0, 4); !errors.Is(err, ErrInvalidArg) {
		t.Fatalf("NewReady k=0 err=%v, want ErrInvalidArg", err)
	}
	if _, err := p.Step(-1); !errors.Is(err, ErrInvalidArg) {
		t.Fatalf("Step now=-1 err=%v, want ErrInvalidArg", err)
	}
	if err := p.NewFail(1, -1); !errors.Is(err, ErrInvalidArg) {
		t.Fatalf("NewFail now=-1 err=%v, want ErrInvalidArg", err)
	}
	if err := p.OldUnready(0, -1); !errors.Is(err, ErrInvalidArg) {
		t.Fatalf("OldUnready err=%v, want ErrInvalidArg", err)
	}
	// 时钟回退优先于超出范围（即便 k 也超量）。
	if err := p.NewReady(99, 4); !errors.Is(err, ErrClockRewind) {
		t.Fatalf("NewReady rewind err=%v, want ErrClockRewind", err)
	}
	if err := p.NewFail(99, 4); !errors.Is(err, ErrClockRewind) {
		t.Fatalf("NewFail rewind err=%v, want ErrClockRewind", err)
	}
	if err := p.OldUnready(99, 4); !errors.Is(err, ErrClockRewind) {
		t.Fatalf("OldUnready rewind err=%v, want ErrClockRewind", err)
	}

	before := p.snapLocked(5)
	// 三类超出范围彼此可区分。
	if err := p.NewReady(2, 6); !errors.Is(err, ErrNoUnreadyNew) {
		t.Fatalf("NewReady over d err=%v, want ErrNoUnreadyNew", err)
	}
	if err := p.NewFail(1, 6); !errors.Is(err, ErrNoReadyNew) {
		t.Fatalf("NewFail over c err=%v, want ErrNoReadyNew", err)
	}
	p.a = 0 // 令旧就绪为 0 以触发 OldUnready 超范围
	if err := p.OldUnready(1, 6); !errors.Is(err, ErrNoReadyOld) {
		t.Fatalf("OldUnready over a err=%v, want ErrNoReadyOld", err)
	}
	after := p.snapLocked(6)
	// maxNow 不应被拒绝操作推进，回退到 6 之前先复原 a 再比对。
	p.a = before.OldReady
	after2 := p.snapLocked(5)
	t.Logf("判定: 拒绝前=%+v 拒绝后(now=5)=%+v", before, after2)
	if after2 != before {
		t.Fatalf("被拒绝操作改变了状态: before=%+v after=%+v", before, after2)
	}
	if after.MaxNow != 5 {
		t.Fatalf("拒绝后 maxNow=%d, want 5", after.MaxNow)
	}
	// now=6 与已接受的最大 now=5 不冲突，应可接受。
	if err := p.OldUnready(0, 6); err != nil { // k=0 非法，仅验证不会误判回退
		if !errors.Is(err, ErrInvalidArg) {
			t.Fatalf("err=%v, want ErrInvalidArg", err)
		}
	}
}

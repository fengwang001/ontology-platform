package autoscaler

import (
	"errors"
	"reflect"
	"sync"
	"testing"
)

func mustScaler(t *testing.T, cfg Config) *Scaler {
	t.Helper()
	s, err := NewScaler(cfg)
	if err != nil {
		t.Fatalf("NewScaler: %v", err)
	}
	return s
}

func expectResult(t *testing.T, res Result, err error, action Action, delta, cap, inflight int64) {
	t.Helper()
	if err != nil {
		t.Fatalf("Evaluate: unexpected error %v", err)
	}
	want := Result{Action: action, Delta: delta, Cap: cap, Inflight: inflight}
	if res != want {
		t.Fatalf("Evaluate = %+v, want %+v", res, want)
	}
}

// 题目给出的完整示例追踪。
func TestWorkedExample(t *testing.T) {
	s := mustScaler(t, Config{
		Mn: 2, Mx: 20, C0: 4, H: 70, Lw: 30,
		Up:   []Step{{0, 20}, {10, 50}, {20, 100}},
		Down: []Step{{0, 10}, {10, 30}},
		Ms:   1, W: 5, Cout: 10, Cin: 15,
	})

	// v=5 选 20%，eff=4，delta=max(1,ceil(0.8))=1，追加 (5,1)。
	res, err := s.Evaluate(0, 75)
	expectResult(t, res, err, ActionScaleOut, 1, 4, 1)
	if snap := s.Snapshot(); snap.B0 != 4 || !snap.HasLastOut || snap.LastOut != 0 {
		t.Fatalf("after first scale-out: %+v", snap)
	}

	// 冷却内按 B0=4 计算：delta=ceil(2.0)=2，target=6>eff=5，追加 (8,1)。
	res, err = s.Evaluate(3, 85)
	expectResult(t, res, err, ActionScaleOut, 1, 4, 2)
	if snap := s.Snapshot(); snap.B0 != 4 || snap.LastOut != 0 {
		t.Fatalf("cooldown top-up must keep B0/lastOut: %+v", snap)
	}

	// 批次 (5,1) 恰等 now 就绪并入，metric 落入 [Lw,H) 无动作。
	res, err = s.Evaluate(5, 50)
	expectResult(t, res, err, ActionNone, 0, 5, 1)

	// 仍有在途 (8,1)，缩容被阻止。
	res, err = s.Evaluate(6, 20)
	expectResult(t, res, err, ActionNone, 0, 5, 1)

	// (8,1) 就绪后 cap=6，delta=max(1,floor(1.8))=1，cap=5。
	res, err = s.Evaluate(8, 20)
	expectResult(t, res, err, ActionScaleIn, 1, 5, 0)
	if snap := s.Snapshot(); !snap.HasLastIn || snap.LastIn != 8 {
		t.Fatalf("after scale-in: %+v", snap)
	}

	// now 恰等于 lastOut+Cout，冷却结束，base=eff=5，delta=ceil(2.5)=3。
	res, err = s.Evaluate(10, 80)
	expectResult(t, res, err, ActionScaleOut, 3, 5, 3)
	snap := s.Snapshot()
	if snap.B0 != 5 || snap.LastOut != 10 {
		t.Fatalf("after cooldown end: %+v", snap)
	}
	wantBatches := []Batch{{ReadyAt: 15, Count: 3}}
	if !reflect.DeepEqual(snap.Batches, wantBatches) {
		t.Fatalf("batches = %v, want %v", snap.Batches, wantBatches)
	}
}

// metric 恰等于 H 触发扩容；恰等于 Lw 不触发缩容。
func TestThresholdEquality(t *testing.T) {
	s := mustScaler(t, Config{
		Mn: 1, Mx: 100, C0: 10, H: 100, Lw: 50,
		Up: []Step{{0, 10}}, Down: []Step{{0, 10}},
		Ms: 1, W: 5, Cout: 0, Cin: 0,
	})
	res, err := s.Evaluate(1, 100) // metric == H
	expectResult(t, res, err, ActionScaleOut, 1, 10, 1)

	res, err = s.Evaluate(2, 50) // metric == Lw，位于 [Lw,H)
	expectResult(t, res, err, ActionNone, 0, 10, 1)

	res, err = s.Evaluate(3, 49) // metric < Lw，但在途批次阻止缩容
	expectResult(t, res, err, ActionNone, 0, 10, 1)

	res, err = s.Evaluate(6, 49) // 批次 (6,1) 已就绪，允许缩容
	expectResult(t, res, err, ActionScaleIn, 1, 10, 0)
}

// 档表边界：v 恰等于 lo 时归入下一档。
func TestStepBoundary(t *testing.T) {
	s := mustScaler(t, Config{
		Mn: 1, Mx: 1000, C0: 100, H: 100, Lw: 50,
		Up: []Step{{0, 10}, {10, 20}}, Down: []Step{{0, 5}, {10, 15}},
		Ms: 1, W: 100, Cout: 0, Cin: 0,
	})
	res, err := s.Evaluate(1, 109) // v=9，第一档 10%
	expectResult(t, res, err, ActionScaleOut, 10, 100, 10)

	res, err = s.Evaluate(2, 110) // v=10 恰等于 lo，归第二档 20%，eff=110
	expectResult(t, res, err, ActionScaleOut, 22, 100, 32)

	// 缩容档表边界：u=10 恰等于 lo，归第二档 15%。
	s2 := mustScaler(t, Config{
		Mn: 1, Mx: 1000, C0: 100, H: 100, Lw: 50,
		Up: []Step{{0, 10}}, Down: []Step{{0, 5}, {10, 15}},
		Ms: 1, W: 100, Cout: 0, Cin: 0,
	})
	res, err = s2.Evaluate(1, 40) // u=10，15%，floor(15)=15
	expectResult(t, res, err, ActionScaleIn, 15, 85, 0)
}

// ceil 与 max(ms) 的取舍。
func TestCeilVsMinStep(t *testing.T) {
	// ceil(10*1/100)=1 小于 ms=5，取 ms。
	s := mustScaler(t, Config{
		Mn: 1, Mx: 1000, C0: 10, H: 100, Lw: 50,
		Up: []Step{{0, 1}}, Down: []Step{{0, 10}},
		Ms: 5, W: 1, Cout: 0, Cin: 0,
	})
	res, err := s.Evaluate(1, 100)
	expectResult(t, res, err, ActionScaleOut, 5, 10, 5)

	// ceil(101*10/100)=ceil(10.1)=11 大于 ms=1，取 ceil。
	s2 := mustScaler(t, Config{
		Mn: 1, Mx: 10000, C0: 101, H: 100, Lw: 50,
		Up: []Step{{0, 10}}, Down: []Step{{0, 10}},
		Ms: 1, W: 1, Cout: 0, Cin: 0,
	})
	res, err = s2.Evaluate(1, 100)
	expectResult(t, res, err, ActionScaleOut, 11, 101, 11)

	// 整除时 ceil 不上浮：ceil(100*20/100)=20。
	s3 := mustScaler(t, Config{
		Mn: 1, Mx: 10000, C0: 100, H: 100, Lw: 50,
		Up: []Step{{0, 20}}, Down: []Step{{0, 10}},
		Ms: 1, W: 1, Cout: 0, Cin: 0,
	})
	res, err = s3.Evaluate(1, 100)
	expectResult(t, res, err, ActionScaleOut, 20, 100, 20)
}

// 冷却内用 B0 而不是当前 eff 计算 target。
func TestCooldownUsesB0(t *testing.T) {
	s := mustScaler(t, Config{
		Mn: 1, Mx: 40, C0: 10, H: 100, Lw: 50,
		Up: []Step{{0, 100}, {5, 200}}, Down: []Step{{0, 10}},
		Ms: 1, W: 5, Cout: 100, Cin: 0,
	})
	res, err := s.Evaluate(0, 100) // eff=10，delta=10，追加 (5,10)，B0=10
	expectResult(t, res, err, ActionScaleOut, 10, 10, 10)

	// 冷却内：base=B0=10，delta=20，target=30>eff=20，追加差额 10。
	// 若误用 eff=20 为 base，则 delta=40、追加 20。
	res, err = s.Evaluate(1, 105)
	expectResult(t, res, err, ActionScaleOut, 10, 10, 20)
	if snap := s.Snapshot(); snap.B0 != 10 || snap.LastOut != 0 {
		t.Fatalf("cooldown top-up must keep B0/lastOut: %+v", snap)
	}
}

// 冷却内 target 不大于 eff 时无动作且不更新 lastOut。
func TestCooldownNoActionKeepsLastOut(t *testing.T) {
	s := mustScaler(t, Config{
		Mn: 1, Mx: 40, C0: 10, H: 100, Lw: 50,
		Up: []Step{{0, 100}}, Down: []Step{{0, 10}},
		Ms: 1, W: 5, Cout: 100, Cin: 0,
	})
	res, err := s.Evaluate(0, 100) // 追加 (5,10)，target=20
	expectResult(t, res, err, ActionScaleOut, 10, 10, 10)

	// 冷却内 target=min(40,10+10)=20 等于 eff=20，无动作。
	res, err = s.Evaluate(1, 100)
	expectResult(t, res, err, ActionNone, 0, 10, 10)
	snap := s.Snapshot()
	if snap.B0 != 10 || !snap.HasLastOut || snap.LastOut != 0 {
		t.Fatalf("no-action in cooldown must keep B0/lastOut: %+v", snap)
	}
	if len(snap.Batches) != 1 || snap.Batches[0] != (Batch{ReadyAt: 5, Count: 10}) {
		t.Fatalf("batches changed unexpectedly: %v", snap.Batches)
	}
}

// now 恰等于 lastOut+Cout 时冷却结束，按当前 eff 重新计算。
func TestCooldownEndsExactly(t *testing.T) {
	s := mustScaler(t, Config{
		Mn: 1, Mx: 100, C0: 5, H: 70, Lw: 30,
		Up: []Step{{0, 50}}, Down: []Step{{0, 10}},
		Ms: 1, W: 5, Cout: 10, Cin: 0,
	})
	res, err := s.Evaluate(0, 70) // eff=5，delta=ceil(2.5)=3，追加 (5,3)
	expectResult(t, res, err, ActionScaleOut, 3, 5, 3)

	// now=10 恰等于 0+10：冷却结束；(5,3) 并入，base=eff=8，delta=4。
	res, err = s.Evaluate(10, 70)
	expectResult(t, res, err, ActionScaleOut, 4, 8, 4)
	if snap := s.Snapshot(); snap.B0 != 8 || snap.LastOut != 10 {
		t.Fatalf("cooldown end must refresh B0/lastOut: %+v", snap)
	}
}

// 上限钳制后 target 等于 eff 时不更新 lastOut。
func TestClampToMaxNoLastOutUpdate(t *testing.T) {
	s := mustScaler(t, Config{
		Mn: 1, Mx: 15, C0: 10, H: 100, Lw: 50,
		Up: []Step{{0, 100}}, Down: []Step{{0, 10}},
		Ms: 1, W: 2, Cout: 0, Cin: 0,
	})
	res, err := s.Evaluate(0, 100) // target=min(15,20)=15，追加 (2,5)
	expectResult(t, res, err, ActionScaleOut, 5, 10, 5)

	// (2,5) 并入后 cap=15=eff，target=min(15,30)=15 等于 eff，无动作。
	res, err = s.Evaluate(3, 100)
	expectResult(t, res, err, ActionNone, 0, 15, 0)
	if snap := s.Snapshot(); !snap.HasLastOut || snap.LastOut != 0 || snap.B0 != 10 {
		t.Fatalf("clamped no-action must not update lastOut: %+v", snap)
	}
}

// 批次就绪时刻恰等于 now 时并入。
func TestBatchReadyExactlyNow(t *testing.T) {
	s := mustScaler(t, Config{
		Mn: 1, Mx: 20, C0: 4, H: 70, Lw: 30,
		Up: []Step{{0, 20}}, Down: []Step{{0, 10}},
		Ms: 1, W: 5, Cout: 10, Cin: 0,
	})
	res, err := s.Evaluate(0, 70) // 追加 (5,1)
	expectResult(t, res, err, ActionScaleOut, 1, 4, 1)

	res, err = s.Evaluate(4, 50) // 尚未就绪
	expectResult(t, res, err, ActionNone, 0, 4, 1)

	res, err = s.Evaluate(5, 50) // 恰等就绪时刻，并入
	expectResult(t, res, err, ActionNone, 0, 5, 0)
}

// 缩容 floor 与最小 1 以及下限钳制。
func TestScaleInFloorMinAndClamp(t *testing.T) {
	cfg := func(c0, mn int64, pct int64) Config {
		return Config{
			Mn: mn, Mx: 1000, C0: c0, H: 100, Lw: 50,
			Up: []Step{{0, 10}}, Down: []Step{{0, pct}},
			Ms: 1, W: 1, Cout: 0, Cin: 0,
		}
	}
	// floor(15*10/100)=1。
	s := mustScaler(t, cfg(15, 1, 10))
	res, err := s.Evaluate(1, 0)
	expectResult(t, res, err, ActionScaleIn, 1, 14, 0)

	// floor(5*1/100)=0，最小缩 1。
	s = mustScaler(t, cfg(5, 1, 1))
	res, err = s.Evaluate(1, 0)
	expectResult(t, res, err, ActionScaleIn, 1, 4, 0)

	// delta=5 但下限 Mn=8 钳制，实际只缩 2。
	s = mustScaler(t, cfg(10, 8, 50))
	res, err = s.Evaluate(1, 0)
	expectResult(t, res, err, ActionScaleIn, 2, 8, 0)

	// cap 已等于 Mn，target 不小于 cap，无动作且不记 lastIn。
	s = mustScaler(t, cfg(8, 8, 50))
	res, err = s.Evaluate(1, 0)
	expectResult(t, res, err, ActionNone, 0, 8, 0)
	if snap := s.Snapshot(); snap.HasLastIn {
		t.Fatalf("no-action scale-in must not set lastIn: %+v", snap)
	}
}

// 缩容冷却独立于扩容冷却：Cin 不阻塞扩容，Cout 不阻塞缩容。
func TestCooldownsIndependent(t *testing.T) {
	s := mustScaler(t, Config{
		Mn: 1, Mx: 100, C0: 10, H: 100, Lw: 50,
		Up: []Step{{0, 100}}, Down: []Step{{0, 100}},
		Ms: 1, W: 1, Cout: 100, Cin: 100,
	})
	res, err := s.Evaluate(0, 100) // 扩容，追加 (1,10)，lastOut=0，B0=10
	expectResult(t, res, err, ActionScaleOut, 10, 10, 10)

	res, err = s.Evaluate(1, 50) // 批次并入，cap=20
	expectResult(t, res, err, ActionNone, 0, 20, 0)

	// 处于扩容冷却 Cout 内，但缩容不受 Cout 影响。
	res, err = s.Evaluate(2, 0)
	expectResult(t, res, err, ActionScaleIn, 19, 1, 0)

	// 处于缩容冷却 Cin 内，但扩容只受 Cout 约束（冷却内按 B0=10 追加差额）。
	// target=min(100,10+10)=20，eff=1，追加 (4,19)。
	res, err = s.Evaluate(3, 100)
	expectResult(t, res, err, ActionScaleOut, 19, 1, 19)

	// (4,19) 并入后 cap=20；Cin 内再次缩容被阻止。
	res, err = s.Evaluate(4, 0)
	expectResult(t, res, err, ActionNone, 0, 20, 0)
}

// 同一 metric 在同一 now 重复评估，第二次起无动作（含 Cout=0 的情形）。
func TestSameNowSameMetricIdempotent(t *testing.T) {
	s := mustScaler(t, Config{
		Mn: 1, Mx: 1000, C0: 10, H: 100, Lw: 50,
		Up: []Step{{0, 100}}, Down: []Step{{0, 100}},
		Ms: 1, W: 5, Cout: 0, Cin: 0,
	})
	res, err := s.Evaluate(7, 100)
	expectResult(t, res, err, ActionScaleOut, 10, 10, 10)
	res, err = s.Evaluate(7, 100) // 重复：无动作
	expectResult(t, res, err, ActionNone, 0, 10, 10)

	// 同一 now 的不同 metric 仍可触发冷却外/内规则。
	res, err = s.Evaluate(7, 150)
	expectResult(t, res, err, ActionScaleOut, 20, 10, 30)

	// 缩容侧同样幂等。
	s2 := mustScaler(t, Config{
		Mn: 1, Mx: 1000, C0: 10, H: 100, Lw: 50,
		Up: []Step{{0, 100}}, Down: []Step{{0, 50}},
		Ms: 1, W: 5, Cout: 0, Cin: 0,
	})
	res, err = s2.Evaluate(3, 0)
	expectResult(t, res, err, ActionScaleIn, 5, 5, 0)
	res, err = s2.Evaluate(3, 0)
	expectResult(t, res, err, ActionNone, 0, 5, 0)
}

// 参数非法拒绝：now/metric 越界，且不改变任何状态。
func TestInvalidParamRejected(t *testing.T) {
	s := mustScaler(t, Config{
		Mn: 1, Mx: 100, C0: 10, H: 100, Lw: 50,
		Up: []Step{{0, 100}}, Down: []Step{{0, 100}},
		Ms: 1, W: 5, Cout: 0, Cin: 0,
	})
	res, err := s.Evaluate(0, 100)
	expectResult(t, res, err, ActionScaleOut, 10, 10, 10)
	before := s.Snapshot()

	for _, p := range [][2]int64{
		{-1, 50},                    // now < 0
		{1_000_000_000_000_001, 50}, // now > 10^15
		{1, -1},                     // metric < 0
		{1, 1_000_000_001},          // metric > 10^9
	} {
		if _, err := s.Evaluate(p[0], p[1]); !errors.Is(err, ErrInvalidParam) {
			t.Fatalf("Evaluate(%d,%d) err = %v, want ErrInvalidParam", p[0], p[1], err)
		}
	}
	if after := s.Snapshot(); !reflect.DeepEqual(after, before) {
		t.Fatalf("rejected evaluates must not change state:\nbefore %+v\nafter  %+v", before, after)
	}
}

// 时钟回退拒绝：now 小于已接受的最大 now，且不并入就绪批次；
// 同时校验拒绝原因按参数非法、时钟回退的顺序只报第一个。
func TestClockRegressionRejected(t *testing.T) {
	s := mustScaler(t, Config{
		Mn: 1, Mx: 100, C0: 10, H: 100, Lw: 50,
		Up: []Step{{0, 100}}, Down: []Step{{0, 100}},
		Ms: 1, W: 5, Cout: 0, Cin: 0,
	})
	res, err := s.Evaluate(0, 100) // 追加 (5,10)
	expectResult(t, res, err, ActionScaleOut, 10, 10, 10)

	res, err = s.Evaluate(4, 50) // 推进 maxNow 到 4，批次 (5,10) 未就绪
	expectResult(t, res, err, ActionNone, 0, 10, 10)
	before := s.Snapshot()

	// now=3 回退：批次 (5,10) 不得并入，状态完全不变。
	if _, err := s.Evaluate(3, 100); !errors.Is(err, ErrClockRegression) {
		t.Fatalf("err = %v, want ErrClockRegression", err)
	}
	// now 同时越界且回退：只报参数非法。
	if _, err := s.Evaluate(-1, 50); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("err = %v, want ErrInvalidParam (first reason wins)", err)
	}
	if after := s.Snapshot(); !reflect.DeepEqual(after, before) {
		t.Fatalf("rejected evaluates must not change state:\nbefore %+v\nafter  %+v", before, after)
	}

	// 恰等于 maxNow 被接受。
	res, err = s.Evaluate(4, 50)
	expectResult(t, res, err, ActionNone, 0, 10, 10)
	// 回退解除后正常推进，批次在 now=5 并入。
	res, err = s.Evaluate(5, 50)
	expectResult(t, res, err, ActionNone, 0, 20, 0)
}

// 配置非法整体拒绝。
func TestConfigValidation(t *testing.T) {
	valid := Config{
		Mn: 1, Mx: 10, C0: 5, H: 100, Lw: 50,
		Up: []Step{{0, 10}}, Down: []Step{{0, 10}},
		Ms: 1, W: 1, Cout: 0, Cin: 0,
	}
	if _, err := NewScaler(valid); err != nil {
		t.Fatalf("valid config rejected: %v", err)
	}
	cases := map[string]Config{
		"Mn 为 0":   {Mn: 0, Mx: 10, C0: 5, H: 100, Lw: 50, Up: valid.Up, Down: valid.Down, Ms: 1, W: 1},
		"Mn 超界":    {Mn: 1_000_001, Mx: 1_000_001, C0: 1_000_001, H: 100, Lw: 50, Up: valid.Up, Down: valid.Down, Ms: 1, W: 1},
		"Mx 小于 Mn": {Mn: 5, Mx: 4, C0: 5, H: 100, Lw: 50, Up: valid.Up, Down: valid.Down, Ms: 1, W: 1},
		"c0 越界":    {Mn: 1, Mx: 10, C0: 11, H: 100, Lw: 50, Up: valid.Up, Down: valid.Down, Ms: 1, W: 1},
		"Lw 不小于 H": {Mn: 1, Mx: 10, C0: 5, H: 50, Lw: 50, Up: valid.Up, Down: valid.Down, Ms: 1, W: 1},
		"H 超界":     {Mn: 1, Mx: 10, C0: 5, H: 1_000_000_001, Lw: 50, Up: valid.Up, Down: valid.Down, Ms: 1, W: 1},
		"扩容档表为空":   {Mn: 1, Mx: 10, C0: 5, H: 100, Lw: 50, Down: valid.Down, Ms: 1, W: 1},
		"档表不从 0 起": {Mn: 1, Mx: 10, C0: 5, H: 100, Lw: 50, Up: []Step{{1, 10}}, Down: valid.Down, Ms: 1, W: 1},
		"档表非严格递增":  {Mn: 1, Mx: 10, C0: 5, H: 100, Lw: 50, Up: []Step{{0, 10}, {0, 20}}, Down: valid.Down, Ms: 1, W: 1},
		"档表 lo 倒退": {Mn: 1, Mx: 10, C0: 5, H: 100, Lw: 50, Up: []Step{{0, 10}, {5, 20}, {4, 30}}, Down: valid.Down, Ms: 1, W: 1},
		"pct 为 0":  {Mn: 1, Mx: 10, C0: 5, H: 100, Lw: 50, Up: []Step{{0, 0}}, Down: valid.Down, Ms: 1, W: 1},
		"pct 超界":   {Mn: 1, Mx: 10, C0: 5, H: 100, Lw: 50, Up: valid.Up, Down: []Step{{0, 1001}}, Ms: 1, W: 1},
		"ms 为 0":   {Mn: 1, Mx: 10, C0: 5, H: 100, Lw: 50, Up: valid.Up, Down: valid.Down, Ms: 0, W: 1},
		"W 为 0":    {Mn: 1, Mx: 10, C0: 5, H: 100, Lw: 50, Up: valid.Up, Down: valid.Down, Ms: 1, W: 0},
		"Cout 为负":  {Mn: 1, Mx: 10, C0: 5, H: 100, Lw: 50, Up: valid.Up, Down: valid.Down, Ms: 1, W: 1, Cout: -1},
		"Cin 超界":   {Mn: 1, Mx: 10, C0: 5, H: 100, Lw: 50, Up: valid.Up, Down: valid.Down, Ms: 1, W: 1, Cin: 1_000_000_001},
	}
	for name, cfg := range cases {
		if _, err := NewScaler(cfg); !errors.Is(err, ErrInvalidConfig) {
			t.Fatalf("%s: err = %v, want ErrInvalidConfig", name, err)
		}
	}
}

// 相同 Evaluate 序列重放得到完全相同的动作、容量与批次。
func TestReplayDeterminism(t *testing.T) {
	cfg := Config{
		Mn: 2, Mx: 20, C0: 4, H: 70, Lw: 30,
		Up: []Step{{0, 20}, {10, 50}, {20, 100}}, Down: []Step{{0, 10}, {10, 30}},
		Ms: 1, W: 5, Cout: 10, Cin: 15,
	}
	seq := [][2]int64{{0, 75}, {3, 85}, {5, 50}, {6, 20}, {8, 20}, {10, 80}, {11, 0}, {20, 95}, {20, 95}, {25, 10}}
	run := func() ([]Result, Snapshot) {
		s := mustScaler(t, cfg)
		results := make([]Result, 0, len(seq))
		for _, p := range seq {
			res, err := s.Evaluate(p[0], p[1])
			if err != nil {
				t.Fatalf("Evaluate(%d,%d): %v", p[0], p[1], err)
			}
			results = append(results, res)
		}
		return results, s.Snapshot()
	}
	r1, s1 := run()
	r2, s2 := run()
	if !reflect.DeepEqual(r1, r2) || !reflect.DeepEqual(s1, s2) {
		t.Fatalf("replay mismatch:\n%v %+v\n%v %+v", r1, s1, r2, s2)
	}
}

// 并发调用等价于某个串行顺序：最终状态满足全部不变量。
func TestConcurrentEvaluate(t *testing.T) {
	s := mustScaler(t, Config{
		Mn: 2, Mx: 50, C0: 10, H: 100, Lw: 40,
		Up: []Step{{0, 50}, {10, 100}}, Down: []Step{{0, 20}},
		Ms: 1, W: 3, Cout: 2, Cin: 2,
	})
	const workers = 8
	const rounds = 200
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(seed int64) {
			defer wg.Done()
			for i := 0; i < rounds; i++ {
				now := int64(i / 4) // 多个 goroutine 可能用相同 now 竞争
				metric := (seed*37 + int64(i)*13) % 160
				_, _ = s.Evaluate(now, metric)
			}
		}(int64(w))
	}
	wg.Wait()

	snap := s.Snapshot()
	if snap.Cap < 2 || snap.Cap > 50 {
		t.Fatalf("cap out of bounds: %+v", snap)
	}
	if snap.Eff() > 50 {
		t.Fatalf("cap+inflight exceeds Mx: %+v", snap)
	}
	for _, b := range snap.Batches {
		if b.ReadyAt <= snap.MaxNow {
			t.Fatalf("batch readyAt %d <= maxNow %d", b.ReadyAt, snap.MaxNow)
		}
	}
}

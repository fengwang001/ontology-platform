package autoscaler

import (
	"errors"
	"testing"
)

// TestThresholdExactBoundaries：metric 恰等于 H 触发扩容；恰等于 Lw 不缩容。
func TestThresholdExactBoundaries(t *testing.T) {
	c := mustNew(t, exampleConfig())

	// metric == H == 70：v=0，档 (0,20)，扩容。
	r := mustEval(t, c, 0, 70)
	checkResult(t, r, ActionScaleOut, 1, 4, 1)

	// 等待在途就绪并越过扩容冷却。
	mustEval(t, c, 10, 50)

	// metric == Lw == 30：位于 [Lw, H)，无动作（不是缩容）。
	r = mustEval(t, c, 11, 30)
	checkResult(t, r, ActionNone, 0, 5, 0)
	if c.State().LastIn != -1 {
		t.Fatalf("lastIn=%d, want -1 (metric==Lw 不得缩容)", c.State().LastIn)
	}
}

// TestTierBoundary：v 恰等于下一档 lo 时归下一档。
func TestTierBoundary(t *testing.T) {
	c := mustNew(t, exampleConfig())

	// v = 85-70 = 15 ∈ [10,20)，pct=50：delta=ceil(4*0.5)=2。
	checkResult(t, mustEval(t, c, 0, 85), ActionScaleOut, 2, 4, 2)

	c2 := mustNew(t, exampleConfig())
	// v = 90-70 = 20，恰等于档 (20,100) 的 lo，归该档：delta=ceil(4*1.0)=4。
	checkResult(t, mustEval(t, c2, 0, 90), ActionScaleOut, 4, 4, 4)

	// 缩容档边界：u = Lw-metric = 10 恰等于档 (10,30) 的 lo。
	c3 := mustNew(t, exampleConfig())
	mustEval(t, c3, 0, 50) // 推进 maxNow，不改变容量
	// u=10 → pct=30，delta=max(1,floor(4*0.3))=1。
	checkResult(t, mustEval(t, c3, 1, 20), ActionScaleIn, 1, 3, 0)
}

// TestCeilAndMinStep：ceil 与 max(ms) 的取舍。
func TestCeilAndMinStep(t *testing.T) {
	cfg := exampleConfig()

	// ceil 生效：base=4，pct=20 → ceil(0.8)=1 > ms=1... 用 pct=50 验证 ceil(2.5)=3。
	c := mustNew(t, cfg)
	checkResult(t, mustEval(t, c, 0, 85), ActionScaleOut, 2, 4, 2) // ceil(4*0.5)=2

	// ms 生效：ms=5，ceil(0.8)=1 < 5 → delta=5。
	cfg2 := exampleConfig()
	cfg2.MinStep = 5
	c2 := mustNew(t, cfg2)
	checkResult(t, mustEval(t, c2, 0, 75), ActionScaleOut, 5, 4, 5)

	// ceil 截断：base=5，pct=50 → ceil(2.5)=3。
	cfg3 := exampleConfig()
	cfg3.Initial = 5
	c3 := mustNew(t, cfg3)
	checkResult(t, mustEval(t, c3, 0, 85), ActionScaleOut, 3, 5, 3)
}

// TestCooldownNoActionKeepsLastOut：冷却内 target 不大于 eff 时无动作且不更新 lastOut。
func TestCooldownNoActionKeepsLastOut(t *testing.T) {
	c := mustNew(t, exampleConfig())

	// now=0：追加 (5,1)，B0=4，lastOut=0，target=5。
	checkResult(t, mustEval(t, c, 0, 75), ActionScaleOut, 1, 4, 1)

	// now=1：冷却内（1 < 0+10），base=B0=4，pct=20 → delta=1，target=5 == eff=5。
	// 无动作，且 B0、lastOut 不变。
	checkResult(t, mustEval(t, c, 1, 75), ActionNone, 0, 4, 1)
	s := c.State()
	if s.B0 != 4 || s.LastOut != 0 {
		t.Fatalf("B0=%d lastOut=%d, want unchanged 4/0", s.B0, s.LastOut)
	}
}

// TestCooldownEndsExactly：now 恰等于 lastOut+Cout 时冷却结束（非冷却扩容）。
func TestCooldownEndsExactly(t *testing.T) {
	c := mustNew(t, exampleConfig())

	checkResult(t, mustEval(t, c, 0, 75), ActionScaleOut, 1, 4, 1) // lastOut=0
	// now=10 == lastOut+Cout：非冷却，base=eff=5（批次 (5,1) 已就绪并入）。
	checkResult(t, mustEval(t, c, 10, 75), ActionScaleOut, 1, 5, 1)
	s := c.State()
	if s.B0 != 5 || s.LastOut != 10 {
		t.Fatalf("B0=%d lastOut=%d, want 5/10", s.B0, s.LastOut)
	}
	// now=9 时仍在冷却内（对照）：新控制器验证。
	c2 := mustNew(t, exampleConfig())
	checkResult(t, mustEval(t, c2, 0, 75), ActionScaleOut, 1, 4, 1)
	// 冷却内 base=B0=4，pct=20，target=5 == eff=5 → 无动作，lastOut 不变。
	checkResult(t, mustEval(t, c2, 9, 75), ActionNone, 0, 5, 0)
	if c2.State().LastOut != 0 {
		t.Fatalf("lastOut=%d, want 0", c2.State().LastOut)
	}
}

// TestMaxClampNoLastOutUpdate：上限钳制后 target 等于 eff 时不更新 lastOut。
func TestMaxClampNoLastOutUpdate(t *testing.T) {
	cfg := exampleConfig()
	cfg.Initial = 18 // 接近 Mx=20
	c := mustNew(t, cfg)

	// now=0：v=5 → pct=20，delta=ceil(3.6)=4，target=min(20,22)=20 > eff=18，
	// 追加 (5,2)，lastOut=0。
	checkResult(t, mustEval(t, c, 0, 75), ActionScaleOut, 2, 18, 2)

	// now=10：批次就绪，cap=20=eff=Mx。非冷却，base=20，
	// target=min(20,24)=20 == eff → 无动作，lastOut 不更新。
	checkResult(t, mustEval(t, c, 10, 75), ActionNone, 0, 20, 0)
	s := c.State()
	if s.LastOut != 0 || s.B0 != 18 {
		t.Fatalf("lastOut=%d B0=%d, want 0/18", s.LastOut, s.B0)
	}
}

// TestScaleInFloorMinOneClamp：缩容 floor、最小 1 与下限钳制。
func TestScaleInFloorMinOneClamp(t *testing.T) {
	// floor：cap=6，pct=30 → floor(1.8)=1。
	cfg := exampleConfig()
	cfg.Initial = 6
	c := mustNew(t, cfg)
	checkResult(t, mustEval(t, c, 0, 20), ActionScaleIn, 1, 5, 0)

	// 最小 1：cap=2，pct=10 → floor(0.2)=0 → delta=1；Mn=1 允许降到 1。
	cfg2 := exampleConfig()
	cfg2.Min, cfg2.Initial = 1, 2
	c2 := mustNew(t, cfg2)
	checkResult(t, mustEval(t, c2, 0, 25), ActionScaleIn, 1, 1, 0)

	// 下限钳制：cap=5，Mn=4，pct=100 → delta=5，target=max(4,0)=4，减 1。
	cfg3 := exampleConfig()
	cfg3.Min, cfg3.Initial = 4, 5
	cfg3.Down = []Tier{{0, 100}}
	c3 := mustNew(t, cfg3)
	checkResult(t, mustEval(t, c3, 0, 0), ActionScaleIn, 1, 4, 0)

	// cap 已等于 Mn：target==cap，无动作且不更新 lastIn。
	cfg4 := exampleConfig()
	cfg4.Initial = 2 // == Mn
	c4 := mustNew(t, cfg4)
	checkResult(t, mustEval(t, c4, 0, 0), ActionNone, 0, 2, 0)
	if c4.State().LastIn != -1 {
		t.Fatalf("lastIn=%d, want -1", c4.State().LastIn)
	}
}

// TestScaleInCooldownIndependent：缩容冷却独立于扩容冷却。
func TestScaleInCooldownIndependent(t *testing.T) {
	c := mustNew(t, exampleConfig())

	// 扩容并等待就绪：lastOut=0，cap=5。
	checkResult(t, mustEval(t, c, 0, 75), ActionScaleOut, 1, 4, 1)
	checkResult(t, mustEval(t, c, 5, 50), ActionNone, 0, 5, 0)
	checkResult(t, mustEval(t, c, 8, 50), ActionNone, 0, 5, 0)

	// 扩容冷却未结束（8 < 0+10），但缩容不受 Cout 影响：允许缩容。
	checkResult(t, mustEval(t, c, 8, 20), ActionScaleIn, 1, 4, 0)
	if c.State().LastIn != 8 {
		t.Fatalf("lastIn=%d, want 8", c.State().LastIn)
	}

	// 缩容冷却内（now=9 < 8+15）：无动作。
	checkResult(t, mustEval(t, c, 9, 20), ActionNone, 0, 4, 0)

	// 缩容冷却内的扩容不受影响：now=9 仍在扩容冷却（9 < 0+10? 否，9<10 是），
	// 处于扩容冷却内，base=B0=4，pct=20，target=5 > eff=4 → 追加。
	checkResult(t, mustEval(t, c, 9, 75), ActionScaleOut, 1, 4, 1)

	// now=23 == lastIn+Cin：缩容冷却结束，允许再次缩容（先在途就绪）。
	checkResult(t, mustEval(t, c, 14, 50), ActionNone, 0, 5, 0)
	checkResult(t, mustEval(t, c, 23, 20), ActionScaleIn, 1, 4, 0)
	if c.State().LastIn != 23 {
		t.Fatalf("lastIn=%d, want 23", c.State().LastIn)
	}
}

// TestInvalidParamsRejected：参数非法拒绝，且优先于时钟回退。
func TestInvalidParamsRejected(t *testing.T) {
	c := mustNew(t, exampleConfig())
	mustEval(t, c, 5, 50) // maxNow=5

	cases := []struct {
		name        string
		now, metric int64
	}{
		{"now 为负", -1, 50},
		{"now 超界", 1_000_000_000_000_001, 50},
		{"metric 为负", 6, -1},
		{"metric 超界", 6, 1_000_000_001},
		{"参数非法优先于时钟回退", -1, 2_000_000_000},
	}
	for _, tc := range cases {
		before := c.State()
		_, err := c.Evaluate(tc.now, tc.metric)
		if !errors.Is(err, ErrInvalidParam) {
			t.Fatalf("%s: err=%v, want ErrInvalidParam", tc.name, err)
		}
		after := c.State()
		if !stateEqual(before, after) {
			t.Fatalf("%s: 状态被改变 before=%+v after=%+v", tc.name, before, after)
		}
	}

	// 边界值合法：now=1e15、metric=1e9 被接受。
	if _, err := c.Evaluate(1_000_000_000_000_000, 1_000_000_000); err != nil {
		t.Fatalf("边界值应被接受: %v", err)
	}
}

// TestClockBackwardRejected：时钟回退拒绝，且不并入批次、不改变任何状态。
func TestClockBackwardRejected(t *testing.T) {
	c := mustNew(t, exampleConfig())

	// 制造一个在途批次 (5,1)，并推进 maxNow 到 10。
	checkResult(t, mustEval(t, c, 0, 75), ActionScaleOut, 1, 4, 1)
	checkResult(t, mustEval(t, c, 10, 50), ActionNone, 0, 5, 0)

	// 再造一个批次 (15,3)，随后回退到 9：批次就绪时刻 15 > 9，
	// 拒绝时不得并入，状态完全不变。
	checkResult(t, mustEval(t, c, 10, 85), ActionScaleOut, 3, 5, 3)
	before := c.State()
	if _, err := c.Evaluate(9, 50); !errors.Is(err, ErrClockBackward) {
		t.Fatalf("err=%v, want ErrClockBackward", err)
	}
	if after := c.State(); !stateEqual(before, after) {
		t.Fatalf("状态被改变 before=%+v after=%+v", before, after)
	}

	// 回退拒绝不推进 maxNow：now=10 仍被接受。
	if _, err := c.Evaluate(10, 60); err != nil {
		t.Fatalf("now=10 应被接受: %v", err)
	}
}

// TestDuplicateEvalSameNow：同一 metric 在同一 now 重复评估，第二次起无动作。
func TestDuplicateEvalSameNow(t *testing.T) {
	// Cout=0、Cin=0 时也必须保证幂等。
	cfg := exampleConfig()
	cfg.CoolOut, cfg.CoolIn = 0, 0
	c := mustNew(t, cfg)

	checkResult(t, mustEval(t, c, 5, 90), ActionScaleOut, 4, 4, 4)
	checkResult(t, mustEval(t, c, 5, 90), ActionNone, 0, 4, 4) // 重复：无动作
	checkResult(t, mustEval(t, c, 5, 90), ActionNone, 0, 4, 4)

	// 同一 now 不同 metric 不受去重影响（死区无动作）。
	checkResult(t, mustEval(t, c, 5, 50), ActionNone, 0, 4, 4)

	// 缩容幂等：Cin=0 也不会连续缩。
	c2 := mustNew(t, cfg)
	checkResult(t, mustEval(t, c2, 0, 20), ActionScaleIn, 1, 3, 0)
	checkResult(t, mustEval(t, c2, 0, 20), ActionNone, 0, 3, 0)

	// 同一 metric 在更晚的 now 可再次生效。
	checkResult(t, mustEval(t, c2, 1, 20), ActionScaleIn, 1, 2, 0)
}

// TestNoActionStillAccepted：无动作的 Evaluate 仍并入就绪批次并推进 maxNow。
func TestNoActionStillAccepted(t *testing.T) {
	c := mustNew(t, exampleConfig())
	checkResult(t, mustEval(t, c, 0, 75), ActionScaleOut, 1, 4, 1) // 批次 (5,1)

	// 死区评估：无动作，但并入批次并推进 maxNow。
	checkResult(t, mustEval(t, c, 5, 50), ActionNone, 0, 5, 0)
	if c.State().MaxNow != 5 {
		t.Fatalf("maxNow=%d, want 5", c.State().MaxNow)
	}
	if _, err := c.Evaluate(4, 50); !errors.Is(err, ErrClockBackward) {
		t.Fatalf("err=%v, want ErrClockBackward", err)
	}
}

// TestConfigValidation：非法配置整体拒绝。
func TestConfigValidation(t *testing.T) {
	valid := exampleConfig()
	bad := []struct {
		name   string
		mutate func(*Config)
	}{
		{"Min 为 0", func(c *Config) { c.Min = 0 }},
		{"Min 超界", func(c *Config) { c.Min = 1_000_001 }},
		{"Max 小于 Min", func(c *Config) { c.Max = c.Min - 1 }},
		{"Max 超界", func(c *Config) { c.Max = 1_000_001 }},
		{"c0 小于 Min", func(c *Config) { c.Initial = c.Min - 1 }},
		{"c0 大于 Max", func(c *Config) { c.Initial = c.Max + 1 }},
		{"Lw 等于 H", func(c *Config) { c.Low = c.High }},
		{"Lw 大于 H", func(c *Config) { c.Low = c.High + 1 }},
		{"H 超界", func(c *Config) { c.High = 1_000_000_001 }},
		{"扩容档表为空", func(c *Config) { c.Up = nil }},
		{"缩容档表为空", func(c *Config) { c.Down = nil }},
		{"档表不从 0 起", func(c *Config) { c.Up = []Tier{{1, 20}} }},
		{"档表非严格递增", func(c *Config) { c.Up = []Tier{{0, 20}, {0, 30}} }},
		{"档表 lo 回退", func(c *Config) { c.Up = []Tier{{0, 20}, {5, 30}, {4, 40}} }},
		{"pct 为 0", func(c *Config) { c.Up = []Tier{{0, 0}} }},
		{"pct 超界", func(c *Config) { c.Down = []Tier{{0, 1001}} }},
		{"ms 为 0", func(c *Config) { c.MinStep = 0 }},
		{"ms 超界", func(c *Config) { c.MinStep = 1_000_001 }},
		{"W 为 0", func(c *Config) { c.Warmup = 0 }},
		{"W 超界", func(c *Config) { c.Warmup = 1_000_000_001 }},
		{"Cout 为负", func(c *Config) { c.CoolOut = -1 }},
		{"Cin 超界", func(c *Config) { c.CoolIn = 1_000_000_001 }},
	}
	for _, tc := range bad {
		cfg := valid
		tc.mutate(&cfg)
		if _, err := New(cfg); !errors.Is(err, ErrInvalidConfig) {
			t.Fatalf("%s: err=%v, want ErrInvalidConfig", tc.name, err)
		}
	}
	if _, err := New(valid); err != nil {
		t.Fatalf("合法配置被拒绝: %v", err)
	}
}

func stateEqual(a, b State) bool {
	return a.Cap == b.Cap && a.InflightTotal == b.InflightTotal &&
		a.B0 == b.B0 && a.LastOut == b.LastOut && a.LastIn == b.LastIn &&
		a.MaxNow == b.MaxNow && batchesEqual(a.Batches, b.Batches)
}

func batchesEqual(a, b []Batch) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

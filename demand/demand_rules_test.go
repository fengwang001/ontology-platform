package demand_test

import (
	"math"
	"testing"

	"ontology/demand"
)

func baseCfg() demand.Config {
	return demand.Config{ContractKW: 100, WindowSec: 60, SlipSec: 30, MaxPowerKW: 1000}
}

func mkLoad(id int, kw int64, pri int, minOn, minOff int64) demand.LoadSpec {
	return demand.LoadSpec{ID: id, RatedKW: kw, Priority: pri, MinOnSec: minOn, MinOffSec: minOff}
}

func kindOf(t *testing.T, err error) demand.ErrKind {
	t.Helper()
	if err == nil {
		t.Fatalf("期望错误，实际为 nil")
	}
	e, ok := err.(*demand.Error)
	if !ok {
		t.Fatalf("错误类型不是 *demand.Error: %T", err)
	}
	return e.Kind
}

func approx(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

func hasAction(as []demand.Action, k demand.ActionKind, id int) bool {
	for _, a := range as {
		if a.Kind == k && a.LoadID == id {
			return true
		}
	}
	return false
}

func shedIDs(as []demand.Action) []int {
	var out []int
	for _, a := range as {
		if a.Kind == demand.ActionShed {
			out = append(out, a.LoadID)
		}
	}
	return out
}

func sameIDs(a, b []int) bool {
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

func restoredAt(as []demand.Action, id int) bool {
	return hasAction(as, demand.ActionRestore, id)
}

// 恰好等于合同需量不算越限。
func TestExactContractNotExceed(t *testing.T) {
	c, _ := demand.New(baseCfg())
	_ = c.AddLoad(0, mkLoad(1, 10, 1, 0, 0))
	r, err := c.Report(30, 100*30)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Actions) != 0 || r.StillExceed {
		t.Fatalf("恰好等于合同需量不应动作，got %+v", r.Actions)
	}
}

// 窗口结束对齐边界；早于首次上报部分按零用电计入全长平均；峰值并列取较早者。
func TestWindowCloseOnBoundary(t *testing.T) {
	c, _ := demand.New(baseCfg())
	_, _ = c.Report(30, 50*30)
	p := c.Peak() // end=30 窗口长60s，仅(0,30]有1500 => 25kW
	if p == nil || p.EndAt != 30 || !approx(p.PowerKW.Float64(), 25) {
		t.Fatalf("end=30 应记录 25kW, got %+v", p)
	}
	_, _ = c.Report(60, 50*30)
	p = c.Peak() // end=60 完整窗口 => 50kW
	if p.EndAt != 60 || !approx(p.PowerKW.Float64(), 50) {
		t.Fatalf("期望 50kW@60, got %+v", p)
	}
	_, _ = c.Report(90, 50*30)
	p = c.Peak() // 仍为50kW，并列取较早者
	if p.EndAt != 60 {
		t.Fatalf("并列峰值应保留较早结束时刻 60, got %d", p.EndAt)
	}
}

// 最短接入：差一秒不可切，恰好满足可切。
func TestMinOnBoundary(t *testing.T) {
	cfg := baseCfg()

	c1, _ := demand.New(cfg)
	_ = c1.AddLoad(0, mkLoad(1, 5, 1, 0, 0))
	_ = c1.AddLoad(0, mkLoad(2, 50, 2, 30, 0))
	_, _ = c1.Report(28, 50*28)
	r, _ := c1.Report(29, 200) // 接入29s<30s，越限但不可切
	if len(r.Actions) != 0 || !r.StillExceed {
		t.Fatalf("最短接入差1s不应切除，got %v still=%v", r.Actions, r.StillExceed)
	}

	c2, _ := demand.New(cfg)
	_ = c2.AddLoad(0, mkLoad(1, 5, 1, 0, 0))
	_ = c2.AddLoad(0, mkLoad(2, 50, 2, 30, 0))
	_, _ = c2.Report(29, 50*29)
	r, _ = c2.Report(30, 200) // 接入恰满30s，可切
	if !hasAction(r.Actions, demand.ActionShed, 2) {
		t.Fatalf("最短接入恰好满足后应可切除，got %+v", r.Actions)
	}
}

// 最短断开：差一秒不可恢复，恰好满足可恢复。
func TestMinOffBoundary(t *testing.T) {
	cfg := demand.Config{ContractKW: 100, WindowSec: 60, SlipSec: 10, MaxPowerKW: 1000}

	c1, _ := demand.New(cfg)
	_ = c1.AddLoad(0, mkLoad(1, 1, 1, 0, 0))
	_ = c1.AddLoad(0, mkLoad(2, 15, 2, 0, 30))
	_, _ = c1.Report(10, 116*10)
	_, _ = c1.Report(39, 80*29)
	r, _ := c1.Report(40, 80) // 断开恰满30s
	if !restoredAt(r.Actions, 2) {
		t.Fatalf("断开恰好30s且功率允许时应恢复，got %+v", r.Actions)
	}

	c2, _ := demand.New(cfg)
	_ = c2.AddLoad(0, mkLoad(1, 1, 1, 0, 0))
	_ = c2.AddLoad(0, mkLoad(2, 15, 2, 0, 30))
	_, _ = c2.Report(10, 116*10)
	r, _ = c2.Report(39, 80*29) // 断开29s
	if restoredAt(r.Actions, 2) {
		t.Fatalf("断开29s不应恢复，got %+v", r.Actions)
	}
}

// 切除并列第二层：优先级数字之和最大。
func TestShedTiePrioritySum(t *testing.T) {
	cfg := demand.Config{ContractKW: 120, WindowSec: 120, SlipSec: 60, MaxPowerKW: 1000}
	c, _ := demand.New(cfg)
	_ = c.AddLoad(0, mkLoad(1, 10, 1, 0, 0))
	_ = c.AddLoad(0, mkLoad(2, 30, 2, 0, 0))
	_ = c.AddLoad(0, mkLoad(3, 30, 2, 0, 0))
	_ = c.AddLoad(0, mkLoad(4, 30, 3, 0, 0))
	_ = c.AddLoad(0, mkLoad(5, 30, 3, 0, 0))
	r, _ := c.Report(60, 150*60)
	if !sameIDs(shedIDs(r.Actions), []int{4, 5}) {
		t.Fatalf("优先级数字和最大应切除 {4,5}，got %v", shedIDs(r.Actions))
	}
}

// 切除并列第三层：编号字典序最小。
func TestShedTieLex(t *testing.T) {
	cfg := demand.Config{ContractKW: 120, WindowSec: 120, SlipSec: 60, MaxPowerKW: 1000}
	c, _ := demand.New(cfg)
	_ = c.AddLoad(0, mkLoad(1, 10, 1, 0, 0))
	_ = c.AddLoad(0, mkLoad(2, 30, 2, 0, 0))
	_ = c.AddLoad(0, mkLoad(3, 30, 2, 0, 0))
	_ = c.AddLoad(0, mkLoad(4, 30, 2, 0, 0))
	r, _ := c.Report(60, 150*60)
	if !sameIDs(shedIDs(r.Actions), []int{2, 3}) {
		t.Fatalf("字典序最小应切除 {2,3}，got %v", shedIDs(r.Actions))
	}
}

// 恢复遇首个失败即止，不得跳过它恢复后面的负荷。
func TestRestoreStopsAtFirstFailure(t *testing.T) {
	cfg := demand.Config{ContractKW: 100, WindowSec: 60, SlipSec: 10, MaxPowerKW: 1000}
	c, _ := demand.New(cfg)
	_ = c.AddLoad(0, mkLoad(1, 1, 1, 0, 0))
	_ = c.AddLoad(0, mkLoad(2, 40, 2, 0, 0))
	_ = c.AddLoad(0, mkLoad(3, 10, 3, 0, 0))
	_, _ = c.Report(10, 140*10) // 切除 2、3
	r, _ := c.Report(40, 88*30) // 恢复负荷2=>128越限；负荷3单独可行但不得跳过
	if restoredAt(r.Actions, 2) || restoredAt(r.Actions, 3) {
		t.Fatalf("首个候选失败后本次不应恢复任何负荷，got %+v", r.Actions)
	}
}

// 关键负荷与锁定负荷不被切除。
func TestCriticalAndLockedNeverShed(t *testing.T) {
	c, _ := demand.New(baseCfg())
	_ = c.AddLoad(0, mkLoad(1, 50, 1, 0, 0))
	_ = c.AddLoad(0, mkLoad(2, 50, 2, 0, 0))
	_ = c.LockLoad(1, 2)
	r, _ := c.Report(10, 140*10)
	if len(r.Actions) != 0 || !r.StillExceed {
		t.Fatalf("关键与锁定负荷均不可切，got %+v", r.Actions)
	}
	if err := c.UnlockLoad(11, 2); err != nil {
		t.Fatal(err)
	}
	r, _ = c.Report(20, 140*10)
	if !hasAction(r.Actions, demand.ActionShed, 2) {
		t.Fatalf("解锁后应可切除负荷2，got %+v", r.Actions)
	}
}

// 锁定已断开负荷不会使其恢复。
func TestLockDisconnectedStaysOff(t *testing.T) {
	cfg := demand.Config{ContractKW: 100, WindowSec: 60, SlipSec: 10, MaxPowerKW: 1000}
	c, _ := demand.New(cfg)
	_ = c.AddLoad(0, mkLoad(1, 1, 1, 0, 0))
	_ = c.AddLoad(0, mkLoad(2, 40, 2, 0, 0))
	_, _ = c.Report(10, 140*10)
	if err := c.LockLoad(20, 2); err != nil {
		t.Fatal(err)
	}
	r, _ := c.Report(40, 60*30)
	if restoredAt(r.Actions, 2) {
		t.Fatalf("锁定的断开负荷不应被恢复，got %+v", r.Actions)
	}
}

// 被拒绝上报不留痕。
func TestRejectedReportLeavesNoTrace(t *testing.T) {
	c, _ := demand.New(baseCfg())
	_ = c.AddLoad(0, mkLoad(1, 10, 1, 0, 0))

	_, err := c.Report(10, -1)
	if kindOf(t, err) != demand.ErrInvalidData {
		t.Fatal("应为数据非法")
	}
	if _, err := c.Report(10, 100); err != nil {
		t.Fatal(err)
	}
	_, err = c.Report(10, 100)
	if kindOf(t, err) != demand.ErrTimeRewind {
		t.Fatal("应为时刻回退")
	}
	_, err = c.Report(20, 1000*10+1)
	if kindOf(t, err) != demand.ErrInvalidData {
		t.Fatal("应因超物理上限而数据非法")
	}
	r, err := c.Report(30, 100*20)
	if err != nil {
		t.Fatalf("拒绝不留痕后应能正常上报，got %v", err)
	}
	if r.At != 30 {
		t.Fatalf("当前时刻应为30，got %d", r.At)
	}
}

// 错误次序：上报 参数 > 数据 > 回退；运维 参数 > 不存在 > 状态。
func TestErrorOrdering(t *testing.T) {
	c, _ := demand.New(baseCfg())
	_, _ = c.Report(10, 100)
	_, err := c.Report(-1, -1)
	if kindOf(t, err) != demand.ErrInvalidParameter {
		t.Fatal("参数非法应最优先")
	}
	_, err = c.Report(5, -1)
	if kindOf(t, err) != demand.ErrInvalidData {
		t.Fatal("数据非法应优先于时刻回退")
	}
	if kindOf(t, c.LockLoad(-1, 999)) != demand.ErrInvalidParameter {
		t.Fatal("运维参数非法应最优先")
	}
	if kindOf(t, c.LockLoad(11, 999)) != demand.ErrLoadNotFound {
		t.Fatal("负荷不存在应优先于状态不允许")
	}
	_ = c.AddLoad(11, mkLoad(7, 3, 2, 0, 0))
	_ = c.LockLoad(12, 7)
	if kindOf(t, c.LockLoad(13, 7)) != demand.ErrStateNotAllowed {
		t.Fatal("重复锁定应为状态不允许")
	}
	if kindOf(t, c.RemoveLoad(14, 7)) != demand.ErrStateNotAllowed {
		t.Fatal("删除接入态负荷应为状态不允许")
	}
}

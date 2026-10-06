package toollife

import (
	"testing"
)

// testLogger 打印输入、输出与判定依据；t.Log 输出在 -v 或用例失败时可见。
type testLogger struct{ t *testing.T }

func (l testLogger) Logf(format string, args ...any) {
	l.t.Logf("[判定依据] "+format, args...)
}

func newTestService(t *testing.T) *Service {
	t.Helper()
	s := New()
	s.SetLogger(testLogger{t})
	return s
}

func mustAdd(t *testing.T, s *Service, gid string, cfg GroupConfig) {
	t.Helper()
	if err := s.AddGroup(gid, cfg); err != nil {
		t.Fatalf("AddGroup(%q) 意外失败: %v", gid, err)
	}
}

func mustCode(t *testing.T, err error, want ErrorCode, ctx string) {
	t.Helper()
	if got := CodeOf(err); got != want {
		t.Fatalf("%s: 错误码 = %v, 期望 %v; err=%v", ctx, got, want, err)
	}
}

func strictCfg(limit int, ids ...string) GroupConfig {
	return GroupConfig{Basis: BySeconds, LifeLimit: limit, WarnPermille: 800, Mode: Strict, ToolIDs: ids}
}

// TestReservedPlusUsedExactlyLimit 预占加已用恰等于上限：仍可分配（边界），再申请则暂无余量。
func TestReservedPlusUsedExactlyLimit(t *testing.T) {
	s := newTestService(t)
	mustAdd(t, s, "G", strictCfg(10, "T1"))

	r1, err := s.Apply("G", "R1", 6)
	if err != nil || r1.ToolID != "T1" {
		t.Fatalf("R1: %+v %v", r1, err)
	}
	t.Logf("输入 Apply R1 预计6；输出 T1 预占6；依据: used0+reserved0+6<=10")

	r2, err := s.Apply("G", "R2", 4)
	if err != nil || r2.ToolID != "T1" {
		t.Fatalf("R2（预占加已用恰等于上限应可分配）: %+v %v", r2, err)
	}
	t.Logf("输入 Apply R2 预计4；输出 T1；依据: used0+reserved6+4=10<=10 边界可承载")

	_, err = s.Apply("G", "R3", 1)
	mustCode(t, err, ErrNoMargin, "R3")
	t.Logf("输入 Apply R3 预计1；输出 ErrNoMargin；依据: T1 可用且 used0+reserved10 占满，属预占占满而非耗尽")

	v, _ := s.Query("G")
	if v.Tools[0].Reserved != 10 || v.Tools[0].Remaining != 10 || v.CurrentPick != "" {
		t.Fatalf("查询不符预期: %+v", v.Tools[0])
	}
}

// TestStrictVsLenient 严格与宽松差异。
func TestStrictVsLenient(t *testing.T) {
	s1 := newTestService(t)
	mustAdd(t, s1, "S", GroupConfig{LifeLimit: 10, WarnPermille: 1000, Mode: Strict, ToolIDs: []string{"A"}})
	if _, err := s1.Apply("S", "R1", 10); err != nil {
		t.Fatal(err)
	}
	_, err := s1.Apply("S", "R2", 1)
	mustCode(t, err, ErrNoMargin, "严格组预占占满后再申请")
	t.Logf("严格组: used+reserved=10, est=1 -> 11>10 拒绝；刀仍可用 -> 暂无余量")

	s2 := newTestService(t)
	mustAdd(t, s2, "L", GroupConfig{LifeLimit: 10, WarnPermille: 1000, Mode: Lenient, ToolIDs: []string{"A"}})
	// 先记账到 9（未占满），宽松模式允许申请 est=5 这一刀（预计后 14>10）。
	if _, err := s2.Apply("L", "R0", 9); err != nil {
		t.Fatal(err)
	}
	if _, err := s2.Settle("R0", 9); err != nil {
		t.Fatal(err)
	}
	r, err := s2.Apply("L", "R1", 5)
	if err != nil || r.ToolID != "A" {
		t.Fatalf("宽松模式只要申请时未占满就放行: %+v %v", r, err)
	}
	t.Logf("宽松组: 申请时 used9+reserved0<10 -> 放行，即便 9+5>10；记账允许超出")
	res, err := s2.Settle("R1", 5)
	if err != nil || !res.Exhausted || res.Actual != 5 {
		t.Fatalf("used=14 应耗尽: %+v %v", res, err)
	}
	_, err = s2.Apply("L", "R2", 1)
	mustCode(t, err, ErrNoTool, "已耗尽刀不再被选")
}

// TestLenientOveruse 宽松模式下实际消耗超出上限照常记账，剩余钳为非负。
func TestLenientOveruse(t *testing.T) {
	s := newTestService(t)
	mustAdd(t, s, "G", GroupConfig{LifeLimit: 10, WarnPermille: 500, Mode: Lenient, ToolIDs: []string{"T1"}})
	if _, err := s.Apply("G", "R1", 8); err != nil {
		t.Fatal(err)
	}
	res, err := s.Settle("R1", 15)
	if err != nil {
		t.Fatalf("宽松模式超上限记账应成功: %v", err)
	}
	if !res.Exhausted || res.Actual != 15 {
		t.Fatalf("应报耗尽且实际15: %+v", res)
	}
	v, _ := s.Query("G")
	if v.Tools[0].Used != 15 || v.Tools[0].Remaining != 0 || v.Tools[0].Status != StatusExhausted {
		t.Fatalf("已用可为15，剩余钳为0，状态耗尽: %+v", v.Tools[0])
	}
	t.Logf("输入 Settle 实际15(预计8)；输出 used=15 remaining=0 exhausted；依据: 宽松超上限照常记账")
}

// TestSettleActualGreaterThanEstimated 实际大于预计：预占释放、已用补记。
func TestSettleActualGreaterThanEstimated(t *testing.T) {
	s := newTestService(t)
	mustAdd(t, s, "G", strictCfg(100, "T1"))
	if _, err := s.Apply("G", "R1", 40); err != nil {
		t.Fatal(err)
	}
	res, err := s.Settle("R1", 60)
	if err != nil || res.ToolID != "T1" || res.Exhausted {
		t.Fatalf("实际60>预计40, used=60 未达100不应耗尽: %+v %v", res, err)
	}
	v, _ := s.Query("G")
	if v.Tools[0].Used != 60 || v.Tools[0].Reserved != 0 || v.Tools[0].Remaining != 40 {
		t.Fatalf("预占应清零，已用60，剩余40: %+v", v.Tools[0])
	}
}

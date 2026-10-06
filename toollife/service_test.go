package toollife_test

import (
	"strings"
	"testing"

	"ontology/toollife"
)

func asErr(err error) *toollife.Error {
	for err != nil {
		if x, ok := err.(*toollife.Error); ok {
			return x
		}
		u, ok := err.(interface{ Unwrap() error })
		if !ok {
			break
		}
		err = u.Unwrap()
	}
	return nil
}

func mustCode(tb testing.TB, err error, want toollife.Code) {
	tb.Helper()
	if want == 0 {
		if err != nil {
			tb.Fatalf("unexpected error: %v", err)
		}
		return
	}
	if err == nil {
		tb.Fatalf("want error code %d, got nil", want)
	}
	e := asErr(err)
	if e == nil {
		tb.Fatalf("error is not *toollife.Error: %T %v", err, err)
	}
	if e.Code != want {
		tb.Fatalf("want code %d, got %d (%v)", want, e.Code, err)
	}
}

func newSvc(tb testing.TB, gid string, cfg toollife.GroupConfig, tools ...string) *toollife.Service {
	tb.Helper()
	s := toollife.NewService()
	mustCode(tb, s.Magazine().AddGroup(gid, cfg), 0)
	for _, id := range tools {
		mustCode(tb, s.Magazine().AddTool(gid, id), 0)
	}
	return s
}

func strictCfg(limit uint64) toollife.GroupConfig {
	return toollife.GroupConfig{Basis: toollife.BasisSeconds, LifeLimit: limit, WarnPermille: 800, Mode: toollife.ModeStrict}
}

func okApply(tb testing.TB, s *toollife.Service, reqID, groupID string, est uint64) toollife.ApplyResult {
	tb.Helper()
	r, err := s.Apply(reqID, groupID, est)
	mustCode(tb, err, 0)
	return r
}

// 已用+已预占+本次预计恰好等于上限时严格模式可承载；再多 1 即拒绝。
func TestExactLimitBoundary(t *testing.T) {
	s := newSvc(t, "g", strictCfg(100), "t0")

	r, err := s.Apply("a1", "g", 60)
	mustCode(t, err, 0)
	if r.ToolID != "t0" {
		t.Fatalf("want t0, got %s", r.ToolID)
	}
	r2, err := s.Apply("a2", "g", 40)
	mustCode(t, err, 0)
	if r2.ToolID != "t0" {
		t.Fatalf("want t0 (exact fit), got %s", r2.ToolID)
	}
	// 60+40 恰好占满，再多 1 即暂无余量（释放后可恢复，区别于耗尽）。
	_, err = s.Apply("a3", "g", 1)
	mustCode(t, err, toollife.ErrNoCapacity)

	mustCode(t, s.Abort("a1"), 0)
	var r4 toollife.ApplyResult
	r4, err = s.Apply("a4", "g", 60)
	mustCode(t, err, 0)
	if r4.ToolID != "t0" {
		t.Fatalf("after release want t0, got %s", r4.ToolID)
	}
}

// 严格与宽松差异 + 宽松超限照常记账且剩余截断为 0。
func TestStrictVsLenient(t *testing.T) {
	for _, tc := range []struct {
		name string
		mode toollife.SelectMode
		want toollife.Code
	}{
		{"strict", toollife.ModeStrict, toollife.ErrNoTool},
		{"lenient", toollife.ModeLenient, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := strictCfg(100)
			cfg.Mode = tc.mode
			s := newSvc(t, "g", cfg, "t0")
			okApply(t, s, "a1", "g", 90)
			mustCode(t, s.Settle("a1", 90), 0)
			_, err := s.Apply("a2", "g", 50)
			mustCode(t, err, tc.want)
			if tc.want == 0 {
				mustCode(t, s.Settle("a2", 50), 0)
				snap, _ := s.Query("g")
				if snap.Order[0].Used != 140 || snap.Order[0].Status != toollife.StatusExhausted {
					t.Fatalf("lenient overshoot: used=%d status=%s", snap.Order[0].Used, snap.Order[0].Status)
				}
				if snap.Order[0].Remaining != 0 {
					t.Fatalf("remaining must clamp at 0, got %d", snap.Order[0].Remaining)
				}
			}
		})
	}
}

// 实际消耗大于预计：差额释放，已用以实际为准；重复记账不重复累计。
func TestSettleActualGreater(t *testing.T) {
	s := newSvc(t, "g", strictCfg(100), "t0", "t1")
	_, err := s.Apply("a1", "g", 10)
	mustCode(t, err, 0)
	r2, err := s.Apply("a2", "g", 95)
	mustCode(t, err, 0)
	if r2.ToolID != "t1" {
		t.Fatalf("want t1, got %s", r2.ToolID)
	}
	mustCode(t, s.Settle("a1", 100), 0)
	snap, _ := s.Query("g")
	if snap.Order[0].Status != toollife.StatusExhausted || snap.Order[0].Used != 100 || snap.Order[0].Reserved != 0 {
		t.Fatalf("t0: %+v", snap.Order[0])
	}
	mustCode(t, s.Settle("a1", 100), 0)
	snap, _ = s.Query("g")
	if snap.Order[0].Used != 100 {
		t.Fatalf("idempotent settle broken, used=%d", snap.Order[0].Used)
	}
}

// 破损刀上未结算预占：仍可记账计入已用且保持破损；换新先拒后允。
func TestBrokenWithOpenReservation(t *testing.T) {
	s := newSvc(t, "g", strictCfg(100), "t0")
	okApply(t, s, "a1", "g", 20)
	mustCode(t, s.ReportBroken("g", "t0"), 0)

	_, err := s.Apply("a2", "g", 1)
	mustCode(t, err, toollife.ErrNoTool)

	mustCode(t, s.Replace("g", "t0", "t0new"), toollife.ErrState)

	mustCode(t, s.Settle("a1", 20), 0)
	snap, _ := s.Query("g")
	if snap.Order[0].Status != toollife.StatusBroken || snap.Order[0].Used != 20 || snap.Order[0].Reserved != 0 {
		t.Fatalf("broken settle: %+v", snap.Order[0])
	}
	mustCode(t, s.Replace("g", "t0", "t0new"), 0)
	snap, _ = s.Query("g")
	if snap.Order[0].ID != "t0new" || snap.Order[0].Status != toollife.StatusAvailable || snap.Order[0].Used != 0 {
		t.Fatalf("replace: %+v", snap.Order[0])
	}
	if snap.Selected != "t0new" {
		t.Fatalf("want selected t0new, got %s", snap.Selected)
	}
}

// 换新只能针对破损刀。
func TestReplaceRequiresBroken(t *testing.T) {
	s := newSvc(t, "g", strictCfg(100), "t0")
	mustCode(t, s.Replace("g", "t0", "tn"), toollife.ErrState)
	mustCode(t, s.Lock("g", "t0"), 0)
	mustCode(t, s.Replace("g", "t0", "tn"), toollife.ErrState)
}

// 预警恰好跨线（800‰）只发一次；换新后重新具备资格。
func TestWarningCrossOnceAndReset(t *testing.T) {
	s := newSvc(t, "g", strictCfg(10), "t0") // 800‰ => 已用 8 触发
	okApply(t, s, "a1", "g", 4)
	mustCode(t, s.Settle("a1", 4), 0)
	if len(s.Warnings()) != 0 {
		t.Fatalf("no warning expected at 40%%")
	}
	okApply(t, s, "a2", "g", 4)
	mustCode(t, s.Settle("a2", 4), 0)
	ws := s.Warnings()
	if len(ws) != 1 || ws[0].ToolID != "t0" || ws[0].Permille != 800 {
		t.Fatalf("want one warning at 800‰, got %+v", ws)
	}
	okApply(t, s, "a3", "g", 2)
	mustCode(t, s.Settle("a3", 2), 0)
	if len(s.Warnings()) != 1 {
		t.Fatalf("warning must fire once, got %d", len(s.Warnings()))
	}

	s2 := newSvc(t, "g2", strictCfg(100), "b0")
	okApply(t, s2, "b1", "g2", 80)
	mustCode(t, s2.Settle("b1", 80), 0)
	mustCode(t, s2.ReportBroken("g2", "b0"), 0)
	mustCode(t, s2.Replace("g2", "b0", "b1new"), 0)
	okApply(t, s2, "b2", "g2", 80)
	mustCode(t, s2.Settle("b2", 80), 0)
	if len(s2.Warnings()) != 2 {
		t.Fatalf("want warning rearmed after replace, got %d", len(s2.Warnings()))
	}
}

// 暂无余量与耗尽的区分。
func TestNoCapacityVsExhausted(t *testing.T) {
	s := newSvc(t, "g", strictCfg(10), "t0")
	okApply(t, s, "a1", "g", 10)
	_, err := s.Apply("a2", "g", 1)
	mustCode(t, err, toollife.ErrNoCapacity)
	if !strings.Contains(err.Error(), "free capacity") {
		t.Fatalf("err should explain capacity: %v", err)
	}
	mustCode(t, s.Settle("a1", 10), 0)
	_, err = s.Apply("a3", "g", 1)
	mustCode(t, err, toollife.ErrNoTool)
}

// 重复申请幂等不重复预占；同编号换内容报冲突；刀组不存在优先于冲突。
func TestApplyIdempotentAndConflict(t *testing.T) {
	s := newSvc(t, "g", strictCfg(100), "t0")
	r1, err := s.Apply("a1", "g", 30)
	mustCode(t, err, 0)
	r2, err := s.Apply("a1", "g", 30)
	mustCode(t, err, 0)
	if r1.ToolID != r2.ToolID || r2.Reserved != 30 {
		t.Fatalf("idempotent apply mismatch: %+v %+v", r1, r2)
	}
	snap, _ := s.Query("g")
	if snap.Order[0].Reserved != 30 {
		t.Fatalf("reservation double-counted: %d", snap.Order[0].Reserved)
	}
	_, err = s.Apply("a1", "g", 31)
	mustCode(t, err, toollife.ErrConflict)
	_, err = s.Apply("a1", "nope", 30)
	mustCode(t, err, toollife.ErrGroupNotFound)
	mustCode(t, s.Magazine().AddGroup("g2", strictCfg(100)), 0)
	_, err = s.Apply("a1", "g2", 30)
	mustCode(t, err, toollife.ErrConflict)
}

// 中止释放预占；中止与记账互斥；错误次序：参数 > 刀组 > 刀具 > 状态 > 无刀。
func TestAbortAndErrorOrder(t *testing.T) {
	s := newSvc(t, "g", strictCfg(100), "t0")
	okApply(t, s, "a1", "g", 40)
	mustCode(t, s.Abort("a1"), 0)
	mustCode(t, s.Abort("a1"), 0)
	snap, _ := s.Query("g")
	if snap.Order[0].Reserved != 0 || snap.Order[0].Used != 0 {
		t.Fatalf("abort must release reservation: %+v", snap.Order[0])
	}
	mustCode(t, s.Settle("a1", 1), toollife.ErrState)
	mustCode(t, s.Settle("zzz", 1), toollife.ErrRequestNotFound)
	mustCode(t, s.Abort("zzz"), toollife.ErrRequestNotFound)
	_, err := s.Apply("", "g", 1)
	mustCode(t, err, toollife.ErrInvalid)
	_, err = s.Apply("x", "g", 0)
	mustCode(t, err, toollife.ErrInvalid)
	mustCode(t, s.Settle("", 1), toollife.ErrInvalid)
	_, err = s.Apply("x", "nope", 1)
	mustCode(t, err, toollife.ErrGroupNotFound)
	mustCode(t, s.Lock("nope", "t0"), toollife.ErrGroupNotFound)
	mustCode(t, s.Lock("g", "nope"), toollife.ErrToolNotFound)
}

// 锁定/解锁语义：锁定不被选中、预占不受影响；耗尽刀不能解锁为可用。
func TestLockUnlock(t *testing.T) {
	s := newSvc(t, "g", strictCfg(100), "t0", "t1")
	okApply(t, s, "a1", "g", 10) // t0 持有预占
	mustCode(t, s.Lock("g", "t0"), 0)
	r, err := s.Apply("a2", "g", 1)
	mustCode(t, err, 0)
	if r.ToolID != "t1" {
		t.Fatalf("locked t0 must be skipped, got %s", r.ToolID)
	}
	snap, _ := s.Query("g")
	if snap.Order[0].Reserved != 10 {
		t.Fatalf("locking must not affect existing reservation: %d", snap.Order[0].Reserved)
	}
	// 解锁后重复锁定/解锁幂等。
	mustCode(t, s.Unlock("g", "t0"), 0)
	mustCode(t, s.Unlock("g", "t0"), 0)

	// 独立场景：刀在可用时被预占，锁定后记账至耗尽，不能解锁为可用。
	s2 := newSvc(t, "g2", strictCfg(100), "b0")
	okApply(t, s2, "b1", "g2", 100)
	mustCode(t, s2.Lock("g2", "b0"), 0)
	mustCode(t, s2.Settle("b1", 100), 0)
	mustCode(t, s2.Unlock("g2", "b0"), toollife.ErrState)

	// 独立场景：破损刀不能解锁。
	s3 := newSvc(t, "g3", strictCfg(100), "c0")
	mustCode(t, s3.ReportBroken("g3", "c0"), 0)
	mustCode(t, s3.Unlock("g3", "c0"), toollife.ErrState)
}

// 查询返回当前会被选中的刀（预计消耗 1），以及剩余寿命不为负。
func TestQuerySelection(t *testing.T) {
	cfg := strictCfg(10)
	s := newSvc(t, "g", cfg)
	snap, err := s.Query("g")
	mustCode(t, err, 0)
	if len(snap.Order) != 0 || snap.Selected != "" {
		t.Fatalf("empty group: %+v", snap)
	}
	mustCode(t, s.Magazine().AddTool("g", "q0"), 0)
	okApply(t, s, "q1", "g", 10)
	snap, _ = s.Query("g")
	if snap.Selected != "" || snap.Order[0].Remaining != 10 {
		t.Fatalf("reserved-full tool has remaining=used based: %+v", snap)
	}
	mustCode(t, s.Settle("q1", 10), 0)
	snap, _ = s.Query("g")
	if snap.Order[0].Status != toollife.StatusExhausted || snap.Order[0].Remaining != 0 || snap.Selected != "" {
		t.Fatalf("exhausted query: %+v", snap)
	}
}

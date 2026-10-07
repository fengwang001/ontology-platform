package retention_test

import (
	"bytes"
	"testing"

	"ontology/retention"
)

// 宽限期：截止时刻恰好到达与刚过去两种临界情形。
func TestGraceExactBoundaryAndJustPast(t *testing.T) {
	svc, clk, _ := newSvc(100)
	must(t, svc.CreateObject("o", map[string]string{"k": "v"}))
	must(t, svc.SoftDelete("o", 200))

	// 截止之前：撤销成功，抹去本次删除对查询的影响。
	clk.Advance(199)
	v, exists := svc.Query("o", retention.RoleUser)
	if !exists || v.Visible {
		t.Fatalf("user must not see GRACE object, got %+v exists=%v", v, exists)
	}
	must(t, svc.Undelete("o"))
	if st, _, _, _ := svc.State("o"); st != retention.StateAlive {
		t.Fatalf("undelete should restore ALIVE, got %s", st)
	}
	v, _ = svc.Query("o", retention.RoleUser)
	if !v.Visible || v.Attributes["k"] != "v" {
		t.Fatalf("user should see restored object with attrs, got %+v", v)
	}

	// “截止时刻恰好到达”：now == deadline，下一次操作触发自动归档，
	// 该转换不消耗调用方请求。
	must(t, svc.SoftDelete("o", 250))
	clk.Advance(250)
	va, exists := svc.Query("o", retention.RoleAdmin)
	if !exists || !va.Visible || va.State != retention.StateArchived {
		t.Fatalf("at exact deadline object should be ARCHIVED, got %+v", va)
	}
	if err := svc.Undelete("o"); retention.Code(err) != retention.ErrIllegalTransition {
		t.Fatalf("undelete archived must be ILLEGAL_TRANSITION, got %v", err)
	}

	// “刚过去”：deadline 之后撤销失败，对象已被惰性归档。
	svc2, clk2, _ := newSvc(0)
	must(t, svc2.CreateObject("p", nil))
	must(t, svc2.SoftDelete("p", 10))
	clk2.Advance(11)
	if err := svc2.Undelete("p"); retention.Code(err) != retention.ErrIllegalTransition {
		t.Fatalf("undelete just past deadline must fail, got %v", err)
	}
	if st, _, _, _ := svc2.State("p"); st != retention.StateArchived {
		t.Fatalf("object should be archived, got %s", st)
	}
}

// 保留期冻结：期满前后对撤销与归档的拒绝/放行。
func TestFreezeBoundaries(t *testing.T) {
	svc, clk, _ := newSvc(100)
	must(t, svc.CreateObject("o", map[string]string{"secret": "42"}))
	must(t, svc.Freeze("o", 50)) // deadline = 150，进入时一次性确定

	clk.Advance(149)
	if err := svc.Undelete("o"); retention.Code(err) != retention.ErrFrozenNotExpired {
		t.Fatalf("undelete before freeze expiry: %v", err)
	}
	if err := svc.Archive("o"); retention.Code(err) != retention.ErrFrozenNotExpired {
		t.Fatalf("archive before freeze expiry: %v", err)
	}
	if st, _, d, _ := svc.State("o"); st != retention.StateFrozen || d != 150 {
		t.Fatalf("state/deadline must remain unchanged, st=%s d=%d", st, d)
	}

	// 管理员只见状态与冻结截止时刻，业务属性被遮蔽。
	va, _ := svc.Query("o", retention.RoleAdmin)
	if !va.Visible || va.State != retention.StateFrozen || va.FreezeDeadline != 150 {
		t.Fatalf("admin frozen view wrong: %+v", va)
	}
	if va.Attributes != nil {
		t.Fatalf("frozen attrs must be redacted, got %+v", va.Attributes)
	}

	// 恰好期满放行。
	clk.Advance(150)
	must(t, svc.Archive("o"))
	if st, _, _, _ := svc.State("o"); st != retention.StateArchived {
		t.Fatalf("expected archived, got %s", st)
	}

	// 已归档不可转回任何状态。
	badOps := []func() error{
		func() error { return svc.SoftDelete("o", 999) },
		func() error { return svc.Undelete("o") },
		func() error { return svc.Freeze("o", 1) },
		func() error { return svc.Archive("o") },
	}
	for _, op := range badOps {
		if err := op(); retention.Code(err) != retention.ErrIllegalTransition {
			t.Fatalf("transition out of ARCHIVED must be illegal, got %v", err)
		}
	}

	// 时长在进入时确定，期间推进时钟不重算/延长 deadline。
	svc2, clk2, _ := newSvc(0)
	must(t, svc2.CreateObject("q", nil))
	must(t, svc2.Freeze("q", 100))
	clk2.Advance(50)
	if _, _, d, _ := svc2.State("q"); d != 100 {
		t.Fatalf("freeze deadline recomputed/extended: %d", d)
	}
}

// 审计日志：记录输入、输出与据以判定的状态与时刻。
func TestAuditContents(t *testing.T) {
	buf := &bytes.Buffer{}
	clk := retention.NewLogicalClock(7)
	log := retention.NewAuditLog(buf)
	svc := retention.NewService(clk, log)
	must(t, svc.CreateObject("o", map[string]string{"k": "v"}))
	must(t, svc.SoftDelete("o", 20))
	must(t, svc.Undelete("o"))
	entries := log.Entries()
	if len(entries) != 3 {
		t.Fatalf("want 3 entries, got %d", len(entries))
	}
	for i, e := range entries {
		if e.Seq != int64(i+1) || e.At != 7 {
			t.Fatalf("seq/at wrong: %+v", e)
		}
	}
	mid := entries[1]
	if mid.StateBefore != retention.StateAlive || mid.StateAfter != retention.StateGrace ||
		!mid.Success || mid.Input["grace_deadline"] != int64(20) {
		t.Fatalf("soft_delete audit wrong: %+v", mid)
	}
	if n := bytes.Count(buf.Bytes(), []byte("\n")); n != 3 {
		t.Fatalf("want 3 JSON lines in sink, got %d", n)
	}
}

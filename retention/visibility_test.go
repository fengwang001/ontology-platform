package retention_test

import (
	"testing"

	"ontology/retention"
)

// 两类查询身份对同一对象及其出边链接的可见性组合。
func TestVisibilityMatrixWithEdges(t *testing.T) {
	svc, clk, _ := newSvc(0)
	must(t, svc.CreateObject("src", map[string]string{"a": "1"}))
	must(t, svc.CreateObject("dst", nil))
	must(t, svc.AddEdge("src", "dst"))

	check := func(wantUser, wantEdgeUser, wantEdgeAdmin bool, st retention.State) {
		t.Helper()
		vu, existsU := svc.Query("src", retention.RoleUser)
		va, existsA := svc.Query("src", retention.RoleAdmin)
		if !existsU || !existsA {
			t.Fatalf("object must exist")
		}
		if vu.Visible != wantUser {
			t.Fatalf("%s: user visible=%v want %v", st, vu.Visible, wantUser)
		}
		if !va.Visible || va.State != st {
			t.Fatalf("%s: admin view %+v", st, va)
		}
		if got := svc.EdgeVisible("src", "dst", retention.RoleUser); got != wantEdgeUser {
			t.Fatalf("%s: edge user=%v want %v", st, got, wantEdgeUser)
		}
		if got := svc.EdgeVisible("src", "dst", retention.RoleAdmin); got != wantEdgeAdmin {
			t.Fatalf("%s: edge admin=%v want %v", st, got, wantEdgeAdmin)
		}
		if edges := svc.VisibleEdges("src", retention.RoleUser); len(edges) != b2i(wantEdgeUser) {
			t.Fatalf("%s: VisibleEdges user=%v", st, edges)
		}
		if edges := svc.VisibleEdges("src", retention.RoleAdmin); len(edges) != b2i(wantEdgeAdmin) {
			t.Fatalf("%s: VisibleEdges admin=%v", st, edges)
		}
	}

	check(true, true, true, retention.StateAlive)
	must(t, svc.SoftDelete("src", 100))
	check(false, false, true, retention.StateGrace)
	must(t, svc.Undelete("src"))
	must(t, svc.Freeze("src", 100))
	// 冻结：用户不可见且出边不可见；管理员见状态，但业务关系（出边）随业务属性遮蔽。
	check(false, false, false, retention.StateFrozen)
	clk.Advance(100)
	must(t, svc.Archive("src"))
	check(false, false, true, retention.StateArchived)

	// 链接自身无删除标记：源经 GRACE 撤销恢复后，出边立刻重新可见。
	svc2, clk2, _ := newSvc(0)
	must(t, svc2.CreateObject("s", nil))
	must(t, svc2.CreateObject("d", nil))
	must(t, svc2.AddEdge("s", "d"))
	must(t, svc2.SoftDelete("s", 10))
	if !svc2.EdgeVisible("s", "d", retention.RoleAdmin) {
		t.Fatalf("admin should see edge from GRACE source")
	}
	if svc2.EdgeVisible("s", "d", retention.RoleUser) {
		t.Fatalf("user must not see edge from GRACE source")
	}
	clk2.Advance(5)
	must(t, svc2.Undelete("s"))
	if !svc2.EdgeVisible("s", "d", retention.RoleUser) {
		t.Fatalf("edge must reappear after source restored")
	}
}

// 错误四类互斥、次序固定；被拒绝的转换不改状态、截止时刻、时钟。
func TestErrorPrecedence(t *testing.T) {
	svc, clk, _ := newSvc(100)

	// 1) 对象不存在优先于其余三类。
	notFoundOps := []func() error{
		func() error { return svc.SoftDelete("x", 200) },
		func() error { return svc.Undelete("x") },
		func() error { return svc.Freeze("x", 5) },
		func() error { return svc.Archive("x") },
	}
	for _, op := range notFoundOps {
		if code := retention.Code(op()); code != retention.ErrNotFound {
			t.Fatalf("want NOT_FOUND, got %v", code)
		}
	}

	must(t, svc.CreateObject("o", nil))
	must(t, svc.Freeze("o", 100)) // deadline 200
	// 冻结未满：第四类（FROZEN->ARCHIVED 方向本身合法）。
	if code := retention.Code(svc.Archive("o")); code != retention.ErrFrozenNotExpired {
		t.Fatalf("want FROZEN_NOT_EXPIRED, got %v", code)
	}
	if code := retention.Code(svc.Undelete("o")); code != retention.ErrFrozenNotExpired {
		t.Fatalf("want FROZEN_NOT_EXPIRED for undelete, got %v", code)
	}

	// 存活起点 + 非法参数：方向合法 -> 第三类。
	must(t, svc.CreateObject("q", nil))
	if code := retention.Code(svc.SoftDelete("q", 50)); code != retention.ErrInvalidParameter {
		t.Fatalf("want INVALID_PARAMETER, got %v", code)
	}
	if code := retention.Code(svc.Freeze("q", 0)); code != retention.ErrInvalidParameter {
		t.Fatalf("want INVALID_PARAMETER for duration, got %v", code)
	}
	// 非存活起点 + 非法参数：方向错误（第二类）先于第三类。
	must(t, svc.SoftDelete("q", 200))
	if code := retention.Code(svc.SoftDelete("q", 50)); code != retention.ErrIllegalTransition {
		t.Fatalf("want ILLEGAL_TRANSITION first, got %v", code)
	}

	// 拒绝不改变状态、截止时刻与时钟。
	if st, _, d, _ := svc.State("o"); st != retention.StateFrozen || d != 200 {
		t.Fatalf("rejected ops must not change state/deadline: %s %d", st, d)
	}
	if clk.Now() != 100 {
		t.Fatalf("rejected ops must not advance clock: %d", clk.Now())
	}
}

// O(1) 历史记录复杂度的可复现核对。
func TestVisibilityIsO1InHistoryLength(t *testing.T) {
	svc, clk, _ := newSvc(0)
	must(t, svc.CreateObject("o", nil))
	now := int64(0)
	cycles := func(n int) {
		for i := 0; i < n; i++ {
			now += 10
			clk.Advance(now)
			must(t, svc.SoftDelete("o", now+5))
			now += 2
			clk.Advance(now)
			must(t, svc.Undelete("o"))
		}
	}
	cycles(200)
	before := svc.ProbeCount("o")
	svc.Query("o", retention.RoleUser)
	svc.Query("o", retention.RoleAdmin)
	svc.VisibleEdges("o", retention.RoleUser)
	if got := svc.ProbeCount("o") - before; got != 3 {
		t.Fatalf("3 decisions must touch exactly 3 records regardless of history, got %d", got)
	}
	cycles(400)
	before = svc.ProbeCount("o")
	svc.Query("o", retention.RoleUser)
	if got := svc.ProbeCount("o") - before; got != 1 {
		t.Fatalf("one query touches one record after 1200 history events, got %d", got)
	}
}

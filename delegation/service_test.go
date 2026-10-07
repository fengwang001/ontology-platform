package delegation

import (
	"errors"
	"testing"
	"time"
)

var t0 = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

func newTestService() (*Service, *ManualClock) {
	clk := NewManualClock(t0)
	return NewService(WithClock(clk)), clk
}

// perm 构造单对象类型的权限集。
func perm(objType string, attrs []string, rows []string) PermissionSet {
	op := ObjectPerm{}
	if attrs != nil {
		op.Attrs = map[string]bool{}
		for _, a := range attrs {
			op.Attrs[a] = true
		}
	}
	if rows != nil {
		op.Rows = map[string]bool{}
		for _, r := range rows {
			op.Rows[r] = true
		}
	}
	return PermissionSet{objType: op}
}

func mustDelegate(t *testing.T, s *Service, req DelegationRequest) DelegationID {
	t.Helper()
	id, err := s.Delegate(req)
	if err != nil {
		t.Fatalf("Delegate(%+v) 意外失败: %v", req, err)
	}
	return id
}

func decide(s *Service, subject, objType string, at time.Time, attrs ...string) Decision {
	return s.Decide(AccessRequest{
		Subject:    subject,
		ObjectType: objType,
		Require:    ObjectPerm{Attrs: attrsTo(attrs)},
		At:         at,
	})
}

func attrsTo(attrs []string) map[string]bool {
	m := map[string]bool{}
	for _, a := range attrs {
		m[a] = true
	}
	return m
}

func hour(n int) time.Time { return t0.Add(time.Duration(n) * time.Hour) }

func TestBasicDelegationAndDecide(t *testing.T) {
	s, _ := newTestService()
	s.GrantBase("alice", perm("doc", []string{"title", "body"}, []string{"r1", "r2"}))

	id := mustDelegate(t, s, DelegationRequest{
		Delegator: "alice", Delegatee: "bob",
		Subset: perm("doc", []string{"title"}, []string{"r1"}),
		Start:  t0, End: hour(1),
	})

	d := decide(s, "bob", "doc", hour(0), "title")
	if !d.Allowed {
		t.Fatal("bob 应被允许访问 title")
	}
	if len(d.Witness) != 1 || d.Witness[0] != id {
		t.Fatalf("见证链应为 [%d], 实际 %v", id, d.Witness)
	}
	if d := decide(s, "bob", "doc", hour(0), "body"); d.Allowed {
		t.Fatal("body 不在委托子集内，应拒绝")
	}
	if d := decide(s, "carol", "doc", hour(0), "title"); d.Allowed {
		t.Fatal("carol 无任何权限，应拒绝")
	}
	// 行级权限
	if d := decide(s, "bob", "doc", hour(0)); !d.Allowed {
		t.Fatal("空需求应放行")
	}
	rowReq := AccessRequest{Subject: "bob", ObjectType: "doc",
		Require: ObjectPerm{Rows: map[string]bool{"r1": true}}, At: hour(0)}
	if !s.Decide(rowReq).Allowed {
		t.Fatal("bob 应被允许访问行 r1")
	}
	rowReq.Require.Rows = map[string]bool{"r2": true}
	if s.Decide(rowReq).Allowed {
		t.Fatal("r2 不在委托子集内，应拒绝")
	}
}

func TestErrorPrecedence(t *testing.T) {
	s, _ := newTestService()
	s.GrantBase("alice", perm("doc", []string{"a"}, nil))
	// alice -> bob，允许再委托
	mustDelegate(t, s, DelegationRequest{
		Delegator: "alice", Delegatee: "bob",
		Subset: perm("doc", []string{"a"}, nil),
		Start:  t0, End: hour(10), AllowRedelegate: true,
	})
	// bob -> carol，允许再委托（为成环检测准备路径）
	mustDelegate(t, s, DelegationRequest{
		Delegator: "bob", Delegatee: "carol",
		Subset: perm("doc", []string{"a"}, nil),
		Start:  t0, End: hour(10), AllowRedelegate: true,
	})

	// 1. 子集超范围（同时也违反再委托标记、也会成环、且已过期）：
	//    必须优先报告 ErrSubsetExceeds。
	_, err := s.Delegate(DelegationRequest{
		Delegator: "carol", Delegatee: "alice",
		Subset: perm("doc", []string{"a", "b"}, nil),
		Start:  t0, End: hour(-1),
	})
	if !errors.Is(err, ErrSubsetExceeds) {
		t.Fatalf("应为 ErrSubsetExceeds, 实际 %v", err)
	}

	// 2. 子集在有效权限内但依赖的上游委托不允许再委托
	//    （alice 的委托未给 bob 再委托权，bob 直接持有的部分为零；
	//    这里构造 carol 持有了允许再委托的权限，但 dan 只有不允许再委托的来源）。
	s.GrantBase("dan", perm("doc", []string{"x"}, nil))
	mustDelegate(t, s, DelegationRequest{
		Delegator: "dan", Delegatee: "erin",
		Subset: perm("doc", []string{"x"}, nil),
		Start:  t0, End: hour(10), // 不允许再委托
	})
	_, err = s.Delegate(DelegationRequest{
		Delegator: "erin", Delegatee: "frank",
		Subset: perm("doc", []string{"x"}, nil),
		Start:  t0, End: hour(10),
	})
	if !errors.Is(err, ErrRedelegateNotAllowed) {
		t.Fatalf("应为 ErrRedelegateNotAllowed, 实际 %v", err)
	}

	// 3. 成环：carol 拥有可再委托权限，委托回 alice 将成环。
	_, err = s.Delegate(DelegationRequest{
		Delegator: "carol", Delegatee: "alice",
		Subset: perm("doc", []string{"a"}, nil),
		Start:  t0, End: hour(10),
	})
	if !errors.Is(err, ErrCycle) {
		t.Fatalf("应为 ErrCycle, 实际 %v", err)
	}
	// 成环拒绝不得影响已有委托：bob 仍然有效。
	if d := decide(s, "bob", "doc", hour(0), "a"); !d.Allowed {
		t.Fatal("成环拒绝后已有委托不应受影响")
	}

	// 4. 过期：子集合法、无再委托问题、无环，但有效期已结束。
	_, err = s.Delegate(DelegationRequest{
		Delegator: "alice", Delegatee: "zoe",
		Subset: perm("doc", []string{"a"}, nil),
		Start:  hour(-2), End: hour(-1),
	})
	if !errors.Is(err, ErrExpired) {
		t.Fatalf("应为 ErrExpired, 实际 %v", err)
	}
	// 非法区间（End 未晚于 Start）同样报 ErrExpired。
	_, err = s.Delegate(DelegationRequest{
		Delegator: "alice", Delegatee: "zoe",
		Subset: perm("doc", []string{"a"}, nil),
		Start:  hour(1), End: hour(1),
	})
	if !errors.Is(err, ErrExpired) {
		t.Fatalf("非法区间应为 ErrExpired, 实际 %v", err)
	}
}

func TestShrinkCascadeWholeInvalidation(t *testing.T) {
	s, clk := newTestService()
	s.GrantBase("alice", perm("doc", []string{"a", "b"}, nil))
	mustDelegate(t, s, DelegationRequest{
		Delegator: "alice", Delegatee: "bob",
		Subset: perm("doc", []string{"a", "b"}, nil),
		Start:  t0, End: hour(10), AllowRedelegate: true,
	})
	mustDelegate(t, s, DelegationRequest{
		Delegator: "bob", Delegatee: "carol",
		Subset: perm("doc", []string{"a", "b"}, nil),
		Start:  t0, End: hour(10),
	})

	clk.Advance(time.Hour)
	// 收缩 alice 的 b：bob 与 carol 的委托声明都包含 b，
	// 必须整体失效，而不是保留 a 的交集。
	s.ShrinkBase("alice", perm("doc", []string{"b"}, nil))

	if d := decide(s, "bob", "doc", hour(1), "a"); d.Allowed {
		t.Fatal("收缩后 bob 的委托应整体失效，不得保留交集")
	}
	if d := decide(s, "carol", "doc", hour(1), "a"); d.Allowed {
		t.Fatal("收缩应级联到 carol")
	}
}

func TestShrinkWithIndependentSource(t *testing.T) {
	s, clk := newTestService()
	// bob 从两条独立路径获得 {a,b}：alice 与独立来源 zoe。
	s.GrantBase("alice", perm("doc", []string{"a", "b"}, nil))
	s.GrantBase("zoe", perm("doc", []string{"a", "b"}, nil))
	for _, src := range []string{"alice", "zoe"} {
		mustDelegate(t, s, DelegationRequest{
			Delegator: src, Delegatee: "bob",
			Subset: perm("doc", []string{"a", "b"}, nil),
			Start:  t0, End: hour(10), AllowRedelegate: true,
		})
	}
	mustDelegate(t, s, DelegationRequest{
		Delegator: "bob", Delegatee: "carol",
		Subset: perm("doc", []string{"a", "b"}, nil),
		Start:  t0, End: hour(10),
	})

	clk.Advance(time.Hour)
	s.ShrinkBase("alice", perm("doc", []string{"a", "b"}, nil))

	// zoe 路径仍能完整支撑 bob -> carol 的全部内容。
	if d := decide(s, "carol", "doc", hour(1), "a", "b"); !d.Allowed {
		t.Fatal("独立来源仍完整支撑时，下游委托不应失效")
	}
}

func TestExpiryCascade(t *testing.T) {
	s, clk := newTestService()
	s.GrantBase("alice", perm("doc", []string{"a"}, nil))
	mustDelegate(t, s, DelegationRequest{
		Delegator: "alice", Delegatee: "bob",
		Subset: perm("doc", []string{"a"}, nil),
		Start:  t0, End: hour(2), AllowRedelegate: true,
	})
	mustDelegate(t, s, DelegationRequest{
		Delegator: "bob", Delegatee: "carol",
		Subset: perm("doc", []string{"a"}, nil),
		Start:  t0, End: hour(10),
	})

	clk.Advance(3 * time.Hour) // alice->bob 已过期
	if d := decide(s, "bob", "doc", hour(3), "a"); d.Allowed {
		t.Fatal("上游过期后 bob 应失效")
	}
	if d := decide(s, "carol", "doc", hour(3), "a"); d.Allowed {
		t.Fatal("上游过期应级联使 carol 失效")
	}
	// 但在过期之前的时刻，历史判定仍为允许。
	if d := decide(s, "carol", "doc", hour(1), "a"); !d.Allowed {
		t.Fatal("过期前历史时刻的判定应仍为允许")
	}
}

func TestMultiPathIndependence(t *testing.T) {
	s, _ := newTestService()
	s.GrantBase("alice", perm("doc", []string{"a"}, nil))
	s.GrantBase("zoe", perm("doc", []string{"a"}, nil))
	idAB := mustDelegate(t, s, DelegationRequest{
		Delegator: "alice", Delegatee: "bob",
		Subset: perm("doc", []string{"a"}, nil),
		Start:  t0, End: hour(10),
	})
	mustDelegate(t, s, DelegationRequest{
		Delegator: "zoe", Delegatee: "bob",
		Subset: perm("doc", []string{"a"}, nil),
		Start:  t0, End: hour(10),
	})

	if err := s.Revoke(idAB); err != nil {
		t.Fatal(err)
	}
	// 撤销 alice 路径不影响 zoe 路径。
	d := decide(s, "bob", "doc", hour(1), "a")
	if !d.Allowed {
		t.Fatal("撤销一条路径不得影响仍有效的其他路径")
	}
	if len(d.Witness) == 0 {
		t.Fatal("放行应有见证链")
	}
	for _, w := range d.Witness {
		if w == idAB {
			t.Fatal("见证链不应包含已撤销的委托")
		}
	}
}

func TestHistoricalDecisionImmutable(t *testing.T) {
	s, clk := newTestService()
	s.GrantBase("alice", perm("doc", []string{"a", "b"}, nil))
	id := mustDelegate(t, s, DelegationRequest{
		Delegator: "alice", Delegatee: "bob",
		Subset: perm("doc", []string{"a"}, nil),
		Start:  t0, End: hour(5),
	})

	// t=1h 时刻的判定：允许。
	before := decide(s, "bob", "doc", hour(1), "a")
	if !before.Allowed {
		t.Fatal("t=1h 应允许")
	}

	clk.Advance(2 * time.Hour)
	// 之后发生收缩与撤销。
	s.ShrinkBase("alice", perm("doc", []string{"a"}, nil))
	if err := s.Revoke(id); err != nil {
		t.Fatal(err)
	}
	clk.Advance(10 * time.Hour) // 委托也已过期

	// 当前时刻判定：拒绝。
	if d := decide(s, "bob", "doc", hour(12), "a"); d.Allowed {
		t.Fatal("收缩+撤销+过期后应拒绝")
	}
	// 历史时刻判定：结论不变。
	after := decide(s, "bob", "doc", hour(1), "a")
	if after.Allowed != before.Allowed {
		t.Fatal("历史判定不得被后续收缩/撤销/过期追溯改变")
	}
	if len(after.Witness) != 1 || after.Witness[0] != id {
		t.Fatalf("历史见证链应仍为 [%d], 实际 %v", id, after.Witness)
	}
}

func TestDecideRecomputesEachCall(t *testing.T) {
	s, clk := newTestService()
	s.GrantBase("alice", perm("doc", []string{"a"}, nil))
	mustDelegate(t, s, DelegationRequest{
		Delegator: "alice", Delegatee: "bob",
		Subset: perm("doc", []string{"a"}, nil),
		Start:  t0, End: hour(10),
	})
	if d := decide(s, "bob", "doc", hour(0), "a"); !d.Allowed {
		t.Fatal("初次判定应允许")
	}
	// 状态变更后再次判定必须反映新状态（不缓存历史结论）。
	clk.Advance(time.Hour)
	s.ShrinkBase("alice", perm("doc", []string{"a"}, nil))
	if d := decide(s, "bob", "doc", hour(1), "a"); d.Allowed {
		t.Fatal("收缩后重新判定应拒绝（不得使用缓存结论）")
	}
}

func TestNodesVisitedIndependentOfTotalRecords(t *testing.T) {
	s, _ := newTestService()
	s.GrantBase("alice", perm("doc", []string{"a"}, nil))
	mustDelegate(t, s, DelegationRequest{
		Delegator: "alice", Delegatee: "bob",
		Subset: perm("doc", []string{"a"}, nil),
		Start:  t0, End: hour(100),
	})

	base := decide(s, "bob", "doc", hour(1), "a")
	if !base.Allowed {
		t.Fatal("应允许")
	}

	// 灌入大量与 bob 无关的委托记录。
	for i := 0; i < 2000; i++ {
		src := "src" + string(rune('a'+i%26)) + string(rune('0'+i%10))
		s.GrantBase(src, perm("doc", []string{"a"}, nil))
		mustDelegate(t, s, DelegationRequest{
			Delegator: src, Delegatee: "dst" + string(rune('a'+i%26)) + string(rune('0'+i%10)),
			Subset: perm("doc", []string{"a"}, nil),
			Start:  t0, End: hour(100),
		})
	}

	after := decide(s, "bob", "doc", hour(1), "a")
	if !after.Allowed {
		t.Fatal("应允许")
	}
	if after.NodesVisited != base.NodesVisited {
		t.Fatalf("遍历节点数不得随记录总数增长: 之前 %d, 之后 %d",
			base.NodesVisited, after.NodesVisited)
	}
}

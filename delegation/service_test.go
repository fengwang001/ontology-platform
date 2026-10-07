package delegation

import (
	"errors"
	"testing"
	"time"
)

var (
	t0    = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	permP = Permission{ObjectType: "Order", Attribute: "amount", RowScope: "region:cn"}
	permQ = Permission{ObjectType: "Order", Attribute: "*", RowScope: "*"}
	permR = Permission{ObjectType: "Customer", Attribute: "name", RowScope: "*"}
)

func newTestService() (*Service, *ManualClock, *MemoryRecorder) {
	clock := NewManualClock(t0)
	rec := NewMemoryRecorder()
	return NewService(clock, rec), clock, rec
}

// openWindow 返回一个从当前时刻起一小时的有效期窗口。
func openWindow(clock *ManualClock) (time.Time, time.Time) {
	return clock.Now(), clock.Now().Add(time.Hour)
}

func mustDeclare(t *testing.T, s *Service, in DelegationInput) uint64 {
	t.Helper()
	id, err := s.Declare(in)
	if err != nil {
		t.Fatalf("Declare(%s->%s) unexpected error: %v", in.Delegator, in.Delegatee, err)
	}
	return id
}

func assertCheck(t *testing.T, s *Service, subject string, p Permission, want bool) {
	t.Helper()
	if got := s.Check(subject, p); got.Allowed != want {
		t.Fatalf("Check(%s, %v) = %v, want %v", subject, p, got.Allowed, want)
	}
}

// 权限收缩沿委托链级联：A→B→C，收缩 A 的直接权限后 B、C 同步失去。
func TestCascadeShrinkAlongChain(t *testing.T) {
	s, clock, _ := newTestService()
	s.GrantDirect("A", permP)
	from, to := openWindow(clock)
	mustDeclare(t, s, DelegationInput{Delegator: "A", Delegatee: "B", Subset: []Permission{permP}, AllowRedelegate: true, ValidFrom: from, ValidTo: to})
	mustDeclare(t, s, DelegationInput{Delegator: "B", Delegatee: "C", Subset: []Permission{permP}, AllowRedelegate: true, ValidFrom: from, ValidTo: to})

	assertCheck(t, s, "C", permP, true)

	s.RevokeDirect("A", permP)
	assertCheck(t, s, "A", permP, false)
	assertCheck(t, s, "B", permP, false)
	assertCheck(t, s, "C", permP, false)
}

// 全有或全无：收缩后子集不再被完整支持，下游委托整体失效（不保留交集）。
func TestShrinkInvalidatesWholeDelegation(t *testing.T) {
	s, clock, _ := newTestService()
	s.GrantDirect("A", permP, permQ)
	from, to := openWindow(clock)
	mustDeclare(t, s, DelegationInput{Delegator: "A", Delegatee: "B", Subset: []Permission{permP, permQ}, AllowRedelegate: true, ValidFrom: from, ValidTo: to})

	assertCheck(t, s, "B", permP, true)
	assertCheck(t, s, "B", permQ, true)

	// 收缩后 A 只剩 permP，B 的委托声明 {P,Q} 不再被完整支持 → 整体失效。
	s.RevokeDirect("A", permQ)
	assertCheck(t, s, "B", permP, false)
	assertCheck(t, s, "B", permQ, false)
}

// 存在额外独立来源完整支持时，下游委托不受某一来源收缩影响。
func TestShrinkWithIndependentSourceSurvives(t *testing.T) {
	s, clock, _ := newTestService()
	s.GrantDirect("A", permP)
	s.GrantDirect("D", permP)
	from, to := openWindow(clock)
	mustDeclare(t, s, DelegationInput{Delegator: "A", Delegatee: "B", Subset: []Permission{permP}, AllowRedelegate: true, ValidFrom: from, ValidTo: to})
	mustDeclare(t, s, DelegationInput{Delegator: "D", Delegatee: "B", Subset: []Permission{permP}, AllowRedelegate: true, ValidFrom: from, ValidTo: to})
	mustDeclare(t, s, DelegationInput{Delegator: "B", Delegatee: "C", Subset: []Permission{permP}, AllowRedelegate: true, ValidFrom: from, ValidTo: to})

	s.RevokeDirect("A", permP)
	// D→B 仍完整支持 B 的权限，B→C 继续有效。
	assertCheck(t, s, "B", permP, true)
	assertCheck(t, s, "C", permP, true)

	s.RevokeDirect("D", permP)
	assertCheck(t, s, "B", permP, false)
	assertCheck(t, s, "C", permP, false)
}

// 撤销委托同样级联：撤销链中间一环，下游立即失效。
func TestRevokeMiddleLinkCascades(t *testing.T) {
	s, clock, _ := newTestService()
	s.GrantDirect("A", permP)
	from, to := openWindow(clock)
	ab := mustDeclare(t, s, DelegationInput{Delegator: "A", Delegatee: "B", Subset: []Permission{permP}, AllowRedelegate: true, ValidFrom: from, ValidTo: to})
	mustDeclare(t, s, DelegationInput{Delegator: "B", Delegatee: "C", Subset: []Permission{permP}, AllowRedelegate: true, ValidFrom: from, ValidTo: to})

	assertCheck(t, s, "C", permP, true)
	if err := s.Revoke(ab); err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	assertCheck(t, s, "B", permP, false)
	assertCheck(t, s, "C", permP, false)
}

// 直接权限被不可再委托的上游委托“替代”后，下游委托因失去再委托权而失效。
func TestNonRedelegatableSourceCannotSustainDownstream(t *testing.T) {
	s, clock, _ := newTestService()
	s.GrantDirect("A", permP)
	s.GrantDirect("B", permP)
	from, to := openWindow(clock)
	// A→B 不允许再委托；B 靠自己的直接权限声明 B→C。
	mustDeclare(t, s, DelegationInput{Delegator: "A", Delegatee: "B", Subset: []Permission{permP}, AllowRedelegate: false, ValidFrom: from, ValidTo: to})
	mustDeclare(t, s, DelegationInput{Delegator: "B", Delegatee: "C", Subset: []Permission{permP}, AllowRedelegate: true, ValidFrom: from, ValidTo: to})
	assertCheck(t, s, "C", permP, true)

	// B 的直接权限被收缩，仅剩的 A→B 不允许再委托 → B→C 失效。
	s.RevokeDirect("B", permP)
	assertCheck(t, s, "B", permP, true) // B 自己仍有效（来自 A→B）
	assertCheck(t, s, "C", permP, false)
}

// 多路径独立有效：两条路径都能支持同一访问，撤销其一不影响另一条。
func TestMultiPathIndependentValidity(t *testing.T) {
	s, clock, _ := newTestService()
	s.GrantDirect("A", permP)
	s.GrantDirect("D", permP)
	from, to := openWindow(clock)
	ab := mustDeclare(t, s, DelegationInput{Delegator: "A", Delegatee: "B", Subset: []Permission{permP}, AllowRedelegate: true, ValidFrom: from, ValidTo: to})
	mustDeclare(t, s, DelegationInput{Delegator: "D", Delegatee: "B", Subset: []Permission{permP}, AllowRedelegate: true, ValidFrom: from, ValidTo: to})

	assertCheck(t, s, "B", permP, true)
	if err := s.Revoke(ab); err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	assertCheck(t, s, "B", permP, true) // D→B 路径仍有效
}

// 自环被拒绝。
func TestCycleSelfLoopRejected(t *testing.T) {
	s, clock, _ := newTestService()
	s.GrantDirect("A", permP)
	from, to := openWindow(clock)
	_, err := s.Declare(DelegationInput{Delegator: "A", Delegatee: "A", Subset: []Permission{permP}, ValidFrom: from, ValidTo: to})
	if !errors.Is(err, ErrCycle) {
		t.Fatalf("self loop: got %v, want ErrCycle", err)
	}
}

// 长链回边成环被拒绝，且不影响链上已有委托。
func TestCycleLongChainRejected(t *testing.T) {
	s, clock, _ := newTestService()
	s.GrantDirect("A", permP)
	from, to := openWindow(clock)
	mustDeclare(t, s, DelegationInput{Delegator: "A", Delegatee: "B", Subset: []Permission{permP}, AllowRedelegate: true, ValidFrom: from, ValidTo: to})
	mustDeclare(t, s, DelegationInput{Delegator: "B", Delegatee: "C", Subset: []Permission{permP}, AllowRedelegate: true, ValidFrom: from, ValidTo: to})
	s.GrantDirect("C", permR)

	// C→A 会成环（A→B→C→A）。
	_, err := s.Declare(DelegationInput{Delegator: "C", Delegatee: "A", Subset: []Permission{permR}, ValidFrom: from, ValidTo: to})
	if !errors.Is(err, ErrCycle) {
		t.Fatalf("long cycle: got %v, want ErrCycle", err)
	}
	// 已有委托不受影响。
	assertCheck(t, s, "C", permP, true)
	assertCheck(t, s, "B", permP, true)
}

// 已撤销的边不再参与成环判定：撤销 A→B 后 B→A 可以成立。
func TestCycleIgnoresRevokedEdges(t *testing.T) {
	s, clock, _ := newTestService()
	s.GrantDirect("A", permP)
	s.GrantDirect("B", permR)
	from, to := openWindow(clock)
	ab := mustDeclare(t, s, DelegationInput{Delegator: "A", Delegatee: "B", Subset: []Permission{permP}, ValidFrom: from, ValidTo: to})
	if err := s.Revoke(ab); err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	mustDeclare(t, s, DelegationInput{Delegator: "B", Delegatee: "A", Subset: []Permission{permR}, ValidFrom: from, ValidTo: to})
	assertCheck(t, s, "A", permR, true)
}

// 已过期（但尚未被撤销）的边仍参与保守成环判定：
// 保证任意时刻的生效子图都是 DAG（设计文档记录的取舍）。
func TestCycleConservativeOnExpiredEdges(t *testing.T) {
	s, clock, _ := newTestService()
	s.GrantDirect("A", permP)
	s.GrantDirect("B", permR)
	from, to := openWindow(clock)
	mustDeclare(t, s, DelegationInput{Delegator: "A", Delegatee: "B", Subset: []Permission{permP}, ValidFrom: from, ValidTo: to})
	// 推进到 A→B 过期之后。
	clock.Advance(2 * time.Hour)
	from2, to2 := openWindow(clock)
	_, err := s.Declare(DelegationInput{Delegator: "B", Delegatee: "A", Subset: []Permission{permR}, ValidFrom: from2, ValidTo: to2})
	if !errors.Is(err, ErrCycle) {
		t.Fatalf("expired edge should still block cycle conservatively: got %v", err)
	}
}

// 有效期结束使该环及其下游全部失效；生效前也不可用。
func TestExpiryInvalidatesDownstream(t *testing.T) {
	s, clock, _ := newTestService()
	s.GrantDirect("A", permP)
	from, to := openWindow(clock)
	mustDeclare(t, s, DelegationInput{Delegator: "A", Delegatee: "B", Subset: []Permission{permP}, AllowRedelegate: true, ValidFrom: from, ValidTo: to})
	mustDeclare(t, s, DelegationInput{Delegator: "B", Delegatee: "C", Subset: []Permission{permP}, AllowRedelegate: true, ValidFrom: from, ValidTo: to})

	assertCheck(t, s, "C", permP, true)
	clock.Advance(2 * time.Hour) // 越过有效期止
	assertCheck(t, s, "B", permP, false)
	assertCheck(t, s, "C", permP, false)
}

// 有效期尚未开始时不生效。
func TestNotYetValidDelegationInactive(t *testing.T) {
	s, clock, _ := newTestService()
	s.GrantDirect("A", permP)
	from := clock.Now().Add(time.Hour)
	to := from.Add(time.Hour)
	mustDeclare(t, s, DelegationInput{Delegator: "A", Delegatee: "B", Subset: []Permission{permP}, ValidFrom: from, ValidTo: to})
	assertCheck(t, s, "B", permP, false)
	clock.Advance(90 * time.Minute)
	assertCheck(t, s, "B", permP, true)
}

// 错误优先级 1：声明子集超出委托方实际拥有范围（即使同时成环、已过期）。
func TestErrorPrecedenceScopeFirst(t *testing.T) {
	s, clock, _ := newTestService()
	s.GrantDirect("A", permP)
	from, to := openWindow(clock)
	mustDeclare(t, s, DelegationInput{Delegator: "A", Delegatee: "B", Subset: []Permission{permP}, AllowRedelegate: true, ValidFrom: from, ValidTo: to})

	// B 没有任何权限，声明 permQ 超范围；B→A 也会成环；有效期已过。
	past := clock.Now().Add(-time.Hour)
	_, err := s.Declare(DelegationInput{Delegator: "B", Delegatee: "A", Subset: []Permission{permQ}, ValidFrom: past, ValidTo: past.Add(time.Minute)})
	if !errors.Is(err, ErrScopeExceeded) {
		t.Fatalf("got %v, want ErrScopeExceeded", err)
	}
}

// 错误优先级 2：子集在实际拥有范围内、但依赖的上游委托不允许再委托
// （即使同时成环、已过期）。
func TestErrorPrecedenceRedelegationSecond(t *testing.T) {
	s, clock, _ := newTestService()
	s.GrantDirect("A", permP)
	from, to := openWindow(clock)
	// A→B 不允许再委托；B 实际拥有 permP 但不能再委托。
	mustDeclare(t, s, DelegationInput{Delegator: "A", Delegatee: "B", Subset: []Permission{permP}, AllowRedelegate: false, ValidFrom: from, ValidTo: to})
	// B→A 同时满足“禁止再委托、成环、已过期”，应报禁止再委托。
	past := clock.Now().Add(-time.Hour)
	_, err := s.Declare(DelegationInput{Delegator: "B", Delegatee: "A", Subset: []Permission{permP}, ValidFrom: past, ValidTo: past.Add(time.Minute)})
	if !errors.Is(err, ErrRedelegationDenied) {
		t.Fatalf("got %v, want ErrRedelegationDenied", err)
	}
}

// 错误优先级 3：成环优先于已过期。
func TestErrorPrecedenceCycleBeforeExpiry(t *testing.T) {
	s, clock, _ := newTestService()
	s.GrantDirect("A", permP)
	from, to := openWindow(clock)
	mustDeclare(t, s, DelegationInput{Delegator: "A", Delegatee: "B", Subset: []Permission{permP}, AllowRedelegate: true, ValidFrom: from, ValidTo: to})

	past := clock.Now().Add(-time.Hour)
	_, err := s.Declare(DelegationInput{Delegator: "B", Delegatee: "A", Subset: []Permission{permP}, ValidFrom: past, ValidTo: past.Add(time.Minute)})
	if !errors.Is(err, ErrCycle) {
		t.Fatalf("got %v, want ErrCycle", err)
	}
}

// 错误优先级 4：仅已过期时报 ErrExpired。
func TestErrorExpiredAlone(t *testing.T) {
	s, clock, _ := newTestService()
	s.GrantDirect("A", permP)
	past := clock.Now().Add(-time.Hour)
	_, err := s.Declare(DelegationInput{Delegator: "A", Delegatee: "B", Subset: []Permission{permP}, ValidFrom: past, ValidTo: past.Add(time.Minute)})
	if !errors.Is(err, ErrExpired) {
		t.Fatalf("got %v, want ErrExpired", err)
	}
}

// 被拒绝的声明不改变任何已有状态。
func TestRejectedDeclareHasNoSideEffects(t *testing.T) {
	s, clock, _ := newTestService()
	s.GrantDirect("A", permP)
	from, to := openWindow(clock)
	mustDeclare(t, s, DelegationInput{Delegator: "A", Delegatee: "B", Subset: []Permission{permP}, AllowRedelegate: true, ValidFrom: from, ValidTo: to})

	// 各类拒绝各来一次。
	if _, err := s.Declare(DelegationInput{Delegator: "B", Delegatee: "C", Subset: []Permission{permQ}, ValidFrom: from, ValidTo: to}); !errors.Is(err, ErrScopeExceeded) {
		t.Fatalf("want ErrScopeExceeded")
	}
	if _, err := s.Declare(DelegationInput{Delegator: "B", Delegatee: "A", Subset: []Permission{permP}, ValidFrom: from, ValidTo: to}); !errors.Is(err, ErrCycle) {
		t.Fatalf("want ErrCycle")
	}
	past := clock.Now().Add(-time.Hour)
	if _, err := s.Declare(DelegationInput{Delegator: "A", Delegatee: "C", Subset: []Permission{permP}, ValidFrom: past, ValidTo: past.Add(time.Minute)}); !errors.Is(err, ErrExpired) {
		t.Fatalf("want ErrExpired")
	}

	// 状态不变：B 仍持有 permP，C 仍无任何权限。
	assertCheck(t, s, "B", permP, true)
	assertCheck(t, s, "C", permP, false)
	assertCheck(t, s, "C", permQ, false)
}

// 历史判定不可追溯：t1 时刻的判定结论在后续收缩、撤销、过期后保持不变。
func TestHistoricalDecisionImmutable(t *testing.T) {
	s, clock, _ := newTestService()
	s.GrantDirect("A", permP)
	from, to := openWindow(clock)
	mustDeclare(t, s, DelegationInput{Delegator: "A", Delegatee: "B", Subset: []Permission{permP}, AllowRedelegate: true, ValidFrom: from, ValidTo: to})
	mustDeclare(t, s, DelegationInput{Delegator: "B", Delegatee: "C", Subset: []Permission{permP}, AllowRedelegate: true, ValidFrom: from, ValidTo: to})

	clock.Advance(time.Minute)
	t1 := clock.Now()
	if got := s.Check("C", permP); !got.Allowed {
		t.Fatalf("t1 check should allow")
	}

	// 之后发生收缩、撤销与过期。
	clock.Advance(time.Minute)
	s.RevokeDirect("A", permP)
	clock.Advance(3 * time.Hour)
	assertCheck(t, s, "C", permP, false)

	// 事后查询 t1：结论必须仍是允许。
	if got := s.CheckAt("C", permP, t1); !got.Allowed {
		t.Fatalf("CheckAt(t1) must remain allowed after shrink/expiry")
	}
	// 事后查询更早时刻（委托尚未声明）：必须是拒绝。
	if got := s.CheckAt("C", permP, t0.Add(-time.Minute)); got.Allowed {
		t.Fatalf("CheckAt(before declare) must be denied")
	}
}

// 历史判定也尊重“当时尚未发生的撤销”。
func TestHistoricalDecisionBeforeRevoke(t *testing.T) {
	s, clock, _ := newTestService()
	s.GrantDirect("A", permP)
	from, to := openWindow(clock)
	ab := mustDeclare(t, s, DelegationInput{Delegator: "A", Delegatee: "B", Subset: []Permission{permP}, ValidFrom: from, ValidTo: to})

	clock.Advance(time.Minute)
	t1 := clock.Now()
	clock.Advance(time.Minute)
	if err := s.Revoke(ab); err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	assertCheck(t, s, "B", permP, false)
	if got := s.CheckAt("B", permP, t1); !got.Allowed {
		t.Fatalf("CheckAt(t1) before revoke must be allowed")
	}
}

// 判定日志完整记录输入、输出与委托链依据。
func TestDecisionLogRecordsEvidence(t *testing.T) {
	s, clock, rec := newTestService()
	s.GrantDirect("A", permP)
	from, to := openWindow(clock)
	ab := mustDeclare(t, s, DelegationInput{Delegator: "A", Delegatee: "B", Subset: []Permission{permP}, AllowRedelegate: true, ValidFrom: from, ValidTo: to})
	bc := mustDeclare(t, s, DelegationInput{Delegator: "B", Delegatee: "C", Subset: []Permission{permP}, AllowRedelegate: true, ValidFrom: from, ValidTo: to})

	res := s.Check("C", permP)
	if !res.Allowed {
		t.Fatalf("should allow")
	}
	if len(res.Witness) != 2 || res.Witness[0] != bc || res.Witness[1] != ab {
		t.Fatalf("witness = %v, want [%d %d]", res.Witness, bc, ab)
	}

	entries := rec.Entries()
	if len(entries) == 0 {
		t.Fatalf("log is empty")
	}
	last := entries[len(entries)-1]
	if last.Op != OpCheck || last.Subject != "C" || !last.Allowed {
		t.Fatalf("bad check log entry: %+v", last)
	}
	if len(last.Witness) != 2 {
		t.Fatalf("log witness = %v", last.Witness)
	}
	// 每次调用都有日志：grant + 2 declare + check = 4。
	if len(entries) != 4 {
		t.Fatalf("log entries = %d, want 4", len(entries))
	}
	// 序号严格递增。
	for i := 1; i < len(entries); i++ {
		if entries[i].Seq <= entries[i-1].Seq {
			t.Fatalf("seq not increasing at %d", i)
		}
	}
}

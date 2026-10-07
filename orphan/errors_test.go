package orphan

import "testing"

func seedStore(t *testing.T) *Store {
	t.Helper()
	s := NewStore(RuleSpec{RequiredLinkTypes: map[ObjectTypeID][]LinkTypeID{"X": {"L"}}, GracePeriod: 5}, 0)
	if err := s.Append(
		Event{ID: "lt1", Kind: EvLinkTypeCreated, LinkType: "L", Time: 0},
		Event{ID: "o1", Kind: EvObjectCreated, Object: "o", ObjectType: "X", Time: 10},
		Event{ID: "p1", Kind: EvObjectCreated, Object: "p", ObjectType: "Y", Time: 10},
	); err != nil {
		t.Fatal(err)
	}
	return s
}

func errCode(t *testing.T, s *Store, q Query) ErrorCode {
	t.Helper()
	_, err := s.Determine(q)
	if err == nil {
		t.Fatalf("query %+v: expected error", q)
	}
	e, ok := err.(*Error)
	if !ok {
		t.Fatalf("query %+v: error type %T", q, err)
	}
	return e.Code
}

func TestErrDanglingReferenceVariants(t *testing.T) {
	// 链接引用了尚未创建的链接类型。
	s := seedStore(t)
	if err := s.Append(Event{ID: "l1", Kind: EvLinkCreated, Link: "k", LinkType: "UNKNOWN", From: "o", To: "p", Time: 12}); err != nil {
		t.Fatal(err)
	}
	if got := errCode(t, s, Query{Object: "o", At: 20, Version: 1}); got != ErrDanglingReference {
		t.Fatalf("unknown link type: got %v", got)
	}

	// 撤销了尚未创建的链接。
	s = seedStore(t)
	if err := s.Append(
		Event{ID: "l1", Kind: EvLinkCreated, Link: "k", LinkType: "L", From: "o", To: "p", Time: 12},
		Event{ID: "r1", Kind: EvLinkRevoked, Link: "ghost", Time: 13},
	); err != nil {
		t.Fatal(err)
	}
	// ghost 撤销无法归属到任何对象切片，o 的判定不受影响。
	if _, err := s.Determine(Query{Object: "o", At: 20, Version: 1}); err != nil {
		t.Fatalf("unattributable revoke should not affect o: %v", err)
	}
	// 但全量重建必须报告悬置引用。
	if _, err := s.RebuildNetwork(20); err == nil || err.(*Error).Code != ErrDanglingReference {
		t.Fatalf("rebuild should report dangling reference, got %v", err)
	}

	// 属性赋值发生在对象创建之前。
	s = seedStore(t)
	if err := s.Append(Event{ID: "ps1", Kind: EvPropertySet, Object: "o", Key: "a", Value: "b", Time: 5}); err != nil {
		t.Fatal(err)
	}
	if got := errCode(t, s, Query{Object: "o", At: 20, Version: 1}); got != ErrDanglingReference {
		t.Fatalf("property before creation: got %v", got)
	}

	// 查询从未创建的对象。
	s = seedStore(t)
	if got := errCode(t, s, Query{Object: "nobody", At: 20, Version: 1}); got != ErrDanglingReference {
		t.Fatalf("unknown object: got %v", got)
	}
}

func TestErrTimeBeforeFirstAppearance(t *testing.T) {
	s := seedStore(t)
	if got := errCode(t, s, Query{Object: "o", At: 5, Version: 1}); got != ErrTimeBeforeFirstAppearance {
		t.Fatalf("got %v, want TimeBeforeFirstAppearance", got)
	}
}

func TestErrAmbiguousOrderAndCausalChain(t *testing.T) {
	// 同一对象上同刻并列事件无因果链 → 顺序不可确定。
	s := seedStore(t)
	if err := s.Append(
		Event{ID: "ps1", Kind: EvPropertySet, Object: "o", Key: "a", Value: "1", Time: 12},
		Event{ID: "ps2", Kind: EvPropertySet, Object: "o", Key: "a", Value: "2", Time: 12},
	); err != nil {
		t.Fatal(err)
	}
	if got := errCode(t, s, Query{Object: "o", At: 20, Version: 1}); got != ErrAmbiguousOrder {
		t.Fatalf("got %v, want AmbiguousOrder", got)
	}

	// 同刻但存在完整 After 链 → 顺序可确定，判定正常。
	s = seedStore(t)
	if err := s.Append(
		Event{ID: "ps1", Kind: EvPropertySet, Object: "o", Key: "a", Value: "1", Time: 12},
		Event{ID: "ps2", Kind: EvPropertySet, Object: "o", Key: "a", Value: "2", Time: 12, After: "ps1"},
	); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Determine(Query{Object: "o", At: 20, Version: 1}); err != nil {
		t.Fatalf("causal chain should resolve order: %v", err)
	}
}

// 错误优先级：RuleVersionVoided > AmbiguousOrder > DanglingReference > TimeBeforeFirstAppearance。
func TestErrorPriority(t *testing.T) {
	mk := func(t *testing.T) *Store {
		s := seedStore(t)
		// 同刻并列（E4 条件）+ 创建前属性（E2 条件）。
		if err := s.Append(
			Event{ID: "ps1", Kind: EvPropertySet, Object: "o", Key: "a", Value: "1", Time: 5},
			Event{ID: "ps2", Kind: EvPropertySet, Object: "o", Key: "a", Value: "2", Time: 5},
		); err != nil {
			t.Fatal(err)
		}
		// 追溯调整使 v1 作废（E1 条件）。
		if _, err := s.AdjustRule(RuleSpec{RequiredLinkTypes: map[ObjectTypeID][]LinkTypeID{}, GracePeriod: 1}, true, 30); err != nil {
			t.Fatal(err)
		}
		return s
	}

	// E1+E4+E2 同时具备 → 只报 E1。
	s := mk(t)
	if got := errCode(t, s, Query{Object: "o", At: 20, Version: 1}); got != ErrRuleVersionVoided {
		t.Fatalf("E1 priority: got %v", got)
	}
	// E4+E2 同时具备（声明版本正确）→ 只报 E4。
	if got := errCode(t, s, Query{Object: "o", At: 20, Version: 2}); got != ErrAmbiguousOrder {
		t.Fatalf("E4 priority: got %v", got)
	}

	// E2+E3 同时具备 → 只报 E2。
	s2 := NewStore(RuleSpec{RequiredLinkTypes: map[ObjectTypeID][]LinkTypeID{}, GracePeriod: 1}, 0)
	if err := s2.Append(
		Event{ID: "o1", Kind: EvObjectCreated, Object: "o", ObjectType: "X", Time: 10},
		Event{ID: "ps1", Kind: EvPropertySet, Object: "o", Key: "a", Value: "1", Time: 5},
	); err != nil {
		t.Fatal(err)
	}
	if got := errCode(t, s2, Query{Object: "o", At: 7, Version: 1}); got != ErrDanglingReference {
		t.Fatalf("E2 priority over E3: got %v", got)
	}
}

// 错误不得对事件流或规则账本产生任何可观察改动。
func TestErrorsHaveNoObservableSideEffects(t *testing.T) {
	s := seedStore(t)
	before := s.Events()
	ledgerBefore := s.RuleLedger()

	// 触发各类错误。
	_, _ = s.Determine(Query{Object: "o", At: 5, Version: 1})      // E3
	_, _ = s.Determine(Query{Object: "ghost", At: 20, Version: 1}) // E2
	_, _ = s.Determine(Query{Object: "o", At: 20, Version: 99})    // E1
	_, _ = s.RebuildNetwork(20)                                    // 成功，也不应改动

	after := s.Events()
	ledgerAfter := s.RuleLedger()
	if len(after) != len(before) {
		t.Fatalf("event stream changed: %d -> %d", len(before), len(after))
	}
	for i := range before {
		if before[i] != after[i] {
			t.Fatalf("event %d changed", i)
		}
	}
	if len(ledgerAfter) != len(ledgerBefore) {
		t.Fatalf("rule ledger changed")
	}
}

// 审计日志：每次判定都记录输入、依据版本与结论。
func TestAuditLogRecordsEveryDetermination(t *testing.T) {
	s := seedStore(t)
	if _, err := s.Determine(Query{Object: "o", At: 20, Version: 1}); err != nil {
		t.Fatal(err)
	}
	_, _ = s.Determine(Query{Object: "o", At: 5, Version: 1}) // 失败也记录

	log := s.AuditLog()
	if len(log) != 2 {
		t.Fatalf("audit entries: %d, want 2", len(log))
	}
	if log[0].Query.At != 20 || log[0].Declared != 1 || log[0].Governing != 1 || log[0].Err != ErrNone {
		t.Fatalf("audit[0]: %+v", log[0])
	}
	if log[1].Err != ErrTimeBeforeFirstAppearance {
		t.Fatalf("audit[1]: %+v", log[1])
	}
}

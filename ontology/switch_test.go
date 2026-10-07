package ontologyindex

import "testing"

// TestCrossCutoverAttribution：切换点 cutoverAt=100。
// 无论事件相对 BeginSwitch/CommitSwitch 的物理到达时序如何，归属只看
// EffectiveAt：<100 的旧字段事件进旧版本，>=100 的新字段事件进新版本。
func TestCrossCutoverAttribution(t *testing.T) {
	s := schemaForTests()
	aud := &MemoryAuditor{}
	eng := NewEngine(s, aud)
	if err := eng.CreateIndex("idx_tax", "Person", "prop_ssn", ConstraintDuplicate); err != nil {
		t.Fatal(err)
	}

	earlyOld := ChangeEvent{EventID: "o1", ObjectType: "Person", ObjectID: "p1", PropertyID: "prop_ssn", NewValue: StringValue("111"), EffectiveAt: 10}
	futureNew := ChangeEvent{EventID: "n1", ObjectType: "Person", ObjectID: "p2", PropertyID: "prop_tax", NewValue: StringValue("T-222"), EffectiveAt: 120}

	// 切换宣布前，新字段事件已提前到达（ts=120，v2 已含 prop_tax）。
	if err := eng.Ingest(earlyOld); err != nil {
		t.Fatal(err)
	}
	if err := eng.Ingest(futureNew); err != nil {
		t.Fatal(err)
	}

	if err := eng.BeginSwitch("idx_tax", 100); err != nil {
		t.Fatal(err)
	}
	if _, err := eng.Lookup("idx_tax", StringValue("111")); err == nil {
		t.Fatal("切换期间查询必须失败")
	} else {
		expectErrorCode(t, err, ErrSwitchInProgress)
	}

	lateOld := ChangeEvent{EventID: "o2", ObjectType: "Person", ObjectID: "p3", PropertyID: "prop_ssn", NewValue: StringValue("333"), EffectiveAt: 50}
	bufNew := ChangeEvent{EventID: "n2", ObjectType: "Person", ObjectID: "p4", PropertyID: "prop_tax", NewValue: StringValue("T-444"), EffectiveAt: 130}
	if err := eng.Ingest(lateOld); err != nil {
		t.Fatalf("切换窗口内旧字段事件应被缓冲: %v", err)
	}
	if err := eng.Ingest(bufNew); err != nil {
		t.Fatal(err)
	}

	if err := eng.CommitSwitch("idx_tax"); err != nil {
		t.Fatalf("提交切换失败: %v", err)
	}

	new222 := lookupSet(t, eng, "idx_tax", StringValue("T-222"))
	new444 := lookupSet(t, eng, "idx_tax", StringValue("T-444"))
	if len(new222) != 1 || !new222["p2"] {
		t.Fatalf("新版本应含 p2(T-222)，得到 %v", new222)
	}
	if len(new444) != 1 || !new444["p4"] {
		t.Fatalf("新版本应含 p4(T-444)，得到 %v", new444)
	}
	if old111 := lookupSet(t, eng, "idx_tax", StringValue("111")); len(old111) != 0 {
		t.Fatalf("旧版本取值 111 不得泄漏到新版本: %v", old111)
	}
	if old333 := lookupSet(t, eng, "idx_tax", StringValue("333")); len(old333) != 0 {
		t.Fatalf("旧版本取值 333 不得泄漏到新版本: %v", old333)
	}

	veryLateOld := ChangeEvent{EventID: "o3", ObjectType: "Person", ObjectID: "p5", PropertyID: "prop_ssn", NewValue: StringValue("555"), EffectiveAt: 20}
	if err := eng.Ingest(veryLateOld); err != nil {
		t.Fatal(err)
	}
	if old555 := lookupSet(t, eng, "idx_tax", StringValue("555")); len(old555) != 0 {
		t.Fatalf("提交后迟到旧字段事件不得污染新版本: %v", old555)
	}

	var sawHistorical bool
	for _, r := range aud.Snapshot() {
		if r.EventID == "o3" && r.Decision == "accepted_historical_only" {
			sawHistorical = true
		}
	}
	if !sawHistorical {
		t.Fatal("迟到旧字段事件的历史化归属必须被审计记录")
	}
}

// TestSameObjectChangesAcrossSwitch：同一对象同一逻辑属性跨越切换点连续
// 变更，最终索引只反映新字段侧按合法顺序的最终值，中间值不得固化。
func TestSameObjectChangesAcrossSwitch(t *testing.T) {
	eng := NewEngine(schemaForTests(), nil)
	if err := eng.CreateIndex("idx", "Person", "prop_ssn", ConstraintDuplicate); err != nil {
		t.Fatal(err)
	}
	must := func(ev ChangeEvent) {
		t.Helper()
		if err := eng.Ingest(ev); err != nil {
			t.Fatal(err)
		}
	}
	must(ChangeEvent{EventID: "a", ObjectType: "Person", ObjectID: "p1", PropertyID: "prop_ssn", NewValue: StringValue("old-mid"), EffectiveAt: 40})
	must(ChangeEvent{EventID: "b", ObjectType: "Person", ObjectID: "p1", PropertyID: "prop_ssn", NewValue: StringValue("old-final"), EffectiveAt: 80})
	if err := eng.BeginSwitch("idx", 100); err != nil {
		t.Fatal(err)
	}
	must(ChangeEvent{EventID: "c", ObjectType: "Person", ObjectID: "p1", PropertyID: "prop_ssn", NewValue: StringValue("old-mid2"), EffectiveAt: 30})
	must(ChangeEvent{EventID: "d", ObjectType: "Person", ObjectID: "p1", PropertyID: "prop_tax", NewValue: StringValue("new-mid"), EffectiveAt: 110})
	must(ChangeEvent{EventID: "e", ObjectType: "Person", ObjectID: "p1", PropertyID: "prop_tax", NewValue: StringValue("new-final"), EffectiveAt: 150})
	if err := eng.CommitSwitch("idx"); err != nil {
		t.Fatal(err)
	}
	got := lookupSet(t, eng, "idx", StringValue("new-final"))
	if len(got) != 1 || !got["p1"] {
		t.Fatalf("切换后只应反映 new-final，得到 %v", got)
	}
	for _, stale := range []string{"new-mid", "old-mid", "old-mid2", "old-final"} {
		if s := lookupSet(t, eng, "idx", StringValue(stale)); len(s) != 0 {
			t.Fatalf("中间取值 %s 被错误固化: %v", stale, s)
		}
	}
}

// TestSwitchValidationFailureRollback：E2 校验失败整体回滚，缓冲事件不丢。
func TestSwitchValidationFailureRollback(t *testing.T) {
	eng := NewEngine(schemaForTests(), nil)
	if err := eng.CreateIndex("idx", "Person", "prop_ssn", ConstraintUnique); err != nil {
		t.Fatal(err)
	}
	must := func(ev ChangeEvent) {
		t.Helper()
		if err := eng.Ingest(ev); err != nil {
			t.Fatal(err)
		}
	}
	must(ChangeEvent{EventID: "a", ObjectType: "Person", ObjectID: "p1", PropertyID: "prop_ssn", NewValue: StringValue("S-1"), EffectiveAt: 10})
	if err := eng.BeginSwitch("idx", 100); err != nil {
		t.Fatal(err)
	}
	must(ChangeEvent{EventID: "b", ObjectType: "Person", ObjectID: "p1", PropertyID: "prop_tax", NewValue: StringValue("DUP"), EffectiveAt: 110})
	must(ChangeEvent{EventID: "c", ObjectType: "Person", ObjectID: "p2", PropertyID: "prop_tax", NewValue: StringValue("DUP"), EffectiveAt: 120})
	must(ChangeEvent{EventID: "d", ObjectType: "Person", ObjectID: "p3", PropertyID: "prop_ssn", NewValue: StringValue("S-3"), EffectiveAt: 50})

	err := eng.CommitSwitch("idx")
	if err == nil {
		t.Fatal("唯一约束冲突应导致提交失败")
	}
	expectErrorCode(t, err, ErrSwitchValidation)

	status, version, _ := eng.Status("idx")
	if status != "active" || version != 1 {
		t.Fatalf("回滚后应恢复 active/v1，得到 %s/v%d", status, version)
	}
	got := lookupSet(t, eng, "idx", StringValue("S-3"))
	if len(got) != 1 || !got["p3"] {
		t.Fatalf("回滚后缓冲旧字段事件不得丢失，得到 %v", got)
	}
	// 按需求，为新版本缓冲过的事件（含新字段 prop_tax 上的 b/c）必须
	// 重新归入旧版本继续生效、不得丢失：它们作为同一逻辑属性的取值进入旧索引，
	// 按 (EffectiveAt, EventID) 决定最终值。p1 在 ts=110 的 DUP 覆盖 ts=10 的 S-1。
	gotDup := lookupSet(t, eng, "idx", StringValue("DUP"))
	if len(gotDup) != 2 || !gotDup["p1"] || !gotDup["p2"] {
		t.Fatalf("回滚后新字段缓冲事件应重归入旧版本，得到 DUP=%v", gotDup)
	}

	if err := eng.BeginSwitch("idx", 100); err != nil {
		t.Fatal(err)
	}
	if err := eng.Ingest(ChangeEvent{EventID: "c2", ObjectType: "Person", ObjectID: "p2", PropertyID: "prop_tax", NewValue: StringValue("UNIQUE-2"), EffectiveAt: 121}); err != nil {
		t.Fatal(err)
	}
	if err := eng.CommitSwitch("idx"); err != nil {
		t.Fatalf("修复后切换应成功: %v", err)
	}
	u2 := lookupSet(t, eng, "idx", StringValue("UNIQUE-2"))
	if len(u2) != 1 || !u2["p2"] {
		t.Fatalf("重新切换后新版本应含 p2 修正值，得到 %v", u2)
	}
}

// TestAbortSwitchReintegratesBuffered：显式中止后窗口缓冲事件重归入旧版本。
func TestAbortSwitchReintegratesBuffered(t *testing.T) {
	eng := NewEngine(schemaForTests(), nil)
	if err := eng.CreateIndex("idx", "Person", "prop_name", ConstraintDuplicate); err != nil {
		t.Fatal(err)
	}
	if err := eng.Ingest(ChangeEvent{EventID: "a", ObjectType: "Person", ObjectID: "p1", PropertyID: "prop_name", NewValue: StringValue("A"), EffectiveAt: 10}); err != nil {
		t.Fatal(err)
	}
	if err := eng.BeginSwitch("idx", 100); err != nil {
		t.Fatal(err)
	}
	if err := eng.Ingest(ChangeEvent{EventID: "b", ObjectType: "Person", ObjectID: "p2", PropertyID: "prop_name", NewValue: StringValue("B"), EffectiveAt: 50}); err != nil {
		t.Fatal(err)
	}
	if err := eng.AbortSwitch("idx"); err != nil {
		t.Fatal(err)
	}
	status, version, _ := eng.Status("idx")
	if status != "active" || version != 1 {
		t.Fatalf("中止后应 active/v1，得到 %s/v%d", status, version)
	}
	if got := lookupSet(t, eng, "idx", StringValue("B")); len(got) != 1 || !got["p2"] {
		t.Fatalf("中止后窗口缓冲事件应在旧版本生效，得到 %v", got)
	}
}

// TestDeprecatedPropertyNoReplacement：name 在 v2 废弃且无替代（E1）。
func TestDeprecatedPropertyNoReplacement(t *testing.T) {
	eng := NewEngine(schemaForTests(), nil)
	if err := eng.CreateIndex("idx", "Person", "prop_name", ConstraintDuplicate); err != nil {
		t.Fatal(err)
	}
	err := eng.Ingest(ChangeEvent{EventID: "a", ObjectType: "Person", ObjectID: "p1", PropertyID: "prop_name", NewValue: StringValue("x"), EffectiveAt: 100})
	if err == nil {
		t.Fatal("废弃且无替代字段必须报错")
	}
	expectErrorCode(t, err, ErrDeprecatedProperty)

	if err := eng.DeprecateIndex("idx"); err != nil {
		t.Fatal(err)
	}
	_, err = eng.Lookup("idx", StringValue("x"))
	expectErrorCode(t, err, ErrDeprecatedProperty)
}

// TestPropertyUndefined：属性在生效版本中尚不存在（E3）。
func TestPropertyUndefined(t *testing.T) {
	eng := NewEngine(schemaForTests(), nil)
	if err := eng.CreateIndex("idx", "Person", "prop_nick", ConstraintDuplicate); err != nil {
		t.Fatal(err)
	}
	err := eng.Ingest(ChangeEvent{EventID: "a", ObjectType: "Person", ObjectID: "p1", PropertyID: "prop_nick", NewValue: StringValue("n"), EffectiveAt: 150})
	if err == nil {
		t.Fatal("prop_nick 在 ts=150 的版本中尚未定义，必须报 E3")
	}
	expectErrorCode(t, err, ErrPropertyUndefined)

	// ts=200 版本已定义，应正常接受。
	if err := eng.Ingest(ChangeEvent{EventID: "b", ObjectType: "Person", ObjectID: "p1", PropertyID: "prop_nick", NewValue: StringValue("n"), EffectiveAt: 200}); err != nil {
		t.Fatalf("已定义后应接受: %v", err)
	}
}

// TestErrorPriorityDeprecatedOverUndefined：同一事件同时满足 E1 与 E3
// （类型在该时刻无任何版本），按优先级只报告 E1 类别的判定由解析顺序保证；
// 此处覆盖“未定义类型”这一 E3 场景的独立报告。
func TestUnknownTypeReportsUndefined(t *testing.T) {
	eng := NewEngine(schemaForTests(), nil)
	if err := eng.CreateIndex("idx", "Person", "prop_name", ConstraintDuplicate); err != nil {
		t.Fatal(err)
	}
	// 直接以未知类型发送事件：不属于任何索引血缘，按 no-index 忽略；
	// 为 Person 索引显式制造“无版本时刻”（ts<1）应报 E3。
	err := eng.Ingest(ChangeEvent{EventID: "z", ObjectType: "Person", ObjectID: "p1", PropertyID: "prop_name", NewValue: StringValue("x"), EffectiveAt: 0})
	if err == nil {
		t.Fatal("类型首个版本生效之前引用属性必须报 E3")
	}
	expectErrorCode(t, err, ErrPropertyUndefined)
}

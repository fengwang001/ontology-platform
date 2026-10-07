package ontology

import (
	"testing"
)

// checkViewInvariants 校验视图可观察状态的核心不变量：
// 分组键有序、组内按裁决规则有序、对象不重复出现在多个分组、
// 成员归一化时刻与其分组键一致。
func checkViewInvariants(t *testing.T, groups []GroupView) {
	t.Helper()
	seen := make(map[string]int64)
	prevKey := int64(-1) << 62
	for _, g := range groups {
		if g.Key < prevKey {
			t.Fatalf("group keys not sorted: %v", groups)
		}
		prevKey = g.Key
		for i, m := range g.Members {
			if groupKeyOf(m.InstantUnix) != g.Key {
				t.Fatalf("member %s instant %d inconsistent with group key %d",
					m.ObjectID, m.InstantUnix, g.Key)
			}
			if k, dup := seen[m.ObjectID]; dup {
				t.Fatalf("object %s in two groups %d and %d", m.ObjectID, k, g.Key)
			}
			seen[m.ObjectID] = g.Key
			if i > 0 {
				a, b := g.Members[i-1], m
				less := a.InstantUnix < b.InstantUnix ||
					(a.InstantUnix == b.InstantUnix &&
						(a.ObjectTypeID < b.ObjectTypeID ||
							(a.ObjectTypeID == b.ObjectTypeID && a.ObjectID < b.ObjectID)))
				if !less {
					t.Fatalf("members out of adjudicated order: %v then %v", a, b)
				}
			}
		}
	}
}

func TestErrNoDefaultTimezoneAtWrite(t *testing.T) {
	p := NewPlatform()
	p.AddObjectType("T1", "at", nil) // 注册时不定义默认时区
	p.AddObjectType("T2", "at", &TzDefVersion{Version: 1, ZoneID: "UTC", EffectiveSeq: 1})
	p.AddLink("l", "T1", "T2")
	p.CreateView("v", "l")
	evt := p.WriteTimeProperty("T1", "x1", local(2024, 1, 1, 0, 0))
	if evt.AnchoredTzVersion != 0 {
		t.Fatalf("expected anchored version 0, got %d", evt.AnchoredTzVersion)
	}
	res := p.Maintain("v")
	if res.TopError == nil || res.TopError.Kind != ErrNoDefaultTimezoneAtWrite {
		t.Fatalf("TopError = %v", res.TopError)
	}
	groups, _ := p.Query("v")
	if countObject(groups, "x1") != 0 {
		t.Fatalf("x1 must not be grouped: %v", groups)
	}
}

func TestErrLinkEndpointTypeMissing(t *testing.T) {
	p := newFixture()
	p.WriteTimeProperty("Order", "o1", local(2024, 1, 1, 0, 0))
	p.Maintain("v1")

	// 情形一：事件对象类型不存在。
	p.InjectEvent(ChangeEvent{ObjectID: "g1", ObjectTypeID: "Ghost", WriteSeq: 50,
		LocalValue: local(2024, 1, 1, 1, 0), AnchoredTzVersion: 1, AnchoredZoneID: "UTC"})
	res := p.Maintain("v1")
	if res.TopError == nil || res.TopError.Kind != ErrLinkEndpointTypeMissing {
		t.Fatalf("TopError = %v", res.TopError)
	}

	// 情形二：链接端点类型被删除，既有成员被清空，后续事件报错。
	p.DeleteObjectType("Shipment")
	groups, _ := p.Query("v1")
	if len(groups) != 0 {
		t.Fatalf("view must be empty after endpoint type deletion: %v", groups)
	}
	p.WriteTimeProperty("Order", "o2", local(2024, 1, 1, 2, 0))
	res = p.Maintain("v1")
	if res.TopError == nil || res.TopError.Kind != ErrLinkEndpointTypeMissing {
		t.Fatalf("TopError after deletion = %v", res.TopError)
	}
}

func TestErrGroupingPropertyDeprecated(t *testing.T) {
	p := newFixture()
	p.WriteTimeProperty("Order", "o1", local(2024, 1, 1, 0, 0))
	p.Maintain("v1")

	p.DeprecateGroupingProperty("Order")
	groups, _ := p.Query("v1")
	if countObject(groups, "o1") != 0 {
		t.Fatalf("o1 must be evicted after deprecation: %v", groups)
	}
	if countObject(groups, "s1") != 0 {
		t.Fatalf("unrelated type members must not be affected")
	}

	p.WriteTimeProperty("Order", "o2", local(2024, 1, 1, 1, 0))
	res := p.Maintain("v1")
	if res.TopError == nil || res.TopError.Kind != ErrGroupingPropertyDeprecated {
		t.Fatalf("TopError = %v", res.TopError)
	}
}

func TestErrMigrationValidationFailed(t *testing.T) {
	p := newFixture()
	cases := []struct {
		name string
		tv   TzDefVersion
	}{
		{"version not increasing", TzDefVersion{Version: 1, ZoneID: "UTC+05:30", EffectiveSeq: 10}},
		{"retroactive effective seq", TzDefVersion{Version: 2, ZoneID: "UTC+05:30", EffectiveSeq: 1}},
		{"unknown zone", TzDefVersion{Version: 2, ZoneID: "Mars/Olympus", EffectiveSeq: 10}},
	}
	for _, c := range cases {
		if err := p.MigrateDefaultTz("Order", c.tv); err == nil ||
			err.(*ViewError).Kind != ErrMigrationValidationFailed {
			t.Fatalf("%s: expected migration validation error, got %v", c.name, err)
		}
	}
	if err := p.MigrateDefaultTz("NoSuchType", TzDefVersion{Version: 1, ZoneID: "UTC", EffectiveSeq: 1}); err == nil {
		t.Fatalf("missing type: expected migration validation error")
	}
	// 全部被拒绝，注册表仍只有 v1，视图行为不变。
	p.WriteTimeProperty("Order", "o1", local(2024, 1, 1, 0, 0))
	res := p.Maintain("v1")
	if res.TopError != nil {
		t.Fatalf("view must be unaffected by rejected migrations: %v", res.TopError)
	}
}

// priorityFixture 构造可同时触发四类错误的场景。
// 返回的平台在调用 Maintain 后将依次触发：
//   - 事件对象类型不存在（ErrLinkEndpointTypeMissing）
//   - 分组属性已废弃（ErrGroupingPropertyDeprecated）
//   - 写入时刻未定义默认时区（ErrNoDefaultTimezoneAtWrite）
//   - 队列中的迁移校验失败（ErrMigrationValidationFailed）
func priorityFixture(withLinkMissing, withDeprecated, withNoTz, withMigration bool) *Platform {
	p := NewPlatform()
	p.AddObjectType("T1", "at", &TzDefVersion{Version: 1, ZoneID: "UTC", EffectiveSeq: 1})
	p.AddObjectType("T2", "at", nil) // 无默认时区定义
	p.AddLink("l", "T1", "T2")
	p.CreateView("v", "l")
	if withDeprecated {
		p.DeprecateGroupingProperty("T1")
	}
	if withLinkMissing {
		p.InjectEvent(ChangeEvent{ObjectID: "e-link", ObjectTypeID: "Ghost", WriteSeq: 90,
			LocalValue: local(2024, 1, 1, 0, 0), AnchoredTzVersion: 1, AnchoredZoneID: "UTC"})
	}
	if withDeprecated {
		p.InjectEvent(ChangeEvent{ObjectID: "e-dep", ObjectTypeID: "T1", WriteSeq: 91,
			LocalValue: local(2024, 1, 1, 0, 0), AnchoredTzVersion: 1, AnchoredZoneID: "UTC"})
	}
	if withNoTz {
		p.InjectEvent(ChangeEvent{ObjectID: "e-notz", ObjectTypeID: "T2", WriteSeq: 92,
			LocalValue: local(2024, 1, 1, 0, 0), AnchoredTzVersion: 0})
	}
	if withMigration {
		p.EnqueueMigration("T1", TzDefVersion{Version: 1, ZoneID: "UTC+05:30", EffectiveSeq: 100})
	}
	return p
}

// TestErrorPrioritySingleReport 验证同一次维护触发多类错误时只报告优先级最高的一类，
// 且优先级顺序为：链接端点类型缺失 > 分组属性废弃 > 写入时刻无默认时区 > 迁移校验失败。
func TestErrorPrioritySingleReport(t *testing.T) {
	steps := []struct {
		name                               string
		linkMissing, deprecated, noTz, mig bool
		want                               ErrorKind
	}{
		{"all four", true, true, true, true, ErrLinkEndpointTypeMissing},
		{"without link missing", false, true, true, true, ErrGroupingPropertyDeprecated},
		{"without deprecated", false, false, true, true, ErrNoDefaultTimezoneAtWrite},
		{"only migration", false, false, false, true, ErrMigrationValidationFailed},
	}
	for _, s := range steps {
		p := priorityFixture(s.linkMissing, s.deprecated, s.noTz, s.mig)
		res := p.Maintain("v")
		if res.TopError == nil || res.TopError.Kind != s.want {
			t.Fatalf("%s: TopError = %v, want kind %v", s.name, res.TopError, s.want)
		}
		// 各类错误的触发次数仍完整保留在 Errors 明细中。
		if s.linkMissing && res.Errors[ErrLinkEndpointTypeMissing] == 0 {
			t.Fatalf("%s: link-missing error not tallied", s.name)
		}
		if s.mig && res.Errors[ErrMigrationValidationFailed] == 0 {
			t.Fatalf("%s: migration error not tallied", s.name)
		}
	}
	// 全量触发时，四类错误必须全部出现在明细中。
	p := priorityFixture(true, true, true, true)
	res := p.Maintain("v")
	for _, k := range []ErrorKind{ErrLinkEndpointTypeMissing, ErrGroupingPropertyDeprecated,
		ErrNoDefaultTimezoneAtWrite, ErrMigrationValidationFailed} {
		if res.Errors[k] == 0 {
			t.Fatalf("error kind %v not tallied in %+v", k, res.Errors)
		}
	}
}

// TestErrorsCauseNoObservableAnomaly 验证各类错误发生后视图不出现
// 重复计入或分组内时间倒退等可观察异常。
func TestErrorsCauseNoObservableAnomaly(t *testing.T) {
	p := priorityFixture(true, true, true, true)
	// 混入若干正常事件：T1 已废弃，改用 T2 的合法事件（先补一个时区定义）。
	if err := p.MigrateDefaultTz("T2", TzDefVersion{Version: 1, ZoneID: "UTC+08:00", EffectiveSeq: 95}); err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	p.InjectEvent(ChangeEvent{ObjectID: "ok1", ObjectTypeID: "T2", WriteSeq: 96,
		LocalValue: local(2024, 1, 1, 9, 0), AnchoredTzVersion: 1, AnchoredZoneID: "UTC+08:00"})
	p.InjectEvent(ChangeEvent{ObjectID: "ok2", ObjectTypeID: "T2", WriteSeq: 97,
		LocalValue: local(2024, 1, 2, 9, 0), AnchoredTzVersion: 1, AnchoredZoneID: "UTC+08:00"})
	res := p.Maintain("v")
	if res.TopError == nil {
		t.Fatalf("expected errors to be reported")
	}
	groups, _ := p.Query("v")
	checkViewInvariants(t, groups)
	if countObject(groups, "ok1") != 1 || countObject(groups, "ok2") != 1 {
		t.Fatalf("valid objects must be grouped exactly once: %v", groups)
	}
	if countObject(groups, "e-notz") != 0 || countObject(groups, "e-dep") != 0 || countObject(groups, "e-link") != 0 {
		t.Fatalf("rejected objects must not appear: %v", groups)
	}
}

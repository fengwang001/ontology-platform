package ontology

import (
	"fmt"
	"testing"
	"time"
)

// newFixture 构造标准夹具：两个对象类型（默认时区分别为 UTC 与 UTC+08:00）、
// 一条链接关系、一个聚合视图。
func newFixture() *Platform {
	p := NewPlatform()
	p.AddObjectType("Order", "createdAt", &TzDefVersion{Version: 1, ZoneID: "UTC", EffectiveSeq: 1})
	p.AddObjectType("Shipment", "shippedAt", &TzDefVersion{Version: 1, ZoneID: "UTC+08:00", EffectiveSeq: 1})
	p.AddLink("order-shipment", "Order", "Shipment")
	p.CreateView("v1", "order-shipment")
	return p
}

func local(y int, m time.Month, d, h, min int) time.Time {
	return time.Date(y, m, d, h, min, 0, 0, time.UTC)
}

// countObject 统计对象在查询结果中出现的次数（用于验证不重复计入）。
func countObject(groups []GroupView, objectID string) int {
	n := 0
	for _, g := range groups {
		for _, m := range g.Members {
			if m.ObjectID == objectID {
				n++
			}
		}
	}
	return n
}

// TestGroupingNormalizesToUTC 验证不同对象类型按各自锚定时区归一化后进入同一 UTC 日桶。
func TestGroupingNormalizesToUTC(t *testing.T) {
	p := newFixture()
	// Order 本地 2024-03-05 23:30 @UTC → UTC 2024-03-05 23:30（3 月 5 日桶）
	p.WriteTimeProperty("Order", "o1", local(2024, 3, 5, 23, 30))
	// Shipment 本地 2024-03-06 07:30 @UTC+8 → UTC 2024-03-05 23:30（同一日桶）
	p.WriteTimeProperty("Shipment", "s1", local(2024, 3, 6, 7, 30))
	res := p.Maintain("v1")
	if res.TopError != nil {
		t.Fatalf("unexpected error: %v", res.TopError)
	}
	groups, _ := p.Query("v1")
	if len(groups) != 1 {
		t.Fatalf("expected 1 group, got %d", len(groups))
	}
	if len(groups[0].Members) != 2 {
		t.Fatalf("expected 2 members, got %d", len(groups[0].Members))
	}
	wantKey := groupKeyOf(time.Date(2024, 3, 5, 23, 30, 0, 0, time.UTC).Unix())
	if groups[0].Key != wantKey {
		t.Fatalf("group key = %d, want %d", groups[0].Key, wantKey)
	}
}

// TestTieBreakDeterministic 验证同组同值的确定次序裁决规则：
// 归一化时刻相等时按 (对象类型 ID, 对象 ID) 字典序，且与处理先后无关。
func TestTieBreakDeterministic(t *testing.T) {
	// 两个对象归一化后时刻完全相等：
	// o1: 2024-01-01 00:00 @UTC；s1: 2024-01-01 08:00 @UTC+8。
	build := func(orderFirst bool) []GroupView {
		p := newFixture()
		if orderFirst {
			p.WriteTimeProperty("Order", "o1", local(2024, 1, 1, 0, 0))
			p.WriteTimeProperty("Shipment", "s1", local(2024, 1, 1, 8, 0))
		} else {
			p.WriteTimeProperty("Shipment", "s1", local(2024, 1, 1, 8, 0))
			p.WriteTimeProperty("Order", "o1", local(2024, 1, 1, 0, 0))
		}
		if res := p.Maintain("v1"); res.TopError != nil {
			t.Fatalf("unexpected error: %v", res.TopError)
		}
		groups, _ := p.Query("v1")
		return groups
	}
	a := build(true)
	b := build(false)
	if fmt.Sprint(a) != fmt.Sprint(b) {
		t.Fatalf("result depends on processing order:\n%v\n%v", a, b)
	}
	if len(a) != 1 || len(a[0].Members) != 2 {
		t.Fatalf("expected 1 group with 2 members, got %v", a)
	}
	// "Order" < "Shipment"，与写入顺序无关。
	if a[0].Members[0].ObjectID != "o1" || a[0].Members[1].ObjectID != "s1" {
		t.Fatalf("tie-break order wrong: %v", a[0].Members)
	}
	if a[0].Members[0].InstantUnix != a[0].Members[1].InstantUnix {
		t.Fatalf("instants should be equal after normalization")
	}
}

// TestRewriteKeepsSingleMembership 验证重写时间属性后对象只出现在一个分组。
func TestRewriteKeepsSingleMembership(t *testing.T) {
	p := newFixture()
	p.WriteTimeProperty("Order", "o1", local(2024, 1, 1, 10, 0))
	p.Maintain("v1")
	p.WriteTimeProperty("Order", "o1", local(2024, 2, 1, 10, 0))
	p.Maintain("v1")
	groups, _ := p.Query("v1")
	if n := countObject(groups, "o1"); n != 1 {
		t.Fatalf("o1 appears %d times, want 1", n)
	}
	wantKey := groupKeyOf(local(2024, 2, 1, 10, 0).Unix())
	if len(groups) != 1 || groups[0].Key != wantKey {
		t.Fatalf("o1 in wrong group: %v", groups)
	}
}

// TestMigrationDoesNotMoveExistingMembers 验证默认时区版本迁移后，
// 已归入分组的对象归属保持不变（不发生时间倒退、不重复计入）。
func TestMigrationDoesNotMoveExistingMembers(t *testing.T) {
	p := newFixture()
	p.WriteTimeProperty("Order", "o1", local(2024, 1, 1, 0, 30)) // @UTC → 1 月 1 日桶
	p.Maintain("v1")
	before, _ := p.Query("v1")

	// 迁移：Order 默认时区 UTC → UTC+08:00，自下一写入序号起生效。
	if err := p.MigrateDefaultTz("Order", TzDefVersion{Version: 2, ZoneID: "UTC+08:00", EffectiveSeq: 2}); err != nil {
		t.Fatalf("migration failed: %v", err)
	}
	after, _ := p.Query("v1")
	if fmt.Sprint(before) != fmt.Sprint(after) {
		t.Fatalf("migration moved existing members:\nbefore=%v\nafter=%v", before, after)
	}

	// 迁移后新写入的对象按新版本解释：本地 00:30 @UTC+8 → UTC 前一日 16:30。
	p.WriteTimeProperty("Order", "o2", local(2024, 1, 1, 0, 30))
	p.Maintain("v1")
	groups, _ := p.Query("v1")
	if countObject(groups, "o1") != 1 || countObject(groups, "o2") != 1 {
		t.Fatalf("duplicate membership: %v", groups)
	}
	// o1 仍在 1 月 1 日桶；o2 落在 12 月 31 日桶。
	keyOf := func(id string) int64 {
		for _, g := range groups {
			for _, m := range g.Members {
				if m.ObjectID == id {
					return g.Key
				}
			}
		}
		return -1
	}
	if keyOf("o1") != groupKeyOf(local(2024, 1, 1, 0, 30).Unix()) {
		t.Fatalf("o1 moved after migration")
	}
	wantO2 := groupKeyOf(local(2023, 12, 31, 16, 30).Unix())
	if keyOf("o2") != wantO2 {
		t.Fatalf("o2 group = %d, want %d", keyOf("o2"), wantO2)
	}
}

// TestDecisionLogRecordsJudgement 验证决策日志记录了判定输入、所依据的时区版本与结论。
func TestDecisionLogRecordsJudgement(t *testing.T) {
	p := newFixture()
	evt := p.WriteTimeProperty("Order", "o1", local(2024, 1, 1, 0, 30))
	p.Maintain("v1")
	p.MigrateDefaultTz("Order", TzDefVersion{Version: 2, ZoneID: "UTC+08:00", EffectiveSeq: 100})

	log := p.DecisionLog("v1")
	var assign, migrate *DecisionRecord
	for i := range log {
		switch log[i].Op {
		case "assign":
			assign = &log[i]
		case "migrate":
			migrate = &log[i]
		}
	}
	if assign == nil {
		t.Fatalf("no assign decision recorded")
	}
	if assign.AnchoredTzVersion != evt.AnchoredTzVersion || assign.ZoneID != "UTC" {
		t.Fatalf("assign decision missing anchored tz: %+v", assign)
	}
	if assign.ObjectID != "o1" || assign.WriteSeq != evt.WriteSeq || assign.GroupKey == 0 && assign.InstantUnix == 0 {
		t.Fatalf("assign decision incomplete: %+v", assign)
	}
	if migrate == nil || migrate.AnchoredTzVersion != 2 || migrate.ZoneID != "UTC+08:00" {
		t.Fatalf("migrate decision missing: %+v", migrate)
	}
}

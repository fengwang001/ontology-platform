package ontologyindex

import (
	"math/rand"
	"testing"
)

// TestOutOfOrderExhaustive：对同一对象同一属性的 3 条取值变更，穷举
// 所有 6 种物理到达排列。合法顺序由 (EffectiveAt, EventID) 唯一确定，
// 无论怎么乱序到达，最终索引都必须等于按逻辑顺序应用后的结果。
func TestOutOfOrderExhaustive(t *testing.T) {
	events := []ChangeEvent{
		{EventID: "e1", ObjectType: "Person", ObjectID: "p1", PropertyID: "prop_name", NewValue: StringValue("A"), EffectiveAt: 10},
		{EventID: "e2", ObjectType: "Person", ObjectID: "p1", PropertyID: "prop_name", NewValue: StringValue("B"), EffectiveAt: 20},
		{EventID: "e3", ObjectType: "Person", ObjectID: "p1", PropertyID: "prop_name", NewValue: StringValue("C"), EffectiveAt: 30},
	}
	perms := [][]int{{0, 1, 2}, {0, 2, 1}, {1, 0, 2}, {1, 2, 0}, {2, 0, 1}, {2, 1, 0}}
	for pi, perm := range perms {
		t.Run("perm", func(t *testing.T) {
			eng := NewEngine(schemaForTests(), nil)
			if err := eng.CreateIndex("idx_name", "Person", "prop_name", ConstraintDuplicate); err != nil {
				t.Fatal(err)
			}
			for k, i := range perm {
				ev := events[i]
				ev.ArrivedAt = int64(pi*10 + k)
				if err := eng.Ingest(ev); err != nil {
					t.Fatalf("perm=%v ingest 失败: %v", perm, err)
				}
			}
			got := lookupSet(t, eng, "idx_name", StringValue("C"))
			if !got["p1"] || len(got) != 1 {
				t.Fatalf("perm=%v 最终取值应为 C，得到 %v", perm, got)
			}
			if _, err := eng.Lookup("idx_name", StringValue("A")); err != nil {
				t.Fatal(err)
			}
			if a := lookupSet(t, eng, "idx_name", StringValue("A")); len(a) != 0 {
				t.Fatalf("perm=%v 中间取值 A 被错误固化: %v", perm, a)
			}
			if b := lookupSet(t, eng, "idx_name", StringValue("B")); len(b) != 0 {
				t.Fatalf("perm=%v 中间取值 B 被错误固化: %v", perm, b)
			}
		})
	}
}

// TestDuplicateDeliveryExhaustive：在一条到达序列中对每条事件插入若干
// 完全相同的重复投递，并打乱重复体的位置，最终结果必须与无重复一致。
func TestDuplicateDeliveryExhaustive(t *testing.T) {
	base := []ChangeEvent{
		{EventID: "d1", ObjectType: "Person", ObjectID: "p1", PropertyID: "prop_name", NewValue: StringValue("X"), EffectiveAt: 10},
		{EventID: "d2", ObjectType: "Person", ObjectID: "p1", PropertyID: "prop_name", NewValue: StringValue("Y"), EffectiveAt: 20},
		{EventID: "d3", ObjectType: "Person", ObjectID: "p2", PropertyID: "prop_name", NewValue: StringValue("Y"), EffectiveAt: 15},
	}
	rng := rand.New(rand.NewSource(42))
	for iter := 0; iter < 50; iter++ {
		var stream []ChangeEvent
		for _, ev := range base {
			stream = append(stream, ev, ev, ev) // 每条重复 3 次
		}
		rng.Shuffle(len(stream), func(i, j int) { stream[i], stream[j] = stream[j], stream[i] })

		aud := &MemoryAuditor{}
		eng := NewEngine(schemaForTests(), aud)
		if err := eng.CreateIndex("idx", "Person", "prop_name", ConstraintDuplicate); err != nil {
			t.Fatal(err)
		}
		for k, ev := range stream {
			ev.ArrivedAt = int64(k + 1)
			if err := eng.Ingest(ev); err != nil {
				t.Fatal(err)
			}
		}
		y := lookupSet(t, eng, "idx", StringValue("Y"))
		if len(y) != 2 || !y["p1"] || !y["p2"] {
			t.Fatalf("iter=%d 取值 Y 应对应 p1,p2，得到 %v", iter, y)
		}
		x := lookupSet(t, eng, "idx", StringValue("X"))
		if len(x) != 0 {
			t.Fatalf("iter=%d X 应已被覆盖，得到 %v", iter, x)
		}

		var dupCount int
		for _, r := range aud.Snapshot() {
			if r.Decision == "duplicate_ignored" {
				dupCount++
			}
		}
		if dupCount != 6 {
			t.Fatalf("iter=%d 应有 6 条重复被幂等忽略，实际 %d", iter, dupCount)
		}
	}
}

// TestTieBrokenByEventID：相同 EffectiveAt 的事件，EventID 字典序最大者胜。
func TestTieBrokenByEventID(t *testing.T) {
	eng := NewEngine(schemaForTests(), nil)
	if err := eng.CreateIndex("idx", "Person", "prop_name", ConstraintDuplicate); err != nil {
		t.Fatal(err)
	}
	stream := []ChangeEvent{
		{EventID: "zzz", ObjectType: "Person", ObjectID: "p1", PropertyID: "prop_name", NewValue: StringValue("late-id"), EffectiveAt: 50},
		{EventID: "aaa", ObjectType: "Person", ObjectID: "p1", PropertyID: "prop_name", NewValue: StringValue("early-id"), EffectiveAt: 50},
	}
	for k, ev := range stream {
		ev.ArrivedAt = int64(k + 1)
		if err := eng.Ingest(ev); err != nil {
			t.Fatal(err)
		}
	}
	if got := lookupSet(t, eng, "idx", StringValue("late-id")); len(got) != 1 || !got["p1"] {
		t.Fatalf("同时间戳应由 EventID 最大者获胜，得到 %v", got)
	}
}

// TestLateArrivalRewindsValue：先到达高时间戳、后到达低时间戳（乱序），
// 低时间戳不得覆盖高时间戳的最终取值。
func TestLateArrivalRewindsValue(t *testing.T) {
	eng := NewEngine(schemaForTests(), nil)
	if err := eng.CreateIndex("idx", "Person", "prop_name", ConstraintDuplicate); err != nil {
		t.Fatal(err)
	}
	high := ChangeEvent{EventID: "h", ObjectType: "Person", ObjectID: "p1", PropertyID: "prop_name", NewValue: StringValue("HIGH"), EffectiveAt: 90}
	low := ChangeEvent{EventID: "l", ObjectType: "Person", ObjectID: "p1", PropertyID: "prop_name", NewValue: StringValue("LOW"), EffectiveAt: 10}
	if err := eng.Ingest(high); err != nil {
		t.Fatal(err)
	}
	if err := eng.Ingest(low); err != nil {
		t.Fatal(err)
	}
	if got := lookupSet(t, eng, "idx", StringValue("HIGH")); len(got) != 1 {
		t.Fatalf("低时间戳迟到事件不得覆盖，得到 %v", got)
	}
}

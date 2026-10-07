package ontology

import (
	"errors"
	"testing"
)

// 目标实例同时承载多个不同基数约束（不同链接类型方向）。
// 单次更新必须对涉及的每一个约束分别重新校验：
//   - 全部满足才提交；
//   - 任意一个不满足即整单拒绝，且逐个列出违例依据，不混同为一个笼统结论。
func TestMultipleConstraintsAllCheckedSeparately(t *testing.T) {
	store := NewStore()
	store.AddObject("o")
	store.AddCardinality("o", Cardinality{LinkType: "ownedBy", Direction: Outgoing, Max: 1})
	store.AddCardinality("o", Cardinality{LinkType: "tagged", Direction: Incoming, Max: 2})
	submitter := NewSubmitter(store, RetryPolicy{MaxAttempts: 1})

	// 同一请求同时触及两个约束：out 侧将达 2（超限），in 侧将达 1（合法）。
	// 先预置 out 侧已有 1 个关联。
	_, err := submitter.Submit(Change{ObjectID: "o", Ops: []LinkOp{
		{LinkType: "ownedBy", Direction: Outgoing, OtherID: "owner-a", Add: true},
	}})
	if err != nil {
		t.Fatal(err)
	}

	_, err = submitter.Submit(Change{ObjectID: "o", BaseVersion: 1, Ops: []LinkOp{
		{LinkType: "ownedBy", Direction: Outgoing, OtherID: "owner-b", Add: true}, // 2 > 1
		{LinkType: "tagged", Direction: Incoming, OtherID: "tag-1", Add: true},    // 1 <= 2
	}})
	var ce *CardinalityError
	if !errors.As(err, &ce) {
		t.Fatalf("want cardinality error, got %v", err)
	}
	if len(ce.Violations) != 1 {
		t.Fatalf("exactly one constraint must be reported, got %+v", ce.Violations)
	}
	v := ce.Violations[0]
	if v.LinkType != "ownedBy" || v.Direction != Outgoing ||
		v.Current != 1 || v.Projected != 2 || v.Max != 1 {
		t.Fatalf("violation basis is not per-constraint: %+v", v)
	}

	final := store.Snapshot("o")
	if final.Links[keyOf("tagged", Incoming)] != nil && len(final.Links[keyOf("tagged", Incoming)]) != 0 {
		t.Fatal("legal-side link must not be committed when another constraint fails")
	}
	if final.Version != 1 {
		t.Fatal("version must not advance on rejected multi-constraint request")
	}

	// 两个约束同时超限：依据必须分别给出，不允许只报一个。
	_, err = submitter.Submit(Change{ObjectID: "o", BaseVersion: 1, Ops: []LinkOp{
		{LinkType: "ownedBy", Direction: Outgoing, OtherID: "owner-b", Add: true},
		{LinkType: "tagged", Direction: Incoming, OtherID: "t1", Add: true},
		{LinkType: "tagged", Direction: Incoming, OtherID: "t2", Add: true},
		{LinkType: "tagged", Direction: Incoming, OtherID: "t3", Add: true}, // 3 > 2
	}})
	if !errors.As(err, &ce) {
		t.Fatalf("want cardinality error, got %v", err)
	}
	if len(ce.Violations) != 2 {
		t.Fatalf("both constraints must be reported separately, got %+v", ce.Violations)
	}
	keys := map[string]bool{}
	for _, viol := range ce.Violations {
		keys[keyOf(viol.LinkType, viol.Direction)] = true
	}
	if !keys[keyOf("ownedBy", Outgoing)] || !keys[keyOf("tagged", Incoming)] {
		t.Fatalf("missing distinct constraint keys: %v", keys)
	}

	// 两个约束同时满足：整单一次性提交，版本只推进一次。
	res, err := submitter.Submit(Change{ObjectID: "o", BaseVersion: 1, Ops: []LinkOp{
		{LinkType: "tagged", Direction: Incoming, OtherID: "t1", Add: true},
		{LinkType: "tagged", Direction: Incoming, OtherID: "t2", Add: true},
	}})
	if err != nil || !res.Committed {
		t.Fatalf("both constraints satisfied must commit: %v", err)
	}
	final = store.Snapshot("o")
	if final.Counts[keyOf("tagged", Incoming)] != 2 {
		t.Fatalf("incoming count = %d, want 2", final.Counts[keyOf("tagged", Incoming)])
	}
}

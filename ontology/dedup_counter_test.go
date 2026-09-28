package ontology

import (
	"reflect"
	"testing"
)

func logResult(t *testing.T, c *DedupCounter, changes []Change, res ApplyResult) {
	t.Helper()
	t.Logf("input=%s\n%s\nview=%s", FormatChanges(changes), FormatResult(res), FormatView(c.View()))
}

func TestMultipleInsertsAndRetracts(t *testing.T) {
	c := NewDedupCounter(100)

	// 同一值插入三次：多重性 3，去重计数 1。
	inserts := []Change{
		{Group: "g", Value: "a", Kind: KindInsert},
		{Group: "g", Value: "a", Kind: KindInsert},
		{Group: "g", Value: "a", Kind: KindInsert},
	}
	res := c.Apply(inserts)
	logResult(t, c, inserts, res)
	if !res.Accepted || !reflect.DeepEqual(res.Changes, []GroupDelta{{Group: "g", Before: 0, After: 1, Delta: 1}}) {
		t.Fatalf("unexpected result: %+v", res)
	}
	if c.Multiplicity("g", "a") != 3 || c.View()["g"] != 1 {
		t.Fatalf("unexpected state: mult=%d view=%v", c.Multiplicity("g", "a"), c.View())
	}

	// 撤回两次：多重性 3 -> 1，仍为正，去重计数不变。
	partialRetracts := []Change{
		{Group: "g", Value: "a", Kind: KindRetract},
		{Group: "g", Value: "a", Kind: KindRetract},
	}
	res = c.Apply(partialRetracts)
	logResult(t, c, partialRetracts, res)
	if res.Changes[0].Delta != 0 || c.Multiplicity("g", "a") != 1 || c.View()["g"] != 1 {
		t.Fatalf("unexpected state after partial retract: %+v mult=%d", res, c.Multiplicity("g", "a"))
	}

	// 撤回最后一份：多重性 1 -> 0，去重计数 1 -> 0，空组从视图消失。
	finalRetract := []Change{{Group: "g", Value: "a", Kind: KindRetract}}
	res = c.Apply(finalRetract)
	logResult(t, c, finalRetract, res)
	if !reflect.DeepEqual(res.Changes, []GroupDelta{{Group: "g", Before: 1, After: 0, Delta: -1}}) {
		t.Fatalf("unexpected result: %+v", res)
	}
	if _, ok := c.View()["g"]; ok {
		t.Fatalf("empty group should be absent from view: %v", c.View())
	}
}

func TestInsertThenRetractWithinBatch(t *testing.T) {
	c := NewDedupCounter(100)

	// 批内先增后删：x 插入再撤回，净多重性 0，去重计数净变化 0。
	res := c.Apply([]Change{
		{Group: "g", Value: "x", Kind: KindInsert},
		{Group: "g", Value: "x", Kind: KindRetract},
	})
	logResult(t, c, []Change{
		{Group: "g", Value: "x", Kind: KindInsert},
		{Group: "g", Value: "x", Kind: KindRetract},
	}, res)
	if !res.Accepted || res.Changes[0].Delta != 0 {
		t.Fatalf("net-zero batch should accept with delta 0: %+v", res)
	}
	if len(c.View()) != 0 {
		t.Fatalf("view should be empty: %v", c.View())
	}

	// 已有多重性 1 时，批内再插入一份后撤回一份：2 -> 1，计数保持 1。
	c.Apply([]Change{{Group: "g", Value: "y", Kind: KindInsert}})
	res = c.Apply([]Change{
		{Group: "g", Value: "y", Kind: KindInsert},
		{Group: "g", Value: "y", Kind: KindRetract},
	})
	if res.Changes[0].Before != 1 || res.Changes[0].After != 1 || c.Multiplicity("g", "y") != 1 {
		t.Fatalf("unexpected: %+v mult=%d", res, c.Multiplicity("g", "y"))
	}
}

func TestRetractThenInsertWithinBatchRejected(t *testing.T) {
	c := NewDedupCounter(100)
	changes := []Change{
		{Group: "g", Value: "z", Kind: KindRetract},
		{Group: "g", Value: "z", Kind: KindInsert},
	}
	res := c.Apply(changes)
	logResult(t, c, changes, res)
	if res.Accepted || res.Reason != ReasonRetractZero || res.EntryIndex != 0 {
		t.Fatalf("expected retract_zero at index 0, got %+v", res)
	}
	if len(c.View()) != 0 {
		t.Fatalf("rejected batch must not change view: %v", c.View())
	}
}

func TestRetractValidationUsesWithinBatchState(t *testing.T) {
	c := NewDedupCounter(100)
	// 批前 a 多重性为 1；批内先撤回一份（变 0），再撤回一份 -> 拒绝整批。
	c.Apply([]Change{{Group: "g", Value: "a", Kind: KindInsert}})
	res := c.Apply([]Change{
		{Group: "g", Value: "a", Kind: KindRetract},
		{Group: "g", Value: "a", Kind: KindRetract},
	})
	if res.Accepted || res.Reason != ReasonRetractZero || res.EntryIndex != 1 {
		t.Fatalf("expected retract_zero at index 1, got %+v", res)
	}
	if c.Multiplicity("g", "a") != 1 || c.View()["g"] != 1 {
		t.Fatalf("rejected batch must not change state: mult=%d view=%v", c.Multiplicity("g", "a"), c.View())
	}
}

func TestInvalidInputs(t *testing.T) {
	cases := []struct {
		name    string
		max     int
		changes []Change
		reason  RejectReason
		idx     int
	}{
		{"too many entries", 1, []Change{
			{Group: "g", Value: "a", Kind: KindInsert},
			{Group: "g", Value: "b", Kind: KindInsert},
		}, ReasonTooManyEntries, -1},
		{"empty group", 100, []Change{{Group: "", Value: "a", Kind: KindInsert}}, ReasonEmptyGroup, 0},
		{"empty value", 100, []Change{{Group: "g", Value: "", Kind: KindInsert}}, ReasonEmptyValue, 0},
		{"invalid kind zero", 100, []Change{{Group: "g", Value: "a", Kind: 0}}, ReasonInvalidKind, 0},
		{"invalid kind other", 100, []Change{{Group: "g", Value: "a", Kind: ChangeKind(42)}}, ReasonInvalidKind, 0},
		{"retract absent", 100, []Change{{Group: "g", Value: "a", Kind: KindRetract}}, ReasonRetractZero, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := NewDedupCounter(tc.max)
			res := c.Apply(tc.changes)
			logResult(t, c, tc.changes, res)
			if res.Accepted || res.Reason != tc.reason || res.EntryIndex != tc.idx {
				t.Fatalf("got %+v, want reason=%s idx=%d", res, tc.reason, tc.idx)
			}
		})
	}
}

func TestValidationOrderReportsFirstOffender(t *testing.T) {
	c := NewDedupCounter(100)
	// 下标 0 合法且生效；下标 1 空值优先于其后的撤回零值被报告。
	res := c.Apply([]Change{
		{Group: "g", Value: "a", Kind: KindInsert},
		{Group: "g", Value: "", Kind: KindRetract},
		{Group: "g", Value: "ghost", Kind: KindRetract},
	})
	if res.Reason != ReasonEmptyValue || res.EntryIndex != 1 {
		t.Fatalf("got %+v", res)
	}
	if len(c.View()) != 0 {
		t.Fatalf("rejected batch must not partially apply: %v", c.View())
	}
}

func TestDeltasSortedByGroupAndIndependentGroups(t *testing.T) {
	c := NewDedupCounter(100)
	res := c.Apply([]Change{
		{Group: "b", Value: "1", Kind: KindInsert},
		{Group: "a", Value: "1", Kind: KindInsert},
		{Group: "a", Value: "2", Kind: KindInsert},
	})
	var groups []string
	for _, d := range res.Changes {
		groups = append(groups, d.Group)
	}
	want := []string{"a", "b"}
	if !reflect.DeepEqual(groups, want) {
		t.Fatalf("delta groups = %v, want %v", groups, want)
	}
	if res.Changes[0].After != 2 || res.Changes[1].After != 1 {
		t.Fatalf("unexpected deltas: %+v", res.Changes)
	}
}

func TestViewIsSnapshot(t *testing.T) {
	c := NewDedupCounter(100)
	c.Apply([]Change{{Group: "g", Value: "a", Kind: KindInsert}})
	v := c.View()
	v["g"] = 99
	v["injected"] = 1
	if c.View()["g"] != 1 {
		t.Fatal("mutating returned view must not affect counter")
	}
}

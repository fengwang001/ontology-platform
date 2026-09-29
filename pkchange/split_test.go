package pkchange

import (
	"reflect"
	"testing"
)

func r(v ...string) Row {
	row := Row{}
	for i := 0; i+1 < len(v); i += 2 {
		row[v[i]] = v[i+1]
	}
	return row
}

func kinds(events []Event) []EventKind {
	out := make([]EventKind, len(events))
	for i, ev := range events {
		out[i] = ev.Kind
	}
	return out
}

func keys(events []Event) []string {
	out := make([]string, len(events))
	for i, ev := range events {
		out[i] = ev.Key
	}
	return out
}

func TestSplitBasicOps(t *testing.T) {
	snapshot := map[string]Row{"a": r("v", "1"), "b": r("v", "2")}
	changes := []Change{
		{Op: OpInsert, Key: "c", Data: r("v", "3")},
		{Op: OpUpdate, Key: "a", NewKey: "a", Data: r("v", "11")},
		{Op: OpDelete, Key: "b"},
	}

	events, err := Split(snapshot, changes, 0, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if want := []EventKind{EventWrite, EventWrite, EventDelete}; !reflect.DeepEqual(kinds(events), want) {
		t.Fatalf("event kinds = %v, want %v", kinds(events), want)
	}
	if want := []string{"c", "a", "b"}; !reflect.DeepEqual(keys(events), want) {
		t.Fatalf("event keys = %v, want %v", keys(events), want)
	}

	// 源快照不被 Split 修改。
	if !reflect.DeepEqual(snapshot["a"], r("v", "1")) {
		t.Fatalf("snapshot mutated: %v", snapshot["a"])
	}
	if got := events[1].Data["v"]; got != "11" {
		t.Fatalf("update data = %q, want 11", got)
	}
}

func TestUpdateSameKeyProducesNoDelete(t *testing.T) {
	events, err := Split(
		map[string]Row{"k": r("v", "1")},
		[]Change{{Op: OpUpdate, Key: "k", Data: r("v", "2")}},
		0, nil,
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(events) != 1 || events[0].Kind != EventWrite {
		t.Fatalf("same-key update must produce exactly one write, got %#v", events)
	}
}

func TestUpdateKeyChangeProducesDeleteThenWrite(t *testing.T) {
	events, err := Split(
		map[string]Row{"old": r("v", "1")},
		[]Change{{Op: OpUpdate, Key: "old", NewKey: "new", Data: r("v", "1")}},
		0, nil,
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if want := []EventKind{EventDelete, EventWrite}; !reflect.DeepEqual(kinds(events), want) {
		t.Fatalf("kinds = %v, want %v", kinds(events), want)
	}
	if want := []string{"old", "new"}; !reflect.DeepEqual(keys(events), want) {
		t.Fatalf("keys = %v, want %v", keys(events), want)
	}
}

// 主键链式变更：a -> b -> c 在同一批内必须按投影状态逐条通过。
func TestSplitChainedKeyChanges(t *testing.T) {
	snapshot := map[string]Row{"a": r("v", "1"), "b": r("v", "2")}
	changes := []Change{
		{Op: OpUpdate, Key: "a", NewKey: "a1", Data: r("v", "1")},
		{Op: OpDelete, Key: "b"},
		{Op: OpUpdate, Key: "a1", NewKey: "a2", Data: r("v", "1m")},
		{Op: OpInsert, Key: "b", Data: r("v", "22")}, // b 在本批内已删除，可重新插入
	}

	events, err := Split(snapshot, changes, 0, nil)
	if err != nil {
		t.Fatalf("chained split rejected: %v", err)
	}

	wantKeys := []string{"a", "a1", "b", "a1", "a2", "b"}
	wantKinds := []EventKind{EventDelete, EventWrite, EventDelete, EventDelete, EventWrite, EventWrite}
	if !reflect.DeepEqual(keys(events), wantKeys) {
		t.Fatalf("keys = %v, want %v", keys(events), wantKeys)
	}
	if !reflect.DeepEqual(kinds(events), wantKinds) {
		t.Fatalf("kinds = %v, want %v", kinds(events), wantKinds)
	}

	merged := Merge(events)
	// 同一键只保留最后一条；a1 的最后事件是 delete。
	last := map[string]EventKind{}
	for _, ev := range merged {
		last[ev.Key] = ev.Kind
	}
	if last["a"] != EventDelete || last["a1"] != EventDelete ||
		last["a2"] != EventWrite || last["b"] != EventWrite {
		t.Fatalf("unexpected merged result: %v", last)
	}
}

func TestSplitRejections(t *testing.T) {
	cases := []struct {
		name     string
		snapshot map[string]Row
		changes  []Change
		limit    int
		reason   RejectReason
		index    int
	}{
		{
			name:     "insert invalid key",
			snapshot: map[string]Row{},
			changes:  []Change{{Op: OpInsert, Key: "  "}},
			reason:   ReasonInvalidKey,
			index:    0,
		},
		{
			name:     "insert existing key",
			snapshot: map[string]Row{"k": r("v", "1")},
			changes:  []Change{{Op: OpInsert, Key: "k"}},
			reason:   ReasonKeyExists,
			index:    0,
		},
		{
			name:     "insert key already inserted in batch",
			snapshot: map[string]Row{},
			changes: []Change{
				{Op: OpInsert, Key: "k", Data: r("v", "1")},
				{Op: OpInsert, Key: "k", Data: r("v", "2")},
			},
			reason: ReasonKeyExists,
			index:  1,
		},
		{
			name:     "delete missing key",
			snapshot: map[string]Row{},
			changes:  []Change{{Op: OpDelete, Key: "k"}},
			reason:   ReasonKeyNotFound,
			index:    0,
		},
		{
			name:     "delete twice in batch",
			snapshot: map[string]Row{"k": r("v", "1")},
			changes: []Change{
				{Op: OpDelete, Key: "k"},
				{Op: OpDelete, Key: "k"},
			},
			reason: ReasonKeyNotFound,
			index:  1,
		},
		{
			name:     "update missing old key",
			snapshot: map[string]Row{},
			changes:  []Change{{Op: OpUpdate, Key: "k", NewKey: "k2"}},
			reason:   ReasonKeyNotFound,
			index:    0,
		},
		{
			name:     "update to existing key",
			snapshot: map[string]Row{"a": r(), "b": r()},
			changes:  []Change{{Op: OpUpdate, Key: "a", NewKey: "b"}},
			reason:   ReasonKeyExists,
			index:    0,
		},
		{
			name:     "update with invalid new key",
			snapshot: map[string]Row{"a": r()},
			changes:  []Change{{Op: OpUpdate, Key: "a", NewKey: "\x00"}},
			reason:   ReasonInvalidKey,
			index:    0,
		},
		{
			name:     "delete invalid key",
			snapshot: map[string]Row{},
			changes:  []Change{{Op: OpDelete, Key: ""}},
			reason:   ReasonInvalidKey,
			index:    0,
		},
		{
			name:     "batch too large",
			snapshot: map[string]Row{},
			changes: []Change{
				{Op: OpInsert, Key: "k1"},
				{Op: OpInsert, Key: "k2"},
			},
			limit:  1,
			reason: ReasonBatchTooLarge,
			index:  -1,
		},
		{
			name:     "unknown op",
			snapshot: map[string]Row{},
			changes:  []Change{{Op: Op(99), Key: "k"}},
			reason:   ReasonInvalidChange,
			index:    0,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			events, err := Split(tc.snapshot, tc.changes, tc.limit, nil)
			rj, ok := AsReject(err)
			if !ok {
				t.Fatalf("expected *RejectError, got %v (events=%v)", err, events)
			}
			if rj.Reason != tc.reason {
				t.Fatalf("reason = %q, want %q", rj.Reason, tc.reason)
			}
			if rj.Index != tc.index {
				t.Fatalf("index = %d, want %d", rj.Index, tc.index)
			}
			if events != nil {
				t.Fatalf("rejected batch must return no events, got %v", events)
			}
		})
	}
}

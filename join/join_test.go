package join

import (
	"testing"
)

func lOp(k Kind, id, key, val string) Op {
	return Op{Side: Left, Kind: k, Row: Row{ID: id, Key: key, Value: val}}
}

func rOp(k Kind, id, key, val string) Op {
	return Op{Side: Right, Kind: k, Row: Row{ID: id, Key: key, Value: val}}
}

func entryKey(e Entry) string { return e.LeftID + "|" + e.RightID }

// replayToView 把变更日志逐条应用到内存视图，模拟下游按序消费。
func replayToView(view map[string]Entry, entries []Entry) {
	for _, e := range entries {
		switch e.Kind {
		case Insert:
			view[entryKey(e)] = e
		case Delete:
			delete(view, entryKey(e))
		}
	}
}

func assertEntries(t *testing.T, got, want []Entry) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("entries 数量不符: got %+v want %+v", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("entries[%d] 不符:\n got %+v\nwant %+v", i, got[i], want[i])
		}
	}
}

// assertViewEqualsSnapshot 验证下游按日志重放后的视图与当前连接快照完全一致。
func assertViewEqualsSnapshot(t *testing.T, j *Joiner, view map[string]Entry) {
	t.Helper()
	snap := j.Snapshot()
	if len(view) != len(snap) {
		t.Fatalf("view rows=%d, snapshot rows=%d", len(view), len(snap))
	}
	for _, e := range snap {
		v, ok := view[entryKey(e)]
		if !ok {
			t.Fatalf("snapshot row %q missing in replayed view", entryKey(e))
		}
		if v != e {
			t.Fatalf("row %q mismatch: view=%+v snapshot=%+v", entryKey(e), v, e)
		}
	}
}

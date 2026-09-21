package ontology

import "testing"

func buildSnapshotSelector(t *testing.T) *Selector {
	t.Helper()
	s, err := New(testConfig(2))
	if err != nil {
		t.Fatal(err)
	}
	s.Add(map[string]any{"g": "b", "s": 1.0, "t": "x"})
	s.Add(map[string]any{"g": "a", "s": 2.0, "t": "y"})
	s.Add(map[string]any{"g": "a", "s": 3.0, "t": "z"})
	s.Add(map[string]any{"s": 4.0, "t": "m"}) // 缺失键组
	return s
}

func snapshotEqual(a, b []GroupSnapshot) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Key != b[i].Key || a[i].Skipped != b[i].Skipped || a[i].NaN != b[i].NaN {
			return false
		}
		if len(a[i].Rows) != len(b[i].Rows) {
			return false
		}
		for j := range a[i].Rows {
			ra, rb := a[i].Rows[j], b[i].Rows[j]
			if ra.Score != rb.Score || ra.Tie != rb.Tie || ra.ID() != rb.ID() {
				return false
			}
		}
	}
	return true
}

func TestSnapshotRepeatable(t *testing.T) {
	s := buildSnapshotSelector(t)
	first := s.Snapshot()
	for i := 0; i < 5; i++ {
		if !snapshotEqual(first, s.Snapshot()) {
			t.Fatalf("snapshot %d differs from first", i)
		}
	}
}

func TestSnapshotGroupOrder(t *testing.T) {
	s := buildSnapshotSelector(t)
	snap := s.Snapshot()
	if len(snap) != 3 {
		t.Fatalf("want 3 groups, got %d", len(snap))
	}
	// 缺失组在最前，普通键按字典序。
	if snap[0].Key.Class != KeyMissing {
		t.Fatalf("group 0: want missing-key group, got %v", snap[0].Key.Class)
	}
	if snap[1].Key.Value != "a" || snap[2].Key.Value != "b" {
		t.Fatalf("groups not sorted: %q, %q", snap[1].Key.Value, snap[2].Key.Value)
	}
}

func TestSnapshotIsolation(t *testing.T) {
	s := buildSnapshotSelector(t)
	before := s.Snapshot()

	// 破坏返回值的切片与 map，内部状态不得受影响。
	snap := s.Snapshot()
	snap[0].Rows[0].Score = -999
	snap[0].Rows[0].Tie = "corrupted"
	snap[0].Rows[0].Fields["g"] = "corrupted"
	delete(snap[0].Rows[0].Fields, "s")
	snap[1].Rows = snap[1].Rows[:0]

	after := s.Snapshot()
	if !snapshotEqual(before, after) {
		t.Fatal("mutating snapshot output affected internal state")
	}
}

func TestAddCopiesInputRow(t *testing.T) {
	s, err := New(testConfig(1))
	if err != nil {
		t.Fatal(err)
	}
	row := map[string]any{"g": "a", "s": 1.0, "t": "x"}
	s.Add(row)
	// 调用方事后修改输入 map，不得影响已持有的行。
	row["s"] = 999.0
	row["g"] = "other"

	snap := s.Snapshot()
	if len(snap) != 1 || snap[0].Key.Value != "a" {
		t.Fatalf("internal state changed by caller mutation: %+v", snap)
	}
	if snap[0].Rows[0].Score != 1.0 {
		t.Fatalf("score mutated to %v", snap[0].Rows[0].Score)
	}
}

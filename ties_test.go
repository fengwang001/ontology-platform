package ontology

import (
	"math/rand"
	"testing"
)

// snapshotIDs 提取快照中指定组的行内容标识序列，便于逐元素比较。
func snapshotIDs(snap []GroupSnapshot, keyValue string) []string {
	for _, gs := range snap {
		if gs.Key.Class == KeyValue && gs.Key.Value == keyValue {
			ids := make([]string, len(gs.Rows))
			for i := range gs.Rows {
				ids[i] = gs.Rows[i].ID()
			}
			return ids
		}
	}
	return nil
}

func TestTieBreakDeterministicUnderShuffle(t *testing.T) {
	// 一组分数与 tie 列全相同的行，只能靠内容派生标识区分。
	base := []map[string]any{
		{"g": "a", "s": 7.0, "t": "same", "payload": "alpha"},
		{"g": "a", "s": 7.0, "t": "same", "payload": "beta"},
		{"g": "a", "s": 7.0, "t": "same", "payload": "gamma"},
		{"g": "a", "s": 7.0, "t": "same", "payload": "delta"},
		{"g": "a", "s": 7.0, "t": "same", "payload": "epsilon"},
		{"g": "a", "s": 7.0, "t": "same", "payload": "zeta"},
	}

	var reference []string
	for trial := 0; trial < 8; trial++ {
		rows := make([]map[string]any, len(base))
		copy(rows, base)
		rand.New(rand.NewSource(int64(trial))).Shuffle(len(rows), func(i, j int) {
			rows[i], rows[j] = rows[j], rows[i]
		})

		s, err := New(testConfig(3))
		if err != nil {
			t.Fatal(err)
		}
		for _, r := range rows {
			s.Add(r)
		}
		ids := snapshotIDs(s.Snapshot(), "a")
		if len(ids) != 3 {
			t.Fatalf("trial %d: want 3 rows, got %d", trial, len(ids))
		}
		if trial == 0 {
			reference = ids
			continue
		}
		for i := range reference {
			if ids[i] != reference[i] {
				t.Fatalf("trial %d: row %d differs: %q vs %q", trial, i, ids[i], reference[i])
			}
		}
	}
}

func TestTieBreakByTieColumnThenContentID(t *testing.T) {
	s, err := New(testConfig(3))
	if err != nil {
		t.Fatal(err)
	}
	// 分数全相同：tie 列升序优先；tie 也相同再按内容标识。
	s.Add(map[string]any{"g": "a", "s": 1.0, "t": "b", "payload": "p1"})
	s.Add(map[string]any{"g": "a", "s": 1.0, "t": "a", "payload": "p2"})
	s.Add(map[string]any{"g": "a", "s": 1.0, "t": "b", "payload": "p3"})
	s.Add(map[string]any{"g": "a", "s": 1.0, "t": "c", "payload": "p4"})

	rows := s.Snapshot()[0].Rows
	if len(rows) != 3 {
		t.Fatalf("want 3 rows, got %d", len(rows))
	}
	if rows[0].Tie != "a" {
		t.Fatalf("row 0: want tie \"a\", got %q", rows[0].Tie)
	}
	if rows[1].Tie != "b" || rows[2].Tie != "b" {
		t.Fatalf("rows 1-2: want tie \"b\", got %q,%q", rows[1].Tie, rows[2].Tie)
	}
	// tie 相同的两行按内容标识升序。
	if rows[1].ID() >= rows[2].ID() {
		t.Fatalf("rows 1-2 not ordered by content ID: %q >= %q", rows[1].ID(), rows[2].ID())
	}
	// 内容标识必须稳定：同内容同标识。
	a := Row{Fields: map[string]any{"x": 1, "y": "z"}}
	b := Row{Fields: map[string]any{"y": "z", "x": 1}}
	if a.ID() != b.ID() {
		t.Fatal("content ID depends on map insertion/iteration order")
	}
}

func TestScoreDescendingPrimary(t *testing.T) {
	s, err := New(testConfig(2))
	if err != nil {
		t.Fatal(err)
	}
	s.Add(map[string]any{"g": "a", "s": 1.0, "t": "a"})
	s.Add(map[string]any{"g": "a", "s": 3.0, "t": "z"})
	s.Add(map[string]any{"g": "a", "s": 2.0, "t": "m"})

	rows := s.Snapshot()[0].Rows
	if rows[0].Score != 3.0 || rows[1].Score != 2.0 {
		t.Fatalf("want scores [3 2], got [%v %v]", rows[0].Score, rows[1].Score)
	}
}

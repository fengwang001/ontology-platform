package provenance

import "testing"

func mustWriteObject(t *testing.T, s *Store, id ObjectID, w Time, v Interval, ex bool) {
	t.Helper()
	if err := s.WriteObject(id, w, v, ex); err != nil {
		t.Fatalf("WriteObject(%s): %v", id, err)
	}
}

func mustWriteLink(t *testing.T, s *Store, id LinkID, w Time, v Interval, a, b ObjectID, ex bool) {
	t.Helper()
	if err := s.WriteLink(id, w, v, a, b, ex); err != nil {
		t.Fatalf("WriteLink(%s): %v", id, err)
	}
}

func joinIDs(ids []ObjectID) string {
	out := ""
	for i, id := range ids {
		if i > 0 {
			out += ">"
		}
		out += string(id)
	}
	return out
}

// buildDemoGraph: A -> B -> C -> D 主链，另有 A -> X 死链（X 在 150 不可见），
// 以及一条在写入时间 60 才落定的 A -> Y 链接。
func buildDemoGraph(t *testing.T) *Store {
	t.Helper()
	s := NewStore()
	mustWriteObject(t, s, "A", 10, Interval{0, 1000}, true)
	mustWriteObject(t, s, "B", 10, Interval{0, 1000}, true)
	mustWriteObject(t, s, "C", 10, Interval{0, 1000}, true)
	mustWriteObject(t, s, "D", 10, Interval{0, 1000}, true)
	mustWriteObject(t, s, "X", 10, Interval{0, 150}, true)
	mustWriteObject(t, s, "Y", 10, Interval{0, 1000}, true)
	mustWriteLink(t, s, "L1", 10, Interval{0, 1000}, "A", "B", true)
	mustWriteLink(t, s, "L2", 10, Interval{0, 1000}, "B", "C", true)
	mustWriteLink(t, s, "L3", 10, Interval{0, 1000}, "C", "D", true)
	mustWriteLink(t, s, "LX", 10, Interval{0, 1000}, "A", "X", true)
	mustWriteLink(t, s, "LY", 60, Interval{0, 1000}, "A", "Y", true)
	return s
}

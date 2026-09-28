package ontology

import "testing"

func TestRowsEqual(t *testing.T) {
	cases := []struct {
		name string
		a, b Row
		want bool
	}{
		{"两行均空", Row{}, Row{}, true},
		{"两 nil", nil, nil, true},
		{"完全相同", Row{"a": "1", "b": "2"}, Row{"a": "1", "b": "2"}, true},
		{"值不同", Row{"a": "1"}, Row{"a": "2"}, false},
		{"缺列与空串不相等", Row{"a": ""}, Row{}, false},
		{"空串与缺列不相等(反向)", Row{}, Row{"a": ""}, false},
		{"多列缺一列", Row{"a": "1", "b": "2"}, Row{"a": "1"}, false},
		{"nil 与空行", nil, Row{}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := rowsEqual(c.a, c.b); got != c.want {
				t.Fatalf("rowsEqual(%v,%v)=%v want %v", c.a, c.b, got, c.want)
			}
		})
	}
}

func TestApplyInsertUpdateDelete(t *testing.T) {
	s := NewStore(10)

	// insert
	res, err := s.Apply([]Event{
		{Seq: 1, Key: "k1", Op: OpInsert, Before: nil, After: Row{"name": "a", "v": "1"}},
	})
	if err != nil {
		t.Fatalf("insert: %v", err)
	}
	if res.Applied != 1 || len(res.Conflicts) != 0 || res.LastSeq != 1 {
		t.Fatalf("insert result = %+v", res)
	}
	if r, ok := s.Snapshot().Get("k1"); !ok || r["name"] != "a" || r["v"] != "1" {
		t.Fatalf("after insert row = %v ok=%v", r, ok)
	}

	// update，前像与当前行完全一致
	res, err = s.Apply([]Event{
		{Seq: 2, Key: "k1", Op: OpUpdate,
			Before: Row{"name": "a", "v": "1"},
			After:  Row{"name": "a", "v": "2"}},
	})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if res.Applied != 1 {
		t.Fatalf("update applied = %d", res.Applied)
	}
	if r, _ := s.Snapshot().Get("k1"); r["v"] != "2" {
		t.Fatalf("after update v = %q", r["v"])
	}

	// delete
	res, err = s.Apply([]Event{
		{Seq: 3, Key: "k1", Op: OpDelete, Before: Row{"name": "a", "v": "2"}, After: nil},
	})
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	if res.Applied != 1 {
		t.Fatalf("delete applied = %d", res.Applied)
	}
	if _, ok := s.Snapshot().Get("k1"); ok {
		t.Fatal("row still exists after delete")
	}
	if s.LastSeq() != 3 {
		t.Fatalf("LastSeq = %d, want 3", s.LastSeq())
	}
}

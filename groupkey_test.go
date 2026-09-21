package ontology

import "testing"

func TestNullishGroupKeysFormThreeDistinctGroups(t *testing.T) {
	s, err := New(testConfig(2))
	if err != nil {
		t.Fatal(err)
	}
	s.Add(map[string]any{"s": 1.0, "t": "x"})              // missing
	s.Add(map[string]any{"g": nil, "s": 2.0, "t": "x"})    // null
	s.Add(map[string]any{"g": "", "s": 3.0, "t": "x"})     // empty string
	s.Add(map[string]any{"g": "real", "s": 4.0, "t": "x"}) // ordinary

	snap := s.Snapshot()
	if len(snap) != 4 {
		t.Fatalf("want 4 groups, got %d", len(snap))
	}
	// Order: Missing < Null < Value("" ) < Value("real").
	wantKinds := []KeyKind{KeyMissing, KeyNull, KeyValue, KeyValue}
	wantVals := []string{"", "", "", "real"}
	for i := range snap {
		if snap[i].Group.Kind != wantKinds[i] || snap[i].Group.Value != wantVals[i] {
			t.Fatalf("group %d: want (%v,%q), got %v", i, wantKinds[i], wantVals[i], snap[i].Group)
		}
	}
	// Each null-ish group is individually addressable.
	for _, key := range []GroupKey{
		{Kind: KeyMissing},
		{Kind: KeyNull},
		{Kind: KeyValue, Value: ""},
	} {
		if _, _, ok := s.Skips(key); !ok {
			t.Fatalf("group %v must exist and be distinguishable", key)
		}
	}
}

func TestGroupKeyStringFormsDiffer(t *testing.T) {
	forms := map[string]bool{}
	for _, k := range []GroupKey{
		{Kind: KeyMissing},
		{Kind: KeyNull},
		{Kind: KeyValue, Value: ""},
		{Kind: KeyValue, Value: "a"},
	} {
		s := k.String()
		if forms[s] {
			t.Fatalf("duplicate String() form %q", s)
		}
		forms[s] = true
	}
}

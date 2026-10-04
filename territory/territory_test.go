package territory

import (
	"errors"
	"testing"
)

func exampleTree(t *testing.T) *Tree {
	t.Helper()
	tr, err := New(map[string][]string{
		"WORLD": {"EU", "AS"},
		"EU":    {"FR", "DE"},
		"AS":    {"JP", "KR"},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return tr
}

func TestNodeRangesAndLeaves(t *testing.T) {
	tr := exampleTree(t)
	want := map[string]Segment{
		"WORLD": {0, 4},
		"EU":    {0, 2},
		"AS":    {2, 4},
		"FR":    {0, 1},
		"DE":    {1, 2},
		"JP":    {2, 3},
		"KR":    {3, 4},
	}
	if tr.Leaves() != 4 {
		t.Fatalf("Leaves = %d, want 4", tr.Leaves())
	}
	for code, w := range want {
		if got, ok := tr.NodeRange(code); !ok || got != w {
			t.Errorf("NodeRange(%q) = %v,%v want %v", code, got, ok, w)
		}
		if w.Hi-w.Lo == 1 {
			if !tr.IsLeaf(code) {
				t.Errorf("%q should be leaf", code)
			}
		} else if tr.IsLeaf(code) {
			t.Errorf("%q should not be leaf", code)
		}
	}
	if tr.Has("MARS") {
		t.Errorf("MARS should be unknown")
	}
}

func TestDescendant(t *testing.T) {
	tr := exampleTree(t)
	cases := []struct {
		anc, desc string
		want      bool
	}{
		{"WORLD", "FR", true},
		{"EU", "DE", true},
		{"FR", "WORLD", false},
		{"FR", "FR", false},
		{"EU", "JP", false},
		{"EU", "MARS", false},
	}
	for _, c := range cases {
		if got := tr.IsProperDescendant(c.anc, c.desc); got != c.want {
			t.Errorf("IsProperDescendant(%q,%q)=%v want %v", c.anc, c.desc, got, c.want)
		}
	}
}

func TestCover(t *testing.T) {
	tr := exampleTree(t)
	cases := []struct {
		name    string
		node    string
		exclude []string
		want    []Segment
		wantErr error
	}{
		{"no exclude", "EU", nil, []Segment{{0, 2}}, nil},
		{"EU minus FR", "EU", []string{"FR"}, []Segment{{1, 2}}, nil},
		{"EU minus DE", "EU", []string{"DE"}, []Segment{{0, 1}}, nil},
		{"EU minus both -> empty", "EU", []string{"FR", "DE"}, nil, ErrEmptyCover},
		{"WORLD minus EU", "WORLD", []string{"EU"}, []Segment{{2, 4}}, nil},
		{"unknown node", "MARS", nil, nil, ErrUnknownNode},
		{"unknown exclude", "WORLD", []string{"MARS"}, nil, ErrUnknownNode},
	}
	for _, c := range cases {
		got, err := tr.Cover(c.node, c.exclude)
		if c.wantErr != nil {
			if !errors.Is(err, c.wantErr) {
				t.Errorf("%s: err=%v want %v", c.name, err, c.wantErr)
			}
			continue
		}
		if err != nil {
			t.Errorf("%s: unexpected err %v", c.name, err)
			continue
		}
		if len(got) != len(c.want) {
			t.Fatalf("%s: %v want %v", c.name, got, c.want)
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Errorf("%s: %v want %v", c.name, got, c.want)
			}
		}
	}
}

func TestOverlap(t *testing.T) {
	cases := []struct {
		name string
		a, b []Segment
		want bool
	}{
		{"touch endpoints", []Segment{{0, 1}}, []Segment{{1, 2}}, false},
		{"same", []Segment{{0, 2}}, []Segment{{0, 2}}, true},
		{"gap", []Segment{{0, 1}, {3, 4}}, []Segment{{1, 3}}, false},
		{"multi overlap", []Segment{{1, 2}, {4, 5}}, []Segment{{0, 2}}, true},
		{"empty", []Segment{{0, 4}}, nil, false},
	}
	for _, c := range cases {
		if got := Overlap(c.a, c.b); got != c.want {
			t.Errorf("%s: Overlap=%v want %v", c.name, got, c.want)
		}
	}
}

func TestInvalidTrees(t *testing.T) {
	bad := []map[string][]string{
		{"EU": {"FR"}},                                // 根不可达
		{"WORLD": {"EU"}, "EU": {"WORLD"}},            // 指回根
		{"WORLD": {"EU", "EU"}},                       // 重复子女
		{"WORLD": {"A"}, "A": {"B", "C"}, "X": {"A"}}, // A 双父
		{"WORLD": {""}},                               // 空代码
	}
	for i, m := range bad {
		if _, err := New(m); !errors.Is(err, ErrInvalidTree) {
			t.Errorf("bad[%d]: err=%v want ErrInvalidTree", i, err)
		}
	}
}

func TestDepthLimit(t *testing.T) {
	// 深度 6（根深度 0）非法。
	m := map[string][]string{"WORLD": {"A"}, "A": {"B"}, "B": {"C"}, "C": {"D"}, "D": {"E"}, "E": {"F"}}
	if _, err := New(m); !errors.Is(err, ErrInvalidTree) {
		t.Fatalf("depth 6: err=%v want ErrInvalidTree", err)
	}
	// 深度 5 合法。
	m2 := map[string][]string{"WORLD": {"A"}, "A": {"B"}, "B": {"C"}, "C": {"D"}, "D": {"E"}}
	if _, err := New(m2); err != nil {
		t.Fatalf("depth 5: %v", err)
	}
}

package typetree

import (
	"errors"
	"strings"
	"testing"
)

// TestAddType 表驱动覆盖：合法注册、收窄/放宽两个覆盖方向、
// 菱形三态、父类型缺失、重复注册、自环。
func TestAddType(t *testing.T) {
	base := []struct {
		name    string
		parents []string
		props   map[string]string
	}{
		{"Living", nil, nil},
		{"Animal", []string{"Living"}, map[string]string{"friend": "Animal"}},
		{"IdA", nil, map[string]string{"id": "string"}},
		{"IdB", nil, map[string]string{"id": "string"}},
		{"NumA", nil, map[string]string{"v": "number"}},
		{"StrB", nil, map[string]string{"v": "string"}},
	}
	cases := []struct {
		name    string
		parents []string
		props   map[string]string
		wantErr error // nil 表示应成功
	}{
		{"Dog", []string{"Animal"}, map[string]string{"friend": "Animal"}, nil},          // 相同
		{"Dog2", []string{"Animal"}, map[string]string{"friend": "Dog"}, nil},            // 收窄
		{"Wide", []string{"Animal"}, map[string]string{"friend": "Living"}, ErrOverride}, // 放宽
		{"MergeId", []string{"IdA", "IdB"}, nil, nil},                                    // 菱形：同类型合并
		{"MergePet", []string{"Animal", "Dog2"}, nil, nil},                               // 菱形：相容
		{"Clash", []string{"NumA", "StrB"}, nil, ErrConflict},                            // 菱形：冲突
		{"Orphan", []string{"Ghost"}, nil, ErrUnknownParent},                             // 父类型不存在
		{"Animal", nil, nil, ErrDuplicate},                                               // 重复注册
		{"Self", []string{"Self"}, nil, ErrCycle},                                        // 自环
	}
	tr := New()
	for _, b := range base {
		if err := tr.AddType(b.name, b.parents, b.props); err != nil {
			t.Fatalf("setup %s: %v", b.name, err)
		}
	}
	for _, c := range cases {
		err := tr.AddType(c.name, c.parents, c.props)
		if c.wantErr == nil {
			if err != nil {
				t.Errorf("AddType(%s) = %v, want nil", c.name, err)
			}
			continue
		}
		if !errors.Is(err, c.wantErr) {
			t.Errorf("AddType(%s) = %v, want errors.Is %v", c.name, err, c.wantErr)
		}
	}
	if tr.conflictRejections != 1 {
		t.Errorf("conflictRejections = %d, want 1", tr.conflictRejections)
	}
	if tr.overrideRejections != 1 {
		t.Errorf("overrideRejections = %d, want 1", tr.overrideRejections)
	}
	if tr.cycleRejections != 1 {
		t.Errorf("cycleRejections = %d, want 1", tr.cycleRejections)
	}
}

// TestFindCycleShapes 用合成图遍历循环继承形态：自环、二元环、三元环、无环。
func TestFindCycleShapes(t *testing.T) {
	shapes := []struct {
		name    string
		edges   map[string][]string
		start   string
		wantLen int // 0 表示无环
	}{
		{"self-loop", map[string][]string{"A": {"A"}}, "A", 2},
		{"two-cycle", map[string][]string{"A": {"B"}, "B": {"A"}}, "A", 3},
		{"three-cycle", map[string][]string{"A": {"B"}, "B": {"C"}, "C": {"A"}}, "A", 4},
		{"dag", map[string][]string{"A": {"B", "C"}, "B": {"D"}, "C": {"D"}}, "A", 0},
	}
	for _, s := range shapes {
		types := map[string]*Type{}
		for n, ps := range s.edges {
			types[n] = &Type{Name: n, Parents: ps}
		}
		ring := findCycle(types, s.start, s.edges[s.start])
		if s.wantLen == 0 {
			if ring != nil {
				t.Errorf("%s: got ring %v, want nil", s.name, ring)
			}
			continue
		}
		if len(ring) != s.wantLen {
			t.Errorf("%s: ring %v len=%d, want %d", s.name, ring, len(ring), s.wantLen)
			continue
		}
		if ring[0] != ring[len(ring)-1] {
			t.Errorf("%s: ring %v not closed", s.name, ring)
		}
		if !strings.Contains(strings.Join(ring, " -> "), s.start) {
			t.Errorf("%s: ring %v missing start %s", s.name, ring, s.start)
		}
	}
}

// TestIsSubtype 遍历自反、传递、反向不成立三种关系。
func TestIsSubtype(t *testing.T) {
	tr := New()
	for _, d := range [][2]string{{"A", ""}, {"B", "A"}, {"C", "B"}} {
		var parents []string
		if d[1] != "" {
			parents = []string{d[1]}
		}
		if err := tr.AddType(d[0], parents, nil); err != nil {
			t.Fatal(err)
		}
	}
	cases := []struct {
		sub, super string
		want       bool
	}{
		{"C", "C", true}, {"C", "B", true}, {"C", "A", true},
		{"A", "C", false}, {"B", "C", false}, {"A", "B", false},
		{"string", "string", true}, {"string", "number", false},
	}
	for _, c := range cases {
		if got := tr.IsSubtype(c.sub, c.super); got != c.want {
			t.Errorf("IsSubtype(%s, %s) = %v, want %v", c.sub, c.super, got, c.want)
		}
	}
}

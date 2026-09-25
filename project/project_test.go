package project

import (
	"fmt"
	"sort"
	"strings"
	"testing"

	"ontology/access"
	"ontology/acl"
)

func newProjector(grants ...string) *Projector {
	pol := acl.NewPolicy()
	u := acl.User("u")
	for _, prop := range grants {
		pol.Grant(u, prop, acl.Read)
	}
	return NewProjector(access.NewEvaluator(pol), u)
}

// canonical 把对象序列化为确定字节串，用于逐字节比较投影结果。
func canonical(obj Object) string {
	keys := make([]string, 0, len(obj))
	for k := range obj {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, k := range keys {
		fmt.Fprintf(&b, "%s=%v;", k, obj[k])
	}
	return b.String()
}

// TestApplyDeterministic 同一主体、同一对象、任意输入遍历顺序，投影逐字节相同。
func TestApplyDeterministic(t *testing.T) {
	cases := []struct {
		name   string
		grants []string
		obj    Object
		want   string
	}{
		{"全部可见", []string{"a", "b"}, Object{"a": 1, "b": "x"}, "a=1;b=x;"},
		{"部分可见", []string{"a"}, Object{"a": 1, "b": "x", "c": true}, "a=1;"},
		{"全部不可见", nil, Object{"a": 1, "b": 2}, ""},
		{"空对象", []string{"a"}, Object{}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for round := 0; round < 20; round++ {
				p := newProjector(tc.grants...)
				if got := canonical(p.Apply(tc.obj)); got != tc.want {
					t.Fatalf("round %d: got %q, want %q", round, got, tc.want)
				}
			}
		})
	}
}

// TestRemovalNotZeroing 不可见属性被移除（键不存在），可见零值原样保留。
func TestRemovalNotZeroing(t *testing.T) {
	p := newProjector("keep")
	view := p.Apply(Object{"keep": 0, "drop": "x"})

	if _, ok := view["drop"]; ok {
		t.Fatal("不可见属性应被移除（键不存在），而非置零值")
	}
	v, ok := view["keep"]
	if !ok || v != 0 {
		t.Fatalf("可见零值应保留: ok=%v v=%v", ok, v)
	}
	if p.Removed() != 1 {
		t.Fatalf("Removed=%d, want 1", p.Removed())
	}
	if len(view) != 1 {
		t.Fatalf("len(view)=%d, want 1", len(view))
	}
}

// TestApplyDoesNotMutateInput 投影不修改输入对象。
func TestApplyDoesNotMutateInput(t *testing.T) {
	p := newProjector("a")
	obj := Object{"a": 1, "b": 2}
	p.Apply(obj)
	if len(obj) != 2 || obj["b"] != 2 {
		t.Fatalf("输入对象被修改: %v", obj)
	}
}

// TestApplyWithSchema 必填列被移除时降级为部分可见视图，而非非法对象。
func TestApplyWithSchema(t *testing.T) {
	cases := []struct {
		name        string
		grants      []string
		obj         Object
		required    []string
		wantPartial bool
		wantMissing []string
	}{
		{"必填列全部可见", []string{"a", "b"}, Object{"a": 1, "b": 2},
			[]string{"a", "b"}, false, nil},
		{"必填列被投影移除", []string{"a"}, Object{"a": 1, "b": 2},
			[]string{"a", "b"}, true, []string{"b"}},
		{"存储本就缺必填列不算投影降级", []string{"a"}, Object{"a": 1},
			[]string{"a", "b"}, false, nil},
		{"多个必填列被移除按升序列出", []string{"c"}, Object{"a": 1, "b": 2, "c": 3},
			[]string{"b", "a"}, true, []string{"a", "b"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := newProjector(tc.grants...)
			v := p.ApplyWithSchema(tc.obj, tc.required)
			if v.Partial != tc.wantPartial {
				t.Fatalf("Partial=%v, want %v", v.Partial, tc.wantPartial)
			}
			if strings.Join(v.Missing, ",") != strings.Join(tc.wantMissing, ",") {
				t.Fatalf("Missing=%v, want %v", v.Missing, tc.wantMissing)
			}
		})
	}
}

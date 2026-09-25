package props

import (
	"errors"
	"fmt"
	"testing"
)

// fakeGraph 是一个手工构造的 Graph，用于绕过 typetree 的注册期校验，
// 直接测试 Resolve/Merge 的菱形三态。
type fakeGraph struct {
	own     map[string]map[string]string
	parents map[string][]string
}

func (f *fakeGraph) OwnProps(name string) (map[string]string, bool) {
	m, ok := f.own[name]
	return m, ok
}

func (f *fakeGraph) Parents(name string) []string { return f.parents[name] }

func (f *fakeGraph) IsSubtype(sub, super string) bool {
	if sub == super {
		return true
	}
	seen := map[string]bool{sub: true}
	queue := []string{sub}
	for len(queue) > 0 {
		n := queue[0]
		queue = queue[1:]
		if n == super {
			return true
		}
		for _, p := range f.parents[n] {
			if !seen[p] {
				seen[p] = true
				queue = append(queue, p)
			}
		}
	}
	return false
}

// TestResolveDiamondThreeStates 循环遍历菱形三态：同类型合并、相容取窄、冲突报错。
func TestResolveDiamondThreeStates(t *testing.T) {
	cases := []struct {
		name     string
		aType    string
		bType    string
		wantType string // 空表示应报 ErrConflict
	}{
		{"same-type-merged", "string", "string", "string"},
		{"compatible-narrower-left", "Dog", "Animal", "Dog"},
		{"compatible-narrower-right", "Animal", "Dog", "Dog"},
		{"incompatible-conflict", "string", "number", ""},
	}
	for _, c := range cases {
		g := &fakeGraph{
			own: map[string]map[string]string{
				"Animal": {}, "Dog": {},
				"A": {"p": c.aType}, "B": {"p": c.bType}, "C": {},
			},
			parents: map[string][]string{"Dog": {"Animal"}, "C": {"A", "B"}},
		}
		got, err := Resolve(g, "C")
		if c.wantType == "" {
			if !errors.Is(err, ErrConflict) {
				t.Errorf("%s: err=%v, want ErrConflict", c.name, err)
			}
			continue
		}
		if err != nil {
			t.Errorf("%s: unexpected err %v", c.name, err)
			continue
		}
		if len(got) != 1 || got[0].Type != c.wantType {
			t.Errorf("%s: got %v, want single prop of type %s", c.name, got, c.wantType)
		}
	}
}

// TestResolveDeterminism 同一类型集按不同顺序注册（乱序的 fakeGraph 插入），
// Resolve 结果逐字节相同，且合并调用次数一致。
func TestResolveDeterminism(t *testing.T) {
	orders := [][]string{
		{"Root", "Mid", "Leaf", "Mix"},
		{"Mix", "Leaf", "Mid", "Root"},
		{"Leaf", "Root", "Mix", "Mid"},
	}
	decls := map[string]struct {
		parents []string
		own     map[string]string
	}{
		"Root": {nil, map[string]string{"a": "string", "b": "number"}},
		"Mid":  {[]string{"Root"}, map[string]string{"c": "bool"}},
		"Leaf": {[]string{"Mid"}, map[string]string{"a": "string"}},
		"Mix":  {[]string{"Leaf", "Mid"}, map[string]string{"d": "Dog"}},
	}
	var want string
	var wantCalls int
	for i, order := range orders {
		g := &fakeGraph{own: map[string]map[string]string{}, parents: map[string][]string{}}
		for _, n := range order { // 按该顺序“注册”
			g.own[n] = decls[n].own
			g.parents[n] = decls[n].parents
		}
		before := mergeCalls
		got, err := Resolve(g, "Mix")
		if err != nil {
			t.Fatalf("order %v: %v", order, err)
		}
		calls := mergeCalls - before
		sig := fmt.Sprintf("%v", got)
		if i == 0 {
			want, wantCalls = sig, calls
			continue
		}
		if sig != want {
			t.Errorf("order %v: result %s != %s", order, sig, want)
		}
		if calls != wantCalls {
			t.Errorf("order %v: mergeCalls %d != %d", order, calls, wantCalls)
		}
	}
}

// TestResolveUnknown 未知名称报错。
func TestResolveUnknown(t *testing.T) {
	g := &fakeGraph{own: map[string]map[string]string{}, parents: map[string][]string{}}
	if _, err := Resolve(g, "Ghost"); err == nil {
		t.Error("Resolve(Ghost) = nil error, want non-nil")
	}
}

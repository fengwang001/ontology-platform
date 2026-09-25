package project

import (
	"fmt"
	"sort"
	"testing"

	"ontology/access"
	"ontology/acl"
)

func newFixture() (*Projector, acl.Subject) {
	store := acl.NewStore()
	u := acl.User("u")
	store.Grant(u, "a", acl.Read)
	store.Grant(u, "b", acl.Read)
	return NewProjector(access.NewEvaluator(store)), u
}

// canonical 把对象序列化为确定性字节串（键排序）。
func canonical(obj Object) string {
	keys := make([]string, 0, len(obj))
	for k := range obj {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	s := ""
	for _, k := range keys {
		s += fmt.Sprintf("%s=%v;", k, obj[k])
	}
	return s
}

// TestProjectionDeterministic 证明读投影确定性：同一主体、同一对象、
// 任意遍历（插入）顺序，投影结果逐字节相同；且考察计数与属性数一致。
func TestProjectionDeterministic(t *testing.T) {
	p, u := newFixture()
	attrs := map[string]any{"a": 1, "b": "x", "c": true, "d": 0, "e": nil}

	// 用不同插入顺序构造「同一对象」的多个实例。
	orders := [][]string{
		{"a", "b", "c", "d", "e"},
		{"e", "d", "c", "b", "a"},
		{"c", "a", "e", "b", "d"},
	}
	var want string
	for i, order := range orders {
		obj := Object{}
		for _, k := range order {
			obj[k] = attrs[k]
		}
		before := p.examined
		got := canonical(p.Project(obj, u))
		if p.examined-before != len(attrs) {
			t.Fatalf("第 %d 次投影考察数=%d, want %d", i, p.examined-before, len(attrs))
		}
		if i == 0 {
			want = got
			continue
		}
		if got != want {
			t.Fatalf("第 %d 次投影结果 %q 与首次 %q 不一致", i, got, want)
		}
	}
	if want != "a=1;b=x;" {
		t.Fatalf("投影内容错误：%q, want %q", want, "a=1;b=x;")
	}
}

// TestRemovalNotMasking 不可见属性被移除而非置零值/掩码；
// 可见属性的零值保留（零值与真实缺失可区分）。
func TestRemovalNotMasking(t *testing.T) {
	p, u := newFixture()
	view := p.Project(Object{"a": 0, "b": "", "c": 1}, u)
	if _, ok := view["c"]; ok {
		t.Fatal("不可见属性 c 未被移除")
	}
	if v, ok := view["a"]; !ok || v != 0 {
		t.Fatalf("可见零值被误删或改写：a=%v ok=%v", v, ok)
	}
	if v, ok := view["b"]; !ok || v != "" {
		t.Fatalf("可见空串被误删或改写：b=%v ok=%v", v, ok)
	}
	// 原对象不被修改。
	src := Object{"a": 1, "c": 2}
	p.Project(src, u)
	if src["c"] != 2 {
		t.Fatal("投影修改了原对象")
	}
}

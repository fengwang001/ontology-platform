package ontology

import (
	"errors"
	"testing"
)

// TestFindUnknownElement 从未出现过的 ID，Find 必须返回
// 可判定的 ErrUnknownElement，且不得隐式创建该元素。
func TestFindUnknownElement(t *testing.T) {
	u := New()
	if _, err := u.Find("ghost"); !errors.Is(err, ErrUnknownElement) {
		t.Fatalf("Find(ghost) err=%v, 期望 ErrUnknownElement", err)
	}
	if got := u.ClassCount(); got != 0 {
		t.Fatalf("Find 隐式创建了元素: ClassCount()=%d, 期望 0", got)
	}
}

// TestUnionImplicitlyCreates Union 遇到未出现过的 ID 必须隐式创建。
func TestUnionImplicitlyCreates(t *testing.T) {
	u := New()
	u.Union("x", "y")
	if got := u.ClassCount(); got != 1 {
		t.Fatalf("ClassCount()=%d, 期望 1", got)
	}
	ok, err := u.Connected("x", "y")
	if err != nil || !ok {
		t.Fatalf("Connected(x,y)=%v, %v; 期望 true, nil", ok, err)
	}
}

// TestAddIdempotent 重复 Add 幂等不报错，也不改变类数。
func TestAddIdempotent(t *testing.T) {
	u := New()
	for i := 0; i < 100; i++ {
		u.Add("solo")
	}
	if got := u.ClassCount(); got != 1 {
		t.Fatalf("重复 Add 后 ClassCount()=%d, 期望 1", got)
	}
}

// TestEmptyStringIsValidID 空串是合法 ID。
func TestEmptyStringIsValidID(t *testing.T) {
	u := New()
	u.Add("")
	u.Union("", "a")
	rep, err := u.Find("a")
	if err != nil {
		t.Fatalf("Find(a) 出错: %v", err)
	}
	if rep != "" {
		t.Fatalf("Find(a)=%q, 期望空串作为最小代表元", rep)
	}
	ok, err := u.Connected("", "a")
	if err != nil || !ok {
		t.Fatalf("Connected(\"\",a)=%v, %v; 期望 true, nil", ok, err)
	}
}

// TestConnectedUnknownVsDisconnected Connected 必须把
// 「元素未知」（错误）与「不连通」（false, nil）区分开。
func TestConnectedUnknownVsDisconnected(t *testing.T) {
	u := New()
	u.Add("a")
	u.Add("b")
	// 两个已知但不连通的元素: false, nil。
	ok, err := u.Connected("a", "b")
	if err != nil || ok {
		t.Fatalf("Connected(a,b)=%v, %v; 期望 false, nil", ok, err)
	}
	// 任一元素未知: 可判定错误。
	if _, err := u.Connected("a", "ghost"); !errors.Is(err, ErrUnknownElement) {
		t.Fatalf("Connected(a,ghost) err=%v, 期望 ErrUnknownElement", err)
	}
	if _, err := u.Connected("ghost", "a"); !errors.Is(err, ErrUnknownElement) {
		t.Fatalf("Connected(ghost,a) err=%v, 期望 ErrUnknownElement", err)
	}
	if _, err := u.Connected("g1", "g2"); !errors.Is(err, ErrUnknownElement) {
		t.Fatalf("Connected(g1,g2) err=%v, 期望 ErrUnknownElement", err)
	}
	// 连通后: true, nil。
	u.Union("a", "b")
	ok, err = u.Connected("a", "b")
	if err != nil || !ok {
		t.Fatalf("Union 后 Connected(a,b)=%v, %v; 期望 true, nil", ok, err)
	}
}

// TestRepeatedUnionKeepsClassCount 重复 Union 已同类的两个元素
// 一百万次，类数不得减少，任何代表元不得改变。
func TestRepeatedUnionKeepsClassCount(t *testing.T) {
	u := New()
	u.Union("a", "b")
	u.Union("c", "d")
	u.Add("e")
	before := u.ClassCount()
	repsBefore := make(map[string]string)
	for _, id := range []string{"a", "b", "c", "d", "e"} {
		rep, err := u.Find(id)
		if err != nil {
			t.Fatalf("Find(%q) 出错: %v", id, err)
		}
		repsBefore[id] = rep
	}
	for i := 0; i < 1_000_000; i++ {
		u.Union("a", "b")
	}
	if got := u.ClassCount(); got != before {
		t.Fatalf("重复 Union 后 ClassCount()=%d, 期望 %d", got, before)
	}
	for id, want := range repsBefore {
		rep, err := u.Find(id)
		if err != nil || rep != want {
			t.Fatalf("重复 Union 后 Find(%q)=%q, %v; 期望 %q, nil", id, rep, err, want)
		}
	}
}

package topo

import (
	"errors"
	"testing"
)

func newTestGraph(t *testing.T, cMax int) *Graph {
	t.Helper()
	g, err := NewGraph(cMax)
	if err != nil {
		t.Fatalf("NewGraph: %v", err)
	}
	return g
}

func mustAdd(t *testing.T, g *Graph, name string, kind Kind) {
	t.Helper()
	if err := g.AddNode(name, kind); err != nil {
		t.Fatalf("AddNode(%q): %v", name, err)
	}
}

func TestNewGraphAndInvalidArgs(t *testing.T) {
	cases := []struct {
		name string
		cMax int
		err  error
	}{
		{"zero", 0, ErrInvalid},
		{"negative", -1, ErrInvalid},
		{"too large", 100001, ErrInvalid},
		{"boundary min", 1, nil},
		{"boundary max", 100000, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := NewGraph(tc.cMax)
			if !errors.Is(err, tc.err) {
				t.Fatalf("got %v want %v", err, tc.err)
			}
		})
	}
}

func TestAddNodeTable(t *testing.T) {
	g := newTestGraph(t, 10)
	cases := []struct {
		name string
		kind Kind
		err  error
	}{
		{"G1", KindGateway, nil},
		{"G1", KindGateway, ErrExists},
		{"a", KindDevice, nil},
		{"", KindGateway, ErrInvalid},
		{"bad", KindUnknown, ErrInvalid},
		{string(make([]byte, 65)), KindGateway, ErrInvalid},
		{"k2", Kind(99), ErrInvalid},
	}
	for _, tc := range cases {
		err := g.AddNode(tc.name, tc.kind)
		if !errors.Is(err, tc.err) {
			t.Errorf("AddNode(%q,%v): got %v want %v", tc.name, tc.kind, err, tc.err)
		}
	}
}

func TestBindErrorOrder(t *testing.T) {
	g := newTestGraph(t, 10)
	mustAdd(t, g, "G1", KindGateway)
	mustAdd(t, g, "H", KindGateway)
	mustAdd(t, g, "G2", KindGateway)
	mustAdd(t, g, "a", KindDevice)
	mustAdd(t, g, "b", KindDevice)
	mustAdd(t, g, "c", KindDevice)

	if _, err := g.Bind("H", "G1"); err != nil {
		t.Fatal(err)
	}
	type tc struct {
		child, parent string
		err           error
		note          string
	}
	cases := []tc{
		{"ghost", "G1", ErrNotFound, "child missing"},
		{"G1", "ghost", ErrType, "parent missing"},
		{"a", "b", ErrType, "parent is device (b not bound yet)"},
		{"G1", "G1", ErrType, "self bind"},
		{"", "G1", ErrInvalid, "invalid child name precedes everything"},
		{"G2", "H", ErrDepth, "parent H is a sub-gateway"},
		{"G2", "G1", nil, "empty gateway G2 may bind under G1"},
		{"G1", "G2", ErrDepth, "G1 has direct sub-gateway H"},
		{"c", "G1", nil, "first bind c"},
	}
	for _, c := range cases {
		_, err := g.Bind(c.child, c.parent)
		if !errors.Is(err, c.err) {
			t.Errorf("Bind(%s->%s) [%s]: got %v want %v", c.child, c.parent, c.note, err, c.err)
		}
	}

	// 容量与空操作：cMax=2 且 G1 已有 H、c 两个孩子。
	gf := newTestGraph(t, 2)
	mustAdd(t, gf, "G1", KindGateway)
	mustAdd(t, gf, "H", KindGateway)
	mustAdd(t, gf, "c", KindDevice)
	mustAdd(t, gf, "a", KindDevice)
	if _, err := gf.Bind("H", "G1"); err != nil {
		t.Fatal(err)
	}
	if _, err := gf.Bind("c", "G1"); err != nil {
		t.Fatal(err)
	}
	if _, err := gf.Bind("c", "G1"); err != nil {
		t.Fatalf("noop rebind must win over ErrFull: %v", err)
	}
	if _, err := gf.Bind("a", "G1"); !errors.Is(err, ErrFull) {
		t.Fatalf("Bind(a,G1): got %v want ErrFull", err)
	}
	if got := gf.ChildCount("G1"); got != 2 {
		t.Fatalf("G1 children = %d want 2", got)
	}
}

func TestRebindOnlineCascadesFirst(t *testing.T) {
	g := newTestGraph(t, 10)
	mustAdd(t, g, "G1", KindGateway)
	mustAdd(t, g, "H", KindGateway)
	mustAdd(t, g, "P", KindGateway)
	mustAdd(t, g, "c", KindDevice)
	if _, err := g.Bind("H", "G1"); err != nil {
		t.Fatal(err)
	}
	if _, err := g.Bind("c", "G1"); err != nil {
		t.Fatal(err)
	}
	var hooked []string
	online := map[string]bool{"H": true, "c": true}
	g.SetOnlineCheck(func(n string) bool { return online[n] })
	// 钩子模拟后序离线：此处只有单节点。
	g.SetOfflineHook(func(n string) []Event {
		hooked = append(hooked, n)
		return []Event{{Name: n, Epoch: 4}}
	})
	events, err := g.Bind("c", "H")
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].Name != "c" {
		t.Fatalf("events = %v, want offline(c) before rebind", events)
	}
	if len(hooked) != 1 || hooked[0] != "c" {
		t.Fatalf("hook order = %v", hooked)
	}
	if p, _ := g.ParentOf("c"); p != "H" {
		t.Fatalf("c parent = %q want H", p)
	}
	if g.ChildCount("G1") != 1 || g.ChildCount("H") != 1 {
		t.Fatalf("counts G1=%d H=%d", g.ChildCount("G1"), g.ChildCount("H"))
	}
}

func TestFailedRebindNoCascade(t *testing.T) {
	g := newTestGraph(t, 2)
	mustAdd(t, g, "G1", KindGateway)
	mustAdd(t, g, "P", KindGateway)
	mustAdd(t, g, "c", KindDevice)
	mustAdd(t, g, "x", KindDevice)
	mustAdd(t, g, "y", KindDevice)
	if _, err := g.Bind("c", "G1"); err != nil {
		t.Fatal(err)
	}
	if _, err := g.Bind("x", "P"); err != nil {
		t.Fatal(err)
	}
	if _, err := g.Bind("y", "P"); err != nil {
		t.Fatal(err)
	}
	called := false
	g.SetOfflineHook(func(n string) []Event { called = true; return nil })
	if _, err := g.Bind("c", "P"); !errors.Is(err, ErrFull) {
		t.Fatalf("got %v want ErrFull", err)
	}
	if called {
		t.Fatal("offline hook must not run when bind rejected")
	}
	if p, _ := g.ParentOf("c"); p != "G1" {
		t.Fatalf("binding changed despite rejection: parent=%q", p)
	}
}

func TestUnbindAndRemove(t *testing.T) {
	g := newTestGraph(t, 10)
	mustAdd(t, g, "G1", KindGateway)
	mustAdd(t, g, "a", KindDevice)
	if _, err := g.Unbind("a"); !errors.Is(err, ErrNotBound) {
		t.Fatalf("got %v want ErrNotBound", err)
	}
	if _, err := g.Unbind("ghost"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("got %v want ErrNotFound", err)
	}
	if err := g.RemoveNode("G1"); err != nil {
		t.Fatalf("free gateway removal: %v", err)
	}
	if err := g.RemoveNode("G1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("got %v want ErrNotFound", err)
	}
	g2 := newTestGraph(t, 10)
	mustAdd(t, g2, "R", KindGateway)
	mustAdd(t, g2, "b", KindDevice)
	if _, err := g2.Bind("b", "R"); err != nil {
		t.Fatal(err)
	}
	if err := g2.RemoveNode("b"); !errors.Is(err, ErrBusy) {
		t.Fatalf("bound node: got %v want ErrBusy", err)
	}
	if err := g2.RemoveNode("R"); !errors.Is(err, ErrBusy) {
		t.Fatalf("parent node: got %v want ErrBusy", err)
	}
	g2.SetOnlineCheck(func(string) bool { return true })
	if _, err := g2.Unbind("b"); err != nil {
		t.Fatal(err)
	}
	if err := g2.RemoveNode("b"); !errors.Is(err, ErrBusy) {
		t.Fatalf("online node: got %v want ErrBusy", err)
	}
}

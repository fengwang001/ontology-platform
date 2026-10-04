package route

import (
	"errors"
	"reflect"
	"testing"

	"ontology/session"
	"ontology/topo"
)

func TestRouteSpecExample(t *testing.T) {
	g, err := topo.NewGraph(10)
	if err != nil {
		t.Fatal(err)
	}
	m := session.NewManager(g)
	must := func(e error) {
		if e != nil {
			t.Fatal(e)
		}
	}
	must(g.AddNode("G1", topo.KindGateway))
	must(g.AddNode("H", topo.KindGateway))
	must(g.AddNode("a", topo.KindDevice))
	_, err = g.Bind("H", "G1")
	must(err)
	_, err = g.Bind("a", "H")
	must(err)
	if _, _, err := m.Online("G1", "", 0); err != nil {
		t.Fatal(err)
	}
	if _, _, err := m.Online("H", "G1", 1); err != nil {
		t.Fatal(err)
	}
	if _, _, err := m.Online("a", "H", 2); err != nil {
		t.Fatal(err)
	}

	r := NewResolver(g, m.EpochOfLocked)
	hops, err := r.Route("a")
	if err != nil {
		t.Fatal(err)
	}
	want := []Hop{{Name: "G1", Epoch: 1}, {Name: "H", Epoch: 2}, {Name: "a", Epoch: 3}}
	if !reflect.DeepEqual(hops, want) {
		t.Fatalf("Route(a)=%v want %v", hops, want)
	}
	if r.touched != 3 {
		t.Fatalf("touched=%d want 3", r.touched)
	}

	// 根网关路径只有一跳。
	hops, err = r.Route("G1")
	if err != nil || !reflect.DeepEqual(hops, []Hop{{Name: "G1", Epoch: 1}}) {
		t.Fatalf("Route(G1)=%v err=%v", hops, err)
	}
	if r.touched != 1 {
		t.Fatalf("touched=%d want 1", r.touched)
	}

	if _, err := r.Route("ghost"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("got %v want ErrNotFound", err)
	}
	if _, err := r.Route(""); !errors.Is(err, ErrInvalid) {
		t.Fatalf("got %v want ErrInvalid", err)
	}
}

func TestRouteOfflineCases(t *testing.T) {
	g, _ := topo.NewGraph(10)
	m := session.NewManager(g)
	must := func(e error) {
		if e != nil {
			t.Fatal(e)
		}
	}
	must(g.AddNode("R", topo.KindGateway))
	must(g.AddNode("d", topo.KindDevice))
	_, err := g.Bind("d", "R")
	must(err)
	r := NewResolver(g, m.EpochOfLocked)

	// 全离线。
	if _, err := r.Route("R"); !errors.Is(err, ErrOffline) {
		t.Fatalf("got %v want ErrOffline", err)
	}
	if _, _, err := m.Online("R", "", 0); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Route("d"); !errors.Is(err, ErrOffline) {
		t.Fatalf("child offline: got %v want ErrOffline", err)
	}
	if _, _, err := m.Online("d", "R", 1); err != nil {
		t.Fatal(err)
	}
	hops, err := r.Route("d")
	if err != nil || !reflect.DeepEqual(hops, []Hop{{"R", 1}, {"d", 2}}) {
		t.Fatalf("Route(d)=%v err=%v", hops, err)
	}
	// 接管 R 后，旧纪元子设备因级联离线，路径立即 ErrOffline。
	if _, _, err := m.Online("R", "", 0); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Route("d"); !errors.Is(err, ErrOffline) {
		t.Fatalf("stale child after takeover: got %v want ErrOffline", err)
	}
}

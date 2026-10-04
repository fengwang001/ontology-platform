package session

import (
	"errors"
	"fmt"
	"reflect"
	"testing"

	"ontology/topo"
)

type fixture struct {
	g *topo.Graph
	m *Manager
}

func newFixture(t *testing.T, cMax int) *fixture {
	t.Helper()
	g, err := topo.NewGraph(cMax)
	if err != nil {
		t.Fatal(err)
	}
	return &fixture{g: g, m: NewManager(g)}
}

func (f *fixture) add(name string, kind topo.Kind) {
	if err := f.g.AddNode(name, kind); err != nil {
		panic(err)
	}
}

func (f *fixture) bind(child, parent string) {
	if _, err := f.g.Bind(child, parent); err != nil {
		panic(err)
	}
}

func (f *fixture) online(node, via string, viaEpoch int64) ([]Event, int64) {
	ev, ep, err := f.m.Online(node, via, viaEpoch)
	if err != nil {
		panic(err)
	}
	return ev, ep
}

func buildExample(t *testing.T) *fixture {
	f := newFixture(t, 10)
	f.add("G1", topo.KindGateway)
	f.add("G2", topo.KindGateway)
	f.add("H", topo.KindGateway)
	f.add("a", topo.KindDevice)
	f.add("b", topo.KindDevice)
	f.add("c", topo.KindDevice)
	f.bind("H", "G1")
	f.bind("a", "H")
	f.bind("b", "H")
	f.bind("c", "G1")
	return f
}

func TestSpecExampleOnlineAndRouteBase(t *testing.T) {
	f := buildExample(t)
	checks := []struct {
		node     string
		via      string
		viaEpoch int64
		wantEp   int64
	}{
		{"G1", "", 0, 1},
		{"H", "G1", 1, 2},
		{"a", "H", 2, 3},
		{"c", "G1", 1, 4},
		{"b", "H", 2, 5},
	}
	for _, c := range checks {
		_, ep, err := f.m.Online(c.node, c.via, c.viaEpoch)
		if err != nil || ep != c.wantEp {
			t.Fatalf("Online(%s,%s,%d): ep=%d err=%v want ep=%d", c.node, c.via, c.viaEpoch, ep, err, c.wantEp)
		}
	}
}

func TestSpecTakeoverCascadeOrder(t *testing.T) {
	f := buildExample(t)
	f.online("G1", "", 0)
	f.online("H", "G1", 1)
	f.online("a", "H", 2)
	f.online("c", "G1", 1)
	f.online("b", "H", 2)

	events, ep, err := f.m.Online("G1", "", 0)
	if err != nil {
		t.Fatal(err)
	}
	wantEvents := []Event{
		{Name: "a", Epoch: 3},
		{Name: "b", Epoch: 5},
		{Name: "H", Epoch: 2},
		{Name: "c", Epoch: 4},
		{Name: "G1", Epoch: 1},
	}
	if !reflect.DeepEqual(events, wantEvents) {
		t.Fatalf("cascade events = %v want %v", events, wantEvents)
	}
	if ep != 6 {
		t.Fatalf("new epoch = %d want 6", ep)
	}
	// 父不在线先于纪元不符。
	if _, _, err := f.m.Online("a", "H", 2); !errors.Is(err, ErrOffline) {
		t.Fatalf("Online(a,H,2): got %v want ErrOffline", err)
	}
	if _, err := f.m.Offline("H", 2); !errors.Is(err, ErrOffline) {
		t.Fatalf("Offline(H,2): got %v want ErrOffline", err)
	}
	if _, _, err := f.m.Online("H", "G1", 1); !errors.Is(err, ErrStaleEpoch) {
		t.Fatalf("Online(H,G1,1): got %v want ErrStaleEpoch", err)
	}
	events, ep, err = f.m.Online("H", "G1", 6)
	if err != nil || ep != 7 || events != nil {
		t.Fatalf("Online(H,G1,6): events=%v ep=%d err=%v", events, ep, err)
	}
}

func TestOnlineValidationOrder(t *testing.T) {
	f := buildExample(t)
	f.online("G1", "", 0)

	cases := []struct {
		node     string
		via      string
		viaEpoch int64
		err      error
		note     string
	}{
		{"", "", 0, ErrInvalid, "bad name"},
		{"a", "H", -1, ErrInvalid, "negative epoch"},
		{"G1", "", 1, ErrInvalid, "direct connect with nonzero viaEpoch"},
		{"a", "", 1, ErrInvalid, "nonempty via invalid ordering guard: bad epoch too"},
		{"ghost", "", 0, ErrNotFound, "missing node precedes binding rules"},
		{"a", "", 0, ErrNotBound, "device cannot direct connect"},
		{"G1", "H", 2, ErrNotBound, "unbound-mismatch: G1 has no parent"},
		{"a", "G1", 1, ErrNotBound, "via is not current parent"},
	}
	for _, c := range cases {
		_, _, err := f.m.Online(c.node, c.via, c.viaEpoch)
		if !errors.Is(err, c.err) {
			t.Errorf("Online(%s,%s,%d) [%s]: got %v want %v", c.node, c.via, c.viaEpoch, c.note, err, c.err)
		}
	}

	// 父不在线：把 H 下的 a 尝试上线，H 未上线。
	if _, _, err := f.m.Online("a", "H", 2); !errors.Is(err, ErrOffline) {
		t.Fatalf("got %v want ErrOffline", err)
	}
	// 先上线 H（纪元2），再用旧纪元上 a。
	_, epH, err := f.m.Online("H", "G1", 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := f.m.Online("a", "H", 99); !errors.Is(err, ErrStaleEpoch) {
		t.Fatalf("got %v want ErrStaleEpoch", err)
	}
	// 拒绝不占号：接着合法上线仍得到 epH+1。
	_, epA, err := f.m.Online("a", "H", epH)
	if err != nil {
		t.Fatal(err)
	}
	if epA != epH+1 {
		t.Fatalf("epoch hole: epA=%d want %d", epA, epH+1)
	}
}

func TestOfflineStaleEpoch(t *testing.T) {
	f := buildExample(t)
	f.online("G1", "", 0)
	f.online("H", "G1", 1)
	if _, err := f.m.Offline("H", 99); !errors.Is(err, ErrStaleEpoch) {
		t.Fatalf("got %v want ErrStaleEpoch", err)
	}
	if !f.m.IsOnline("H") {
		t.Fatal("H must remain online after rejected offline")
	}
	events, err := f.m.Offline("H", 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0] != (Event{Name: "H", Epoch: 2}) {
		t.Fatalf("events=%v", events)
	}
	if _, err := f.m.Offline("H", 2); !errors.Is(err, ErrOffline) {
		t.Fatalf("got %v want ErrOffline", err)
	}
	if _, err := f.m.Offline("ghost", 1); !errors.Is(err, ErrNotFound) {
		t.Fatalf("got %v want ErrNotFound", err)
	}
}

func TestRebindOnlineCascadesViaTopo(t *testing.T) {
	f := buildExample(t)
	f.online("G1", "", 0)
	f.online("H", "G1", 1)
	f.online("c", "G1", 1)
	events, err := f.g.Bind("c", "H")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(events, []Event{{Name: "c", Epoch: 3}}) {
		t.Fatalf("rebind events = %v want [(c,3)]", events)
	}
	if f.m.IsOnline("c") {
		t.Fatal("c must be offline after cascading rebind")
	}
}

func TestCascadeTouchedTwoScales(t *testing.T) {
	for _, total := range []int{10000, 10} {
		t.Run(fmt.Sprintf("children=%d", total), func(t *testing.T) {
			f := newFixture(t, 100000)
			f.add("R", topo.KindGateway)
			f.online("R", "", 0)
			onlineNames := map[string]bool{"d00001": true, "d00002": true, "d00003": true}
			for i := 0; i < total; i++ {
				name := fmt.Sprintf("d%05d", i+1)
				f.add(name, topo.KindDevice)
				f.bind(name, "R")
				if onlineNames[name] {
					_, _, err := f.m.Online(name, "R", 1)
					if err != nil {
						t.Fatal(err)
					}
				}
			}
			events, err := f.m.Offline("R", 1)
			if err != nil {
				t.Fatal(err)
			}
			if len(events) != 4 {
				t.Fatalf("events=%d want 4", len(events))
			}
			if f.m.touched != len(events) {
				t.Fatalf("touched=%d events=%d", f.m.touched, len(events))
			}
			if f.m.touched != 4 {
				t.Fatalf("touched=%d want 4 regardless of %d offline children", f.m.touched, total-3)
			}
			// 事件后序：孩子按字节序在前，R 在最后。
			want := []Event{
				{Name: "d00001", Epoch: 2},
				{Name: "d00002", Epoch: 3},
				{Name: "d00003", Epoch: 4},
				{Name: "R", Epoch: 1},
			}
			if !reflect.DeepEqual(events, want) {
				t.Fatalf("events=%v want=%v", events, want)
			}
		})
	}
}

func TestOnlineImpliesParentOnline(t *testing.T) {
	f := buildExample(t)
	f.online("G1", "", 0)
	f.online("H", "G1", 1)
	f.online("a", "H", 2)
	// 直接令 H 离线（带正确纪元），a 必须随级联离线。
	events, err := f.m.Offline("H", 2)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(events, []Event{{Name: "a", Epoch: 3}, {Name: "H", Epoch: 2}}) {
		t.Fatalf("events=%v", events)
	}
	if f.m.IsOnline("a") {
		t.Fatal("child stayed online while parent offline")
	}
}

package ontology_test

import (
	"errors"
	"strings"
	"testing"

	"ontology"
)

type ev struct {
	node string
	ep   int64
}

type hop struct {
	node string
	ep   int64
}

// step 为表驱动中的一个操作及对其输出的期望。
type step struct {
	op      string
	node    string
	kind    ontology.Kind
	parent  string
	via     string
	viaEp   int64
	epoch   int64
	wantErr error
	wantEp  int64 // -1 表示不检查 online 返回纪元
	events  []ev
	route   []hop
}

func g(name string) step { return step{op: "add", node: name, kind: ontology.KindGateway} }
func d(name string) step { return step{op: "add", node: name, kind: ontology.KindDevice} }

func runSteps(t *testing.T, cmax int, steps []step) *ontology.Manager {
	t.Helper()
	m, err := ontology.New(cmax)
	if err != nil {
		t.Fatalf("New(%d): %v", cmax, err)
	}
	for i, s := range steps {
		var gotEvents []ontology.Event
		var gotEp int64
		var gotErr error
		var gotRoute []ontology.Hop
		switch s.op {
		case "add":
			gotErr = m.AddNode(s.node, s.kind)
		case "remove":
			gotErr = m.RemoveNode(s.node)
		case "bind":
			gotEvents, gotErr = m.Bind(s.node, s.parent)
		case "unbind":
			gotEvents, gotErr = m.Unbind(s.node)
		case "online":
			gotEvents, gotEp, gotErr = m.Online(s.node, s.via, s.viaEp)
		case "offline":
			gotEvents, gotErr = m.Offline(s.node, s.epoch)
		case "route":
			gotRoute, gotErr = m.Route(s.node)
		default:
			t.Fatalf("step %d: bad op %q", i, s.op)
		}
		if !errors.Is(gotErr, s.wantErr) {
			t.Fatalf("step %d %s: want err %v, got %v", i, s.op, s.wantErr, gotErr)
		}
		if gotErr != nil {
			if len(gotEvents) != 0 {
				t.Fatalf("step %d: rejected op must emit no events, got %v", i, gotEvents)
			}
			continue
		}
		if s.op == "online" && s.wantEp != -1 && gotEp != s.wantEp {
			t.Fatalf("step %d online epoch: want %d, got %d", i, s.wantEp, gotEp)
		}
		if s.events != nil {
			cmpEvents(t, i, s.events, gotEvents)
		}
		if s.route != nil {
			cmpRoute(t, i, s.route, gotRoute)
		}
	}
	return m
}

func cmpEvents(t *testing.T, step int, want []ev, got []ontology.Event) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("step %d events len: want %v, got %v", step, want, got)
	}
	for j, e := range got {
		if e.Node != want[j].node || e.Epoch != want[j].ep {
			t.Fatalf("step %d event %d: want (%s,%d), got (%s,%d)",
				step, j, want[j].node, want[j].ep, e.Node, e.Epoch)
		}
	}
}

func cmpRoute(t *testing.T, step int, want []hop, got []ontology.Hop) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("step %d route len: want %v, got %v", step, want, got)
	}
	for j, h := range got {
		if h.Node != want[j].node || h.Epoch != want[j].ep {
			t.Fatalf("step %d hop %d: want (%s,%d), got (%s,%d)",
				step, j, want[j].node, want[j].ep, h.Node, h.Epoch)
		}
	}
}

// TestExampleFromSpec 复现题目第一个完整示例（接管、字节序后序、纪元）。
func TestExampleFromSpec(t *testing.T) {
	steps := []step{
		g("G1"), g("G2"), g("H"), d("a"), d("b"), d("c"),
		{op: "bind", node: "H", parent: "G1"},
		{op: "bind", node: "a", parent: "H"},
		{op: "bind", node: "b", parent: "H"},
		{op: "bind", node: "c", parent: "G1"},
		{op: "online", node: "G1", wantEp: 1},
		{op: "online", node: "H", via: "G1", viaEp: 1, wantEp: 2},
		{op: "online", node: "a", via: "H", viaEp: 2, wantEp: 3},
		{op: "online", node: "c", via: "G1", viaEp: 1, wantEp: 4},
		{op: "online", node: "b", via: "H", viaEp: 2, wantEp: 5},
		{op: "route", node: "a", route: []hop{{"G1", 1}, {"H", 2}, {"a", 3}}},
		{op: "online", node: "G1", wantEp: 6, events: []ev{
			{"a", 3}, {"b", 5}, {"H", 2}, {"c", 4}, {"G1", 1},
		}},
		{op: "online", node: "a", via: "H", viaEp: 2, wantErr: ontology.ErrOffline},
		{op: "offline", node: "H", epoch: 2, wantErr: ontology.ErrOffline},
		{op: "online", node: "H", via: "G1", viaEp: 1, wantErr: ontology.ErrStaleEpoch},
		{op: "online", node: "H", via: "G1", viaEp: 6, wantEp: 7},
	}
	runSteps(t, 100000, steps)
}

// TestRebindFailureNoOffline 判定失败不改绑、不离线、不占纪元。
func TestRebindFailureNoOffline(t *testing.T) {
	steps := []step{
		g("G1"), g("G2"), g("GG"), d("a"),
		{op: "bind", node: "a", parent: "G1"},
		{op: "bind", node: "GG", parent: "G1"}, // G1 成为有直属子网关的网关
		{op: "online", node: "G1", wantEp: 1},
		{op: "online", node: "a", via: "G1", viaEp: 1, wantEp: 2},
		{op: "bind", node: "a", parent: "a", wantErr: ontology.ErrType},
		{op: "route", node: "a", route: []hop{{"G1", 1}, {"a", 2}}},
		{op: "bind", node: "G2", parent: "GG", wantErr: ontology.ErrDepth}, // 父网关非根
		{op: "bind", node: "G1", parent: "G2", wantErr: ontology.ErrDepth}, // child 有子网关
		{op: "route", node: "a", route: []hop{{"G1", 1}, {"a", 2}}},
		{op: "online", node: "G2", wantEp: 3},
	}
	runSteps(t, 100000, steps)
}

// TestRemoveAndTakeover 覆盖 ErrBusy、接管先离线再占号、旧纪元 Offline。
func TestRemoveAndTakeover(t *testing.T) {
	steps := []step{
		g("G"), d("a"), d("b"),
		{op: "remove", node: "ghost", wantErr: ontology.ErrNotFound},
		{op: "remove", node: "G"},
		g("G"),
		{op: "bind", node: "a", parent: "G"},
		{op: "bind", node: "b", parent: "G"},
		{op: "online", node: "G", wantEp: 1},
		{op: "online", node: "a", via: "G", viaEp: 1, wantEp: 2},
		{op: "remove", node: "G", wantErr: ontology.ErrBusy},
		{op: "remove", node: "a", wantErr: ontology.ErrBusy},
		{op: "online", node: "a", via: "G", viaEp: 1, wantEp: 3,
			events: []ev{{"a", 2}}},
		{op: "offline", node: "a", epoch: 2, wantErr: ontology.ErrStaleEpoch},
		{op: "offline", node: "a", epoch: 3, events: []ev{{"a", 3}}},
		// 全部离线并解绑后才可删除。
		{op: "offline", node: "G", epoch: 1, events: []ev{{"G", 1}}},
		{op: "unbind", node: "a"},
		{op: "unbind", node: "b"},
		{op: "remove", node: "a"},
		{op: "remove", node: "b"},
		{op: "remove", node: "G"},
	}
	runSteps(t, 100000, steps)
}

// TestPostorderByteSort 多个在线孩子时严格按字节序后序。
func TestPostorderByteSort(t *testing.T) {
	steps := []step{
		g("R"), d("B"), g("a"), g("z"), d("0"), d("9"),
		// R 的在线孩子候选：网关 a、z，设备 0、9；字节序 0,9,a,z。
		{op: "bind", node: "a", parent: "R"},
		{op: "bind", node: "z", parent: "R"},
		{op: "bind", node: "0", parent: "R"},
		{op: "bind", node: "9", parent: "R"},
		{op: "bind", node: "B", parent: "a"}, // 离线设备，不应被触碰
		{op: "online", node: "R", wantEp: 1},
		{op: "online", node: "a", via: "R", viaEp: 1, wantEp: 2},
		{op: "online", node: "z", via: "R", viaEp: 1, wantEp: 3},
		{op: "online", node: "0", via: "R", viaEp: 1, wantEp: 4},
		{op: "online", node: "9", via: "R", viaEp: 1, wantEp: 5},
		{op: "offline", node: "R", epoch: 1, events: []ev{
			{"0", 4}, {"9", 5}, {"a", 2}, {"z", 3}, {"R", 1},
		}},
	}
	m := runSteps(t, 100000, steps)
	if got := m.CascadeTouched(); got != 5 {
		t.Fatalf("CascadeTouched: want 5, got %d", got)
	}
}

// TestTouchedIndependentOfOfflineKids 级联触碰数只等于在线节点数。
func TestTouchedIndependentOfOfflineKids(t *testing.T) {
	build := func(t *testing.T, cmax, total, onlineKids int) (*ontology.Manager, []string) {
		m, err := ontology.New(cmax)
		if err != nil {
			t.Fatal(err)
		}
		if err := m.AddNode("R", ontology.KindGateway); err != nil {
			t.Fatal(err)
		}
		if err := m.AddNode("H", ontology.KindGateway); err != nil {
			t.Fatal(err)
		}
		if _, err := m.Bind("H", "R"); err != nil {
			t.Fatal(err)
		}
		kids := make([]string, total)
		for i := range kids {
			name := "d" + padName(i)
			kids[i] = name
			if err := m.AddNode(name, ontology.KindDevice); err != nil {
				t.Fatal(err)
			}
			if evs, err := m.Bind(name, "H"); err != nil {
				t.Fatal(err)
			} else if len(evs) != 0 {
				t.Fatalf("fresh bind emitted events: %v", evs)
			}
		}
		if _, ep, err := m.Online("R", "", 0); err != nil {
			t.Fatal(err)
		} else if ep != 1 {
			t.Fatalf("R epoch: %d", ep)
		}
		if _, ep, err := m.Online("H", "R", 1); err != nil {
			t.Fatal(err)
		} else if ep != 2 {
			t.Fatalf("H epoch: %d", ep)
		}
		for i := 0; i < onlineKids; i++ {
			if _, _, err := m.Online(kids[i], "H", 2); err != nil {
				t.Fatal(err)
			}
		}
		return m, kids
	}
	for _, tc := range []struct {
		cmax, total, online int
	}{
		{100000, 10, 3},
		{100000, 10000, 3},
	} {
		m, _ := build(t, tc.cmax, tc.total, tc.online)
		evs, err := m.Offline("H", 2)
		if err != nil {
			t.Fatal(err)
		}
		if len(evs) != tc.online+1 {
			t.Fatalf("total=%d: events want %d, got %d", tc.total, tc.online+1, len(evs))
		}
		if got := m.CascadeTouched(); got != tc.online+1 {
			t.Fatalf("total=%d: touched want %d, got %d", tc.total, tc.online+1, got)
		}
	}
}

func padName(i int) string {
	s := ""
	for x := i; x >= 0; x = x/26 - 1 {
		s = string(rune('a'+x%26)) + s
		if x == 0 {
			break
		}
	}
	return s
}

// TestRouteTouched 路径触碰节点数 1..3；离线根下不可路由。
func TestRouteTouched(t *testing.T) {
	m, err := ontology.New(100)
	if err != nil {
		t.Fatal(err)
	}
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(m.AddNode("R", ontology.KindGateway))
	must(m.AddNode("H", ontology.KindGateway))
	must(m.AddNode("a", ontology.KindDevice))
	must(bindErr(m, "H", "R"))
	must(bindErr(m, "a", "H"))
	if _, err := m.Route("R"); !errors.Is(err, ontology.ErrOffline) {
		t.Fatalf("route offline root: %v", err)
	}
	_, _, err = m.Online("R", "", 0)
	must(err)
	if hops, err := m.Route("R"); err != nil || len(hops) != 1 {
		t.Fatalf("root route: %v %v", hops, err)
	}
	if got := m.RouteTouched(); got != 1 {
		t.Fatalf("root touched: %d", got)
	}
	_, _, err = m.Online("H", "R", 1)
	must(err)
	_, _, err = m.Online("a", "H", 2)
	must(err)
	hops, err := m.Route("a")
	must(err)
	if len(hops) != 3 || m.RouteTouched() != 3 {
		t.Fatalf("leaf route: %v touched=%d", hops, m.RouteTouched())
	}
}

// TestConcurrent 并发烟雾测试：配合 -race 检查数据竞争与不变量。
func TestConcurrent(t *testing.T) {
	m, err := ontology.New(1000)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"R", "H", "a", "b"} {
		kind := ontology.KindDevice
		if name == "R" || name == "H" {
			kind = ontology.KindGateway
		}
		if err := m.AddNode(name, kind); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := m.Bind("H", "R"); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Bind("a", "H"); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Bind("b", "R"); err != nil {
		t.Fatal(err)
	}
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			select {
			case <-stop:
				return
			default:
				m.Online("R", "", 0)
				m.Online("H", "R", epochOf(m, "R"))
				m.Online("a", "H", epochOf(m, "H"))
				m.Route("a")
				m.Offline("R", epochOf(m, "R"))
			}
		}
	}()
	for i := 0; i < 200; i++ {
		m.Online("b", "R", epochOf(m, "R"))
		m.Route("b")
	}
	close(stop)
	<-done
}

func epochOf(m *ontology.Manager, name string) ontology.Epoch {
	hops, err := m.Route(name)
	if err != nil || len(hops) == 0 {
		return 0
	}
	return hops[len(hops)-1].Epoch
}

func bindErr(m *ontology.Manager, child, parent string) error {
	_, err := m.Bind(child, parent)
	return err
}

// TestDepthAndRebind 复现题目第二个示例：两种层级超限、改绑先离线、空操作优先容量。
func TestDepthAndRebind(t *testing.T) {
	steps := []step{
		g("G1"), g("G2"), g("H"), d("a"), d("b"), d("c"),
		{op: "bind", node: "H", parent: "G1"},
		{op: "bind", node: "a", parent: "H"},
		{op: "bind", node: "b", parent: "H"},
		{op: "bind", node: "c", parent: "G1"},
		{op: "online", node: "G1", wantEp: 1},
		{op: "online", node: "H", via: "G1", viaEp: 1, wantEp: 2},
		{op: "online", node: "a", via: "H", viaEp: 2, wantEp: 3},
		{op: "online", node: "c", via: "G1", viaEp: 1, wantEp: 4},
		{op: "bind", node: "G2", parent: "G1"},
		{op: "bind", node: "G1", parent: "G2", wantErr: ontology.ErrDepth},
		{op: "bind", node: "G2", parent: "H", wantErr: ontology.ErrDepth},
		{op: "bind", node: "c", parent: "H", events: []ev{{"c", 4}}},
		{op: "route", node: "c", wantErr: ontology.ErrOffline},
	}
	runSteps(t, 100000, steps)

	cmax2 := []step{
		g("G1"), g("H"), d("a"), d("c"),
		{op: "bind", node: "H", parent: "G1"},
		{op: "bind", node: "c", parent: "G1"},
		{op: "online", node: "G1", wantEp: 1},
		{op: "online", node: "H", via: "G1", viaEp: 1, wantEp: 2},
		{op: "bind", node: "a", parent: "H"},
		{op: "online", node: "a", via: "H", viaEp: 2, wantEp: 3},
		{op: "bind", node: "c", parent: "G1"},
		{op: "bind", node: "a", parent: "G1", wantErr: ontology.ErrFull},
		{op: "route", node: "a", route: []hop{{"G1", 1}, {"H", 2}, {"a", 3}}},
	}
	runSteps(t, 2, cmax2)
}

// TestErrorOrdering 覆盖各 API 的判定次序与 ErrInvalid 优先。
func TestErrorOrdering(t *testing.T) {
	steps := []step{
		{op: "add", node: "", kind: ontology.KindGateway, wantErr: ontology.ErrInvalid},
		{op: "add", node: strings.Repeat("x", 65), kind: ontology.KindGateway, wantErr: ontology.ErrInvalid},
		{op: "add", node: "x", kind: ontology.Kind(0), wantErr: ontology.ErrInvalid},
		g("G"), g("H"), d("a"),
		{op: "add", node: "G", kind: ontology.KindGateway, wantErr: ontology.ErrExists},
		{op: "bind", node: "ghost", parent: "G", wantErr: ontology.ErrNotFound},
		{op: "bind", node: "G", parent: "ghost", wantErr: ontology.ErrNotFound},
		{op: "bind", node: "a", parent: "H"},
		{op: "bind", node: "a", parent: "a", wantErr: ontology.ErrType},
		{op: "bind", node: "G", parent: "a", wantErr: ontology.ErrType},
		{op: "bind", node: "H", parent: "G"},
		{op: "bind", node: "G", parent: "H", wantErr: ontology.ErrDepth},
		{op: "unbind", node: "ghost", wantErr: ontology.ErrNotFound},
		{op: "unbind", node: "G", wantErr: ontology.ErrNotBound},
		{op: "online", node: "ghost", wantErr: ontology.ErrNotFound},
		{op: "online", node: "a", wantErr: ontology.ErrNotBound},
		{op: "online", node: "G", via: "H", viaEp: 0, wantErr: ontology.ErrNotBound},
		{op: "online", node: "G", wantEp: 1},
		{op: "online", node: "H", via: "G", viaEp: 1, wantEp: 2},
		{op: "online", node: "a", via: "H", viaEp: 1, wantErr: ontology.ErrStaleEpoch},
		{op: "online", node: "a", via: "H", viaEp: 2, wantEp: 3},
		{op: "offline", node: "H", epoch: 2, events: []ev{{"a", 3}, {"H", 2}}},
		{op: "online", node: "a", via: "H", viaEp: 99, wantErr: ontology.ErrOffline},
		{op: "offline", node: "ghost", epoch: 0, wantErr: ontology.ErrNotFound},
		{op: "offline", node: "H", epoch: 2, wantErr: ontology.ErrOffline},
		{op: "online", node: "H", via: "G", viaEp: 1, wantEp: 4},
		{op: "offline", node: "H", epoch: 2, wantErr: ontology.ErrStaleEpoch},
		{op: "offline", node: "H", epoch: 4, events: []ev{{"H", 4}}},
		{op: "online", node: "G", viaEp: 5, wantErr: ontology.ErrInvalid},
		{op: "online", node: "G", wantEp: 5}, // 接管 G，事件省略
		{op: "offline", node: "G", epoch: -1, wantErr: ontology.ErrInvalid},
		{op: "route", node: "ghost", wantErr: ontology.ErrNotFound},
		{op: "route", node: "H", wantErr: ontology.ErrOffline},
	}
	runSteps(t, 100000, steps)
}

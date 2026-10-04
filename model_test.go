package ontology_test

import (
	"errors"
	"fmt"
	"math/rand"
	"sort"
	"strconv"
	"strings"
	"testing"

	"ontology"
)

// naiveModel 是与产品代码完全独立、按题目语义逐步推进的朴素参考实现。
type naiveModel struct {
	kind    map[string]ontology.Kind
	parent  map[string]string
	kids    map[string]map[string]bool
	liveEp  map[string]int64
	next    int64
	cmax    int
	lastTch int
}

func newNaive(cmax int) *naiveModel {
	return &naiveModel{
		kind:   map[string]ontology.Kind{},
		parent: map[string]string{},
		kids:   map[string]map[string]bool{},
		liveEp: map[string]int64{},
		cmax:   cmax,
	}
}

func validName(n string) bool { return len(n) >= 1 && len(n) <= 64 }

type naiveResult struct {
	err    error
	epoch  int64
	events []ev
	route  []hop
	touch  int
}

func (md *naiveModel) path(n string) []string {
	chain := []string{n}
	for p := md.parent[n]; p != ""; p = md.parent[p] {
		chain = append(chain, p)
	}
	out := make([]string, len(chain))
	for i := range chain {
		out[i] = chain[len(chain)-1-i]
	}
	return out
}

func (md *naiveModel) cascade(n string) []ev {
	var kids []string
	for k := range md.kids[n] {
		if _, ok := md.liveEp[k]; ok {
			kids = append(kids, k)
		}
	}
	sort.Strings(kids)
	var out []ev
	for _, k := range kids {
		out = append(out, md.cascade(k)...)
	}
	ep := md.liveEp[n]
	delete(md.liveEp, n)
	return append(out, ev{n, ep})
}

func (md *naiveModel) add(n string, k ontology.Kind) naiveResult {
	if !validName(n) || (k != ontology.KindGateway && k != ontology.KindDevice) {
		return naiveResult{err: ontology.ErrInvalid}
	}
	if _, ok := md.kind[n]; ok {
		return naiveResult{err: ontology.ErrExists}
	}
	md.kind[n] = k
	md.kids[n] = map[string]bool{}
	return naiveResult{}
}

func (md *naiveModel) bindCheck(child, parent string) (error, bool) {
	if !validName(child) || !validName(parent) {
		return ontology.ErrInvalid, false
	}
	if _, ok := md.kind[child]; !ok {
		return ontology.ErrNotFound, false
	}
	if _, ok := md.kind[parent]; !ok {
		return ontology.ErrNotFound, false
	}
	if md.kind[parent] != ontology.KindGateway || child == parent {
		return ontology.ErrType, false
	}
	if md.parent[child] == parent {
		return nil, true
	}
	if md.kind[child] == ontology.KindGateway {
		if md.parent[parent] != "" {
			return ontology.ErrDepth, false
		}
		for k := range md.kids[child] {
			if md.kind[k] == ontology.KindGateway {
				return ontology.ErrDepth, false
			}
		}
	}
	if len(md.kids[parent]) >= md.cmax {
		return ontology.ErrFull, false
	}
	return nil, false
}

func (md *naiveModel) bind(child, parent string) naiveResult {
	err, noop := md.bindCheck(child, parent)
	if err != nil || noop {
		return naiveResult{err: err}
	}
	var events []ev
	if _, ok := md.liveEp[child]; ok {
		events = md.cascade(child)
	}
	if old := md.parent[child]; old != "" {
		delete(md.kids[old], child)
	}
	md.parent[child] = parent
	md.kids[parent][child] = true
	return naiveResult{events: events, touch: len(events)}
}

func (md *naiveModel) unbind(child string) naiveResult {
	if !validName(child) {
		return naiveResult{err: ontology.ErrInvalid}
	}
	if _, ok := md.kind[child]; !ok {
		return naiveResult{err: ontology.ErrNotFound}
	}
	if md.parent[child] == "" {
		return naiveResult{err: ontology.ErrNotBound}
	}
	var events []ev
	if _, ok := md.liveEp[child]; ok {
		events = md.cascade(child)
	}
	old := md.parent[child]
	delete(md.kids[old], child)
	delete(md.parent, child)
	return naiveResult{events: events, touch: len(events)}
}

func (md *naiveModel) online(node, via string, viaEp int64) naiveResult {
	if !validName(node) || viaEp < 0 {
		return naiveResult{err: ontology.ErrInvalid}
	}
	if via == "" && viaEp != 0 {
		return naiveResult{err: ontology.ErrInvalid}
	}
	if via != "" && !validName(via) {
		return naiveResult{err: ontology.ErrInvalid}
	}
	k, ok := md.kind[node]
	if !ok {
		return naiveResult{err: ontology.ErrNotFound}
	}
	if via == "" {
		if !(k == ontology.KindGateway && md.parent[node] == "") {
			return naiveResult{err: ontology.ErrNotBound}
		}
	} else {
		if md.parent[node] != via {
			return naiveResult{err: ontology.ErrNotBound}
		}
		if _, live := md.liveEp[via]; !live {
			return naiveResult{err: ontology.ErrOffline}
		}
		if md.liveEp[via] != viaEp {
			return naiveResult{err: ontology.ErrStaleEpoch}
		}
	}
	var events []ev
	if _, ok := md.liveEp[node]; ok {
		events = md.cascade(node)
	}
	md.next++
	md.liveEp[node] = md.next
	return naiveResult{epoch: md.next, events: events}
}

func (md *naiveModel) offline(node string, ep int64) naiveResult {
	if !validName(node) || ep < 0 {
		return naiveResult{err: ontology.ErrInvalid}
	}
	if _, ok := md.kind[node]; !ok {
		return naiveResult{err: ontology.ErrNotFound}
	}
	cur, live := md.liveEp[node]
	if !live {
		return naiveResult{err: ontology.ErrOffline}
	}
	if cur != ep {
		return naiveResult{err: ontology.ErrStaleEpoch}
	}
	events := md.cascade(node)
	return naiveResult{events: events, touch: len(events)}
}

func (md *naiveModel) route(node string) naiveResult {
	if !validName(node) {
		return naiveResult{err: ontology.ErrInvalid}
	}
	if _, ok := md.kind[node]; !ok {
		return naiveResult{err: ontology.ErrNotFound}
	}
	var hops []hop
	for _, n := range md.path(node) {
		ep, live := md.liveEp[n]
		if !live {
			return naiveResult{err: ontology.ErrOffline}
		}
		hops = append(hops, hop{n, ep})
	}
	return naiveResult{route: hops, touch: len(hops)}
}

func (md *naiveModel) remove(n string) naiveResult {
	if !validName(n) {
		return naiveResult{err: ontology.ErrInvalid}
	}
	if _, ok := md.kind[n]; !ok {
		return naiveResult{err: ontology.ErrNotFound}
	}
	if _, live := md.liveEp[n]; live || md.parent[n] != "" || len(md.kids[n]) > 0 {
		return naiveResult{err: ontology.ErrBusy}
	}
	delete(md.kind, n)
	delete(md.kids, n)
	return naiveResult{}
}

// assertInvariants 用朴素模型视角检查产品状态必须满足的全局不变量。
func (md *naiveModel) assertInvariants(t *testing.T, group, step int, m *ontology.Manager) {
	t.Helper()
	seenEpoch := map[int64]bool{}
	for n, ep := range md.liveEp {
		if seenEpoch[ep] {
			t.Fatalf("group %d step %d: epoch %d used twice", group, step, ep)
		}
		seenEpoch[ep] = true
		if ep <= 0 || ep > md.next {
			t.Fatalf("group %d step %d: impossible epoch %d (counter=%d)", group, step, ep, md.next)
		}
		if p := md.parent[n]; p != "" {
			pep, live := md.liveEp[p]
			if !live {
				t.Fatalf("group %d step %d: online %s but parent %s offline", group, step, n, p)
			}
			// “在父当前纪元下上线”由成功路径保证；这里校验父子纪元次序一致。
			if pep >= ep {
				t.Fatalf("group %d step %d: %s(ep=%d) >= child %s(ep=%d)", group, step, p, pep, n, ep)
			}
		}
		if md.kind[n] == ontology.KindDevice && md.parent[n] == "" {
			t.Fatalf("group %d step %d: online unbound device %s", group, step, n)
		}
		if !m.IsOnline(n) {
			t.Fatalf("group %d step %d: naive online %s but real offline", group, step, n)
		}
	}
	// 纪元“连续无洞”由主循环每步比对 real 成功上线纪元 == naive 的 next 保证。
	for n := range md.kind {
		if len(md.kids[n]) > md.cmax {
			t.Fatalf("group %d step %d: %s has %d kids > cmax %d", group, step, n, len(md.kids[n]), md.cmax)
		}
		if len(md.path(n)) > 3 {
			t.Fatalf("group %d step %d: path to %s longer than 3", group, step, n)
		}
		if md.liveEp[n] == 0 && m.IsOnline(n) {
			t.Fatalf("group %d step %d: real online %s but naive offline", group, step, n)
		}
	}
}

type randomOp struct {
	kind   int // 0add 1remove 2bind 3unbind 4online 5offline 6route
	node   string
	k      ontology.Kind
	parent string
	via    string
	viaEp  int64
	epoch  int64
}

func opName(k int) string {
	return [...]string{"AddNode", "RemoveNode", "Bind", "Unbind", "Online", "Offline", "Route"}[k]
}

var errNames = map[error]string{
	ontology.ErrInvalid:    "ErrInvalid",
	ontology.ErrNotFound:   "ErrNotFound",
	ontology.ErrExists:     "ErrExists",
	ontology.ErrType:       "ErrType",
	ontology.ErrDepth:      "ErrDepth",
	ontology.ErrFull:       "ErrFull",
	ontology.ErrNotBound:   "ErrNotBound",
	ontology.ErrOffline:    "ErrOffline",
	ontology.ErrStaleEpoch: "ErrStaleEpoch",
	ontology.ErrBusy:       "ErrBusy",
}

func errName(e error) string {
	if e == nil {
		return "OK"
	}
	return errNames[e]
}

func eventsName(evs []ontology.Event) string {
	parts := make([]string, len(evs))
	for i, e := range evs {
		parts[i] = fmt.Sprintf("(%s,%d)", e.Node, e.Epoch)
	}
	return fmt.Sprint(parts)
}

func routeName(hops []ontology.Hop) string {
	parts := make([]string, len(hops))
	for i, h := range hops {
		parts[i] = fmt.Sprintf("(%s,%d)", h.Node, h.Epoch)
	}
	return fmt.Sprint(parts)
}

func sameEvents(a []ontology.Event, b []ev) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Node != b[i].node || a[i].Epoch != b[i].ep {
			return false
		}
	}
	return true
}

func sameRoute(a []ontology.Hop, b []hop) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Node != b[i].node || a[i].Epoch != b[i].ep {
			return false
		}
	}
	return true
}

// runReal 对产品 facade 执行一条随机操作。
func runReal(m *ontology.Manager, c randomOp) ([]ontology.Event, int64, []ontology.Hop, error) {
	switch c.kind {
	case 0:
		return nil, 0, nil, m.AddNode(c.node, c.k)
	case 1:
		return nil, 0, nil, m.RemoveNode(c.node)
	case 2:
		evs, err := m.Bind(c.node, c.parent)
		return evs, 0, nil, err
	case 3:
		evs, err := m.Unbind(c.node)
		return evs, 0, nil, err
	case 4:
		evs, ep, err := m.Online(c.node, c.via, c.viaEp)
		return evs, ep, nil, err
	case 5:
		evs, err := m.Offline(c.node, c.epoch)
		return evs, 0, nil, err
	default:
		hops, err := m.Route(c.node)
		return nil, 0, hops, err
	}
}

func runNaive(md *naiveModel, c randomOp) naiveResult {
	switch c.kind {
	case 0:
		return md.add(c.node, c.k)
	case 1:
		return md.remove(c.node)
	case 2:
		return md.bind(c.node, c.parent)
	case 3:
		return md.unbind(c.node)
	case 4:
		return md.online(c.node, c.via, c.viaEp)
	case 5:
		return md.offline(c.node, c.epoch)
	default:
		return md.route(c.node)
	}
}

// genOp 以 rng 生成一条覆盖成功/拒绝两条路径的随机操作。
func genOp(rng *rand.Rand, names []string, md *naiveModel) randomOp {
	mkName := func() string { return names[rng.Intn(len(names))] }

	if len(names) < 10 && rng.Intn(10) < 3 {
		n := "n" + strconv.Itoa(len(names))
		return randomOp{kind: 0, node: n, k: ontology.Kind(1 + rng.Intn(2))}
	}
	if len(names) == 0 {
		return randomOp{kind: 0, node: "n0", k: ontology.KindGateway}
	}
	node := mkName()
	switch rng.Intn(12) {
	case 0:
		// 偶发非法名，验证 ErrInvalid 优先于一切。
		return randomOp{kind: rng.Intn(6) + 1, node: []string{"", strings.Repeat("x", 65)}[rng.Intn(2)]}
	case 1:
		return randomOp{kind: 1, node: node}
	case 2, 3:
		return randomOp{kind: 2, node: node, parent: mkName()}
	case 4:
		return randomOp{kind: 3, node: node}
	case 5, 6, 7:
		c := randomOp{kind: 4, node: node}
		if md.kind[node] == ontology.KindGateway && md.parent[node] == "" && rng.Intn(2) == 0 {
			if rng.Intn(10) == 0 {
				c.viaEp = int64(1 + rng.Intn(3)) // 直连却带纪元：ErrInvalid
			}
			return c
		}
		if p := md.parent[node]; p != "" {
			c.via = p
			if ep, live := md.liveEp[p]; live && rng.Intn(8) != 0 {
				c.viaEp = ep
			} else {
				c.viaEp = int64(1 + rng.Intn(6))
			}
		} else if rng.Intn(2) == 0 {
			c.via = mkName()
			c.viaEp = int64(rng.Intn(4))
		}
		return c
	case 8, 9:
		c := randomOp{kind: 5, node: node}
		if ep, live := md.liveEp[node]; live && rng.Intn(3) != 0 {
			c.epoch = ep
		} else {
			c.epoch = int64(rng.Intn(6))
		}
		if rng.Intn(20) == 0 {
			c.epoch = -1
		}
		return c
	default:
		return randomOp{kind: 6, node: node}
	}
}

// TestRandomAgainstNaive：1500 组随机操作序列逐步对照朴素模型，
// -v 时打印每条输入、输出与判定依据（错误哨兵）。
func TestRandomAgainstNaive(t *testing.T) {
	const groups = 1500
	const steps = 40
	for gi := 0; gi < groups; gi++ {
		rng := rand.New(rand.NewSource(int64(gi*7919 + 11)))
		cmax := 1 + rng.Intn(4)
		m, err := ontology.New(cmax)
		if err != nil {
			t.Fatal(err)
		}
		md := newNaive(cmax)
		names := []string{}
		for si := 0; si < steps; si++ {
			c := genOp(rng, names, md)
			if c.kind == 0 && c.node == "n"+strconv.Itoa(len(names)) {
				names = append(names, c.node)
			}
			want := runNaive(md, c)
			gotEvs, gotEp, gotRoute, gotErr := runReal(m, c)
			verdict := "match"
			if !errors.Is(gotErr, want.err) {
				verdict = fmt.Sprintf("ERR-MISMATCH real=%s naive=%s", errName(gotErr), errName(want.err))
			}
			t.Logf("[g=%04d s=%02d] %s in={node=%q kind=%d parent=%q via=%q viaEp=%d epoch=%d} => real{err=%s ep=%d ev=%s route=%s} naive{err=%s ep=%d ev=%v route=%v} %s",
				gi, si, opName(c.kind), c.node, c.k, c.parent, c.via, c.viaEp, c.epoch,
				errName(gotErr), gotEp, eventsName(gotEvs), routeName(gotRoute),
				errName(want.err), want.epoch, want.events, want.route, verdict)
			if !errors.Is(gotErr, want.err) {
				t.Fatalf("group %d step %d %s: %s", gi, si, opName(c.kind), verdict)
			}
			if gotErr == nil {
				switch c.kind {
				case 2, 3, 5:
					if !sameEvents(gotEvs, want.events) {
						t.Fatalf("group %d step %d: events real=%v naive=%v", gi, si, gotEvs, want.events)
					}
					if want.touch > 0 {
						if got := m.CascadeTouched(); got != want.touch {
							t.Fatalf("group %d step %d: cascade touched real=%d naive=%d", gi, si, got, want.touch)
						}
					}
				case 4:
					if gotEp != want.epoch {
						t.Fatalf("group %d step %d: epoch real=%d naive=%d", gi, si, gotEp, want.epoch)
					}
					if !sameEvents(gotEvs, want.events) {
						t.Fatalf("group %d step %d: takeover events real=%v naive=%v", gi, si, gotEvs, want.events)
					}
				case 6:
					if !sameRoute(gotRoute, want.route) {
						t.Fatalf("group %d step %d: route real=%v naive=%v", gi, si, gotRoute, want.route)
					}
					if m.RouteTouched() != want.touch {
						t.Fatalf("group %d step %d: route touched real=%d naive=%d", gi, si, m.RouteTouched(), want.touch)
					}
				}
			}
			md.assertInvariants(t, gi, si, m)
		}
	}
}

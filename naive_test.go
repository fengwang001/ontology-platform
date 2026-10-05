package ontology

import (
	"errors"
	"fmt"
	"math"
	"math/rand"
	"reflect"
	"sort"
	"testing"

	"ontology/forward"
)

// naive is an independent, deliberately simple reference implementation.
// It advances the world second by second: at every second it emits the
// queries due at that instant (before applying any operation stamped at
// that instant), and it decides membership liveness by plain exp>now
// comparisons with full scans. The differential test replays identical
// random operation sequences against Switch and naive and compares every
// output.
type naive struct {
	ports        int
	ownIP        uint32
	qi           int64
	rb           int64
	lmqi         int64
	gmi          int64
	oqpi         int64
	fastLeave    []bool
	floodUnknown bool
	gmax         int
	lp           int

	cur     int64 // world advanced up to here; -1 means before time 0
	maxNow  int64
	members map[uint32]map[int]int64
	routers map[int]int64
	querier bool
	oq      int64
	nextGen int64
	spec    map[uint32]map[int]*naiveSpec
	byTime  map[int64][]naiveRef
	pending []Query // emitted, not yet drained
}

type naiveSpec struct {
	times []int64
	cut   int64 // queries with time > cut are cancelled
}

type naiveRef struct {
	group uint32
	port  int
	sp    *naiveSpec
	time  int64
}

func newNaive(P int, ownIP uint32, QI, QRI, Rb, LMQI int64, fastLeave []bool, floodUnknown bool, Gmax, Lp int) *naive {
	return &naive{
		ports:        P,
		ownIP:        ownIP,
		qi:           QI,
		rb:           Rb,
		lmqi:         LMQI,
		gmi:          Rb*QI + QRI,
		oqpi:         Rb*QI + QRI/2,
		fastLeave:    fastLeave,
		floodUnknown: floodUnknown,
		gmax:         Gmax,
		lp:           Lp,
		cur:          -1,
		members:      make(map[uint32]map[int]int64),
		routers:      make(map[int]int64),
		querier:      true,
		spec:         make(map[uint32]map[int]*naiveSpec),
		byTime:       make(map[int64][]naiveRef),
	}
}

func (n *naive) clock(now int64) error {
	if now < 0 || now > maxNow {
		return ErrParam
	}
	if now < n.maxNow {
		return ErrClock
	}
	return nil
}

func (n *naive) alive(group uint32, port int, now int64) bool {
	exp, ok := n.members[group][port]
	return ok && now < exp
}

func (n *naive) emit(q Query) { n.pending = append(n.pending, q) }

// advance moves the world second by second up to to, emitting every query
// whose time has come.
func (n *naive) advance(to int64) {
	for t := n.cur + 1; t <= to; t++ {
		if !n.querier && n.oq <= t {
			n.querier = true
			n.nextGen = t
		}
		if n.querier && n.nextGen == t {
			n.emit(Query{Time: t, Kind: General})
			n.nextGen += n.qi
		}
		for _, ref := range n.byTime[t] {
			if n.spec[ref.group][ref.port] == ref.sp && ref.time <= ref.sp.cut &&
				n.alive(ref.group, ref.port, ref.time) {
				n.emit(Query{Time: ref.time, Kind: Specific, Group: ref.group, Port: ref.port})
			}
		}
		delete(n.byTime, t)
	}
	n.cur = to
}

// schedule registers last-member queries; those already due are emitted
// at once, the rest are indexed by their time.
func (n *naive) schedule(group uint32, port int, times []int64) {
	sp := &naiveSpec{times: times, cut: math.MaxInt64}
	if n.spec[group] == nil {
		n.spec[group] = make(map[int]*naiveSpec)
	}
	n.spec[group][port] = sp
	for _, tm := range times {
		if tm <= n.cur {
			if n.alive(group, port, tm) {
				n.emit(Query{Time: tm, Kind: Specific, Group: group, Port: port})
			}
		} else {
			n.byTime[tm] = append(n.byTime[tm], naiveRef{group: group, port: port, sp: sp, time: tm})
		}
	}
}

func (n *naive) liveGroups(now int64) int {
	cnt := 0
	for _, m := range n.members {
		for _, exp := range m {
			if now < exp {
				cnt++
				break
			}
		}
	}
	return cnt
}

func (n *naive) groupLive(group uint32, now int64) bool {
	for _, exp := range n.members[group] {
		if now < exp {
			return true
		}
	}
	return false
}

func (n *naive) portCount(port int, now int64) int {
	cnt := 0
	for _, m := range n.members {
		for p, exp := range m {
			if p == port && now < exp {
				cnt++
			}
		}
	}
	return cnt
}

func (n *naive) routerPorts(now int64) []int {
	var out []int
	for p, exp := range n.routers {
		if now < exp {
			out = append(out, p)
		}
	}
	sort.Ints(out)
	return out
}

func (n *naive) routersMinus(port int, now int64) []int {
	var out []int
	for _, p := range n.routerPorts(now) {
		if p != port {
			out = append(out, p)
		}
	}
	return out
}

func (n *naive) Report(port int, group uint32, now int64) ([]int, error) {
	if port < 1 || port > n.ports || !forward.Valid(group) {
		return nil, ErrParam
	}
	if err := n.clock(now); err != nil {
		return nil, err
	}
	if forward.LocalLink(group) {
		return nil, ErrLocalLink
	}
	n.advance(now)
	if n.alive(group, port, now) {
		n.members[group][port] = now + n.gmi
		if sp := n.spec[group][port]; sp != nil && now < sp.cut {
			sp.cut = now
		}
		n.maxNow = now
		return n.routersMinus(port, now), nil
	}
	if n.portCount(port, now) >= n.lp {
		return nil, ErrPortLimit
	}
	if !n.groupLive(group, now) && n.liveGroups(now) >= n.gmax {
		return nil, ErrGroupLimit
	}
	if n.members[group] == nil {
		n.members[group] = make(map[int]int64)
	}
	n.members[group][port] = now + n.gmi
	if n.spec[group] != nil {
		delete(n.spec[group], port) // new generation: stale schedules die
	}
	n.maxNow = now
	return n.routersMinus(port, now), nil
}

func (n *naive) Leave(port int, group uint32, now int64) error {
	if port < 1 || port > n.ports || !forward.Valid(group) {
		return ErrParam
	}
	if err := n.clock(now); err != nil {
		return err
	}
	if forward.LocalLink(group) {
		return ErrLocalLink
	}
	n.advance(now)
	if !n.alive(group, port, now) {
		return ErrNonMember
	}
	if n.fastLeave[port-1] {
		delete(n.members[group], port)
		if n.spec[group] != nil {
			delete(n.spec[group], port)
		}
		n.maxNow = now
		return nil
	}
	if n.querier {
		exp := n.members[group][port]
		pending := false
		if sp := n.spec[group][port]; sp != nil {
			for _, tm := range sp.times {
				if tm > now && tm <= sp.cut && tm < exp {
					pending = true
					break
				}
			}
		}
		if !pending {
			if newExp := now + n.rb*n.lmqi; newExp < exp {
				exp = newExp
				n.members[group][port] = exp
			}
			times := make([]int64, n.rb)
			for i := range times {
				times[i] = now + int64(i)*n.lmqi
			}
			n.schedule(group, port, times)
		}
	}
	n.maxNow = now
	return nil
}

func (n *naive) Query(port int, srcIP uint32, now int64) error {
	if port < 1 || port > n.ports || srcIP == 0 || srcIP == n.ownIP {
		return ErrParam
	}
	if err := n.clock(now); err != nil {
		return err
	}
	n.advance(now)
	n.routers[port] = now + n.oqpi
	if srcIP < n.ownIP {
		if n.querier {
			n.querier = false
			for _, m := range n.spec {
				for _, sp := range m {
					if now < sp.cut {
						sp.cut = now
					}
				}
			}
		}
		n.oq = now + n.oqpi
	}
	n.maxNow = now
	return nil
}

func (n *naive) Drain(now int64) ([]Query, error) {
	if err := n.clock(now); err != nil {
		return nil, err
	}
	n.advance(now)
	n.maxNow = now
	out := n.pending
	n.pending = nil
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.Time != b.Time {
			return a.Time < b.Time
		}
		if (a.Kind == General) != (b.Kind == General) {
			return a.Kind == General
		}
		if a.Group != b.Group {
			return a.Group < b.Group
		}
		return a.Port < b.Port
	})
	return out, nil
}

func (n *naive) Forward(group uint32, inPort int, now int64) ([]int, error) {
	if inPort < 1 || inPort > n.ports || !forward.Valid(group) {
		return nil, ErrParam
	}
	if err := n.clock(now); err != nil {
		return nil, err
	}
	n.advance(now)
	n.maxNow = now
	if forward.LocalLink(group) {
		return allBut(n.ports, inPort), nil
	}
	set := make(map[int]bool)
	hasMember := false
	for p, exp := range n.members[group] {
		if now < exp {
			set[p] = true
			hasMember = true
		}
	}
	if !hasMember && n.floodUnknown {
		return allBut(n.ports, inPort), nil
	}
	for p, exp := range n.routers {
		if now < exp {
			set[p] = true
		}
	}
	delete(set, inPort)
	var out []int
	for p := range set {
		out = append(out, p)
	}
	sort.Ints(out)
	return out, nil
}

func allBut(ports, inPort int) []int {
	var out []int
	for p := 1; p <= ports; p++ {
		if p != inPort {
			out = append(out, p)
		}
	}
	return out
}

// TestNaiveDifferential replays 1500 random operation sequences against
// both Switch and the per-second naive simulation, comparing every
// output. Each operation, its output and the deciding rule are logged.
func TestNaiveDifferential(t *testing.T) {
	for seq := 0; seq < 1500; seq++ {
		seq := seq
		t.Run(fmt.Sprintf("seq%04d", seq), func(t *testing.T) {
			runSequence(t, int64(seq)*7919+13)
		})
	}
}

type seqParams struct {
	P         int
	ownIP     uint32
	QI, QRI   int64
	Rb, LMQI  int64
	fastLeave []bool
	flood     bool
	Gmax, Lp  int
	groupPool []uint32
}

func genParams(r *rand.Rand) seqParams {
	p := seqParams{
		P:    2 + r.Intn(4),
		QI:   int64(20 + r.Intn(200)),
		Rb:   int64(1 + r.Intn(3)),
		LMQI: int64(1 + r.Intn(5)),
		Gmax: 2 + r.Intn(6),
		Lp:   1 + r.Intn(3),
	}
	p.ownIP = uint32(1 + r.Intn(10))
	p.QRI = int64(1 + r.Intn(int(p.QI-1)))
	p.fastLeave = make([]bool, p.P)
	for i := range p.fastLeave {
		p.fastLeave[i] = r.Intn(4) == 0
	}
	p.flood = r.Intn(2) == 0
	for i := 0; i < p.Gmax+3; i++ {
		p.groupPool = append(p.groupPool, 0xE0000100+uint32(r.Intn(0x1000)))
	}
	return p
}

func (p seqParams) randGroup(r *rand.Rand) uint32 {
	switch r.Intn(100) {
	case 0, 1, 2:
		return 0xE0000000 + uint32(r.Intn(256)) // link-local
	case 3, 4:
		return 0xF0000000 + uint32(r.Intn(0x1000)) // out of range
	case 5:
		return uint32(r.Intn(0xE0000000)) // out of range
	case 6, 7:
		return 0xE0000100 + uint32(r.Intn(0xEFFF00)) // any valid group
	default:
		return p.groupPool[r.Intn(len(p.groupPool))]
	}
}

func runSequence(t *testing.T, seed int64) {
	r := rand.New(rand.NewSource(seed))
	p := genParams(r)
	sw, err := New(p.P, p.ownIP, p.QI, p.QRI, p.Rb, p.LMQI, p.fastLeave, p.flood, p.Gmax, p.Lp)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	nv := newNaive(p.P, p.ownIP, p.QI, p.QRI, p.Rb, p.LMQI, p.fastLeave, p.flood, p.Gmax, p.Lp)
	t.Logf("seed=%d params=%+v", seed, p)

	now := int64(0)
	nOps := 30 + r.Intn(30)
	for op := 0; op < nOps; op++ {
		now += int64(r.Intn(int(2*p.QI + 2)))
		port := 1 + r.Intn(p.P)
		if r.Intn(20) == 0 {
			port = r.Intn(p.P + 2) // sometimes 0 or P+1
		}
		group := p.randGroup(r)
		srcIP := uint32(1 + r.Intn(12))
		if r.Intn(10) == 0 {
			srcIP = uint32(r.Intn(2)) * p.ownIP // 0 or ownIP
		}
		switch kind := r.Intn(100); {
		case kind < 30:
			gotP, gotE := sw.Report(port, group, now)
			wantP, wantE := nv.Report(port, group, now)
			checkErr(t, op, "Report", gotE, wantE)
			checkPorts(t, op, "Report", gotP, wantP)
			t.Logf("op%d Report(%d,%#x,%d) -> ports=%v err=%v (依据: 成员刷新/新建, 返回路由器端口∖port)", op, port, group, now, gotP, gotE)
		case kind < 50:
			gotE := sw.Leave(port, group, now)
			wantE := nv.Leave(port, group, now)
			checkErr(t, op, "Leave", gotE, wantE)
			t.Logf("op%d Leave(%d,%#x,%d) -> err=%v (依据: fastLeave立即移除; 查询器降exp并计划末成员查询; 否则无效果)", op, port, group, now, gotE)
		case kind < 65:
			gotE := sw.Query(port, srcIP, now)
			wantE := nv.Query(port, srcIP, now)
			checkErr(t, op, "Query", gotE, wantE)
			t.Logf("op%d Query(%d,%d,%d) -> err=%v (依据: 端口记路由器端口; srcIP<ownIP让位并刷新oq)", op, port, srcIP, now, gotE)
		case kind < 85:
			gotP, gotE := sw.Forward(group, port, now)
			wantP, wantE := nv.Forward(group, port, now)
			checkErr(t, op, "Forward", gotE, wantE)
			checkPorts(t, op, "Forward", gotP, wantP)
			t.Logf("op%d Forward(%#x,%d,%d) -> ports=%v err=%v (依据: 本地链路泛洪; 成员∪路由器∖inPort; 无成员按floodUnknown)", op, group, port, now, gotP, gotE)
		default:
			gotQ, gotE := sw.Drain(now)
			wantQ, wantE := nv.Drain(now)
			checkErr(t, op, "Drain", gotE, wantE)
			checkQueries(t, op, gotQ, wantQ)
			t.Logf("op%d Drain(%d) -> %v err=%v (依据: (时刻,通用先于特定,组,端口) 升序)", op, now, gotQ, gotE)
		}
	}
	// Final drain far in the future flushes every remaining query.
	gotQ, _ := sw.Drain(now + 4*(p.Rb*p.QI+p.QI) + 100)
	wantQ, _ := nv.Drain(now + 4*(p.Rb*p.QI+p.QI) + 100)
	checkQueries(t, nOps, gotQ, wantQ)
	t.Logf("final Drain -> %v", gotQ)
}

func checkErr(t *testing.T, op int, what string, got, want error) {
	t.Helper()
	if (got == nil) != (want == nil) || (got != nil && !errors.Is(got, want)) {
		t.Fatalf("op%d %s: err got=%v want=%v", op, what, got, want)
	}
}

func checkPorts(t *testing.T, op int, what string, got, want []int) {
	t.Helper()
	if !reflect.DeepEqual(normPorts(got), normPorts(want)) {
		t.Fatalf("op%d %s: ports got=%v want=%v", op, what, got, want)
	}
}

func checkQueries(t *testing.T, op int, got, want []Query) {
	t.Helper()
	if !reflect.DeepEqual(normQueries(got), normQueries(want)) {
		t.Fatalf("op%d Drain: queries got=%v want=%v", op, got, want)
	}
}

package charger_test

import (
	"bytes"
	"errors"
	"fmt"
	"math/rand"
	"sort"
	"strings"
	"sync"
	"testing"

	"ontology/charger"
)

// naive 是按题意逐秒、逐事件重写的独立朴素参考模型。
// 它不引用实现内部任何逻辑，只用来与 charger.Station 做差分。
type naiveSession struct {
	id             int64
	port           string
	plugAt         int64
	need, max, min int
	prio           charger.Priority
	charged        int
	power          int
	state          charger.State
	selfCap        int
}

type naiveCap struct {
	at  int64
	cap int
}

type naive struct {
	now      int64
	totalCap int
	portCap  map[string]int
	portSess map[string]*naiveSession
	sessions map[int64]*naiveSession
	nextID   int64
	changes  []naiveCap
}

func newNaive(totalCap int, ports []charger.Port) *naive {
	n := &naive{
		totalCap: totalCap,
		portCap:  map[string]int{},
		portSess: map[string]*naiveSession{},
		sessions: map[int64]*naiveSession{},
	}
	for _, p := range ports {
		n.portCap[p.ID] = p.Cap
	}
	return n
}

type opKind int

const (
	opPlug opKind = iota
	opUnplug
	opSetCap
	opSetPrio
	opAdvance
)

type op struct {
	kind                   opKind
	at                     int64
	port                   string
	id                     int64
	need, max, min, capVal int
	prio                   charger.Priority
}

type opResult struct {
	id      int64
	charged int
	err     error
	fills   []charger.FillEvent
}

func validPrio(p charger.Priority) bool { return p == charger.Normal || p == charger.Prefer }

// apply 返回 (新会话ID/已充电量, 错误, 推进充满事件)。
func (n *naive) apply(o op) (int64, int, error, []charger.FillEvent) {
	switch o.kind {
	case opPlug:
		if o.at < 0 || o.need <= 0 || o.max <= 0 || o.min < 0 || o.min > o.max || !validPrio(o.prio) {
			return 0, 0, charger.ErrInvalidArg, nil
		}
		if o.at < n.now {
			return 0, 0, charger.ErrClockBack, nil
		}
		if _, ok := n.portCap[o.port]; !ok {
			return 0, 0, charger.ErrPortMissing, nil
		}
		if _, busy := n.portSess[o.port]; busy {
			return 0, 0, charger.ErrPortOccupied, nil
		}
		// 校验通过即预占 ID（在 advance 前），与实现侧顺序一致，
		// 保证推进产生的充满事件引用稳定 ID。
		n.nextID++
		id := n.nextID
		n.advance(o.at)
		s := &naiveSession{
			id: id, port: o.port, plugAt: o.at, need: o.need,
			max: o.max, min: o.min, prio: o.prio, state: charger.StateWaiting,
			selfCap: minI(o.max, n.portCap[o.port]),
		}
		n.sessions[s.id] = s
		n.portSess[o.port] = s
		n.allocate()
		return s.id, 0, nil, nil

	case opUnplug:
		if o.at < 0 {
			return 0, 0, charger.ErrInvalidArg, nil
		}
		if o.at < n.now {
			return 0, 0, charger.ErrClockBack, nil
		}
		s, ok := n.sessions[o.id]
		if !ok {
			return 0, 0, charger.ErrNoSession, nil
		}
		n.advance(o.at)
		if _, still := n.portSess[s.port]; !still || n.portSess[s.port] != s {
			return 0, 0, charger.ErrNoSession, nil
		}
		c := s.charged
		s.state = charger.StateEnded
		s.power = 0
		delete(n.portSess, s.port)
		n.allocate()
		return 0, c, nil, nil

	case opSetCap:
		if o.at < 0 || o.capVal <= 0 {
			return 0, 0, charger.ErrInvalidArg, nil
		}
		if o.at < n.now {
			return 0, 0, charger.ErrClockBack, nil
		}
		if o.at == n.now {
			n.totalCap = o.capVal
			n.allocate()
			return 0, 0, nil, nil
		}
		n.changes = append(n.changes, naiveCap{at: o.at, cap: o.capVal})
		return 0, 0, nil, nil

	case opSetPrio:
		if o.at < 0 || !validPrio(o.prio) {
			return 0, 0, charger.ErrInvalidArg, nil
		}
		if o.at < n.now {
			return 0, 0, charger.ErrClockBack, nil
		}
		s, ok := n.sessions[o.id]
		if !ok {
			return 0, 0, charger.ErrNoSession, nil
		}
		if s.state == charger.StateFull || s.state == charger.StateEnded {
			return 0, 0, charger.ErrWrongState, nil
		}
		n.advance(o.at)
		if s.state == charger.StateFull || s.state == charger.StateEnded {
			return 0, 0, charger.ErrWrongState, nil
		}
		s.prio = o.prio
		n.allocate()
		return 0, 0, nil, nil

	case opAdvance:
		if o.at < 0 {
			return 0, 0, charger.ErrInvalidArg, nil
		}
		if o.at < n.now {
			return 0, 0, charger.ErrClockBack, nil
		}
		fills := n.advance(o.at)
		n.allocate()
		return 0, 0, nil, fills
	}
	return 0, 0, nil, nil
}

// advance 逐秒推进：每秒先按当前功率计 1 秒并夹到需求，
// 再处理充满（同时刻按插枪先后逐台重分），再生效上限变更并重分。
func (n *naive) advance(target int64) []charger.FillEvent {
	var events []charger.FillEvent
	for n.now < target {
		n.now++
		active := n.activeOrdered()
		for _, s := range active {
			if s.state == charger.StateCharging {
				s.charged += s.power
				if s.charged > s.need {
					s.charged = s.need
				}
			}
		}
		var full []*naiveSession
		for _, s := range active {
			if s.state == charger.StateCharging && s.charged >= s.need {
				full = append(full, s)
			}
		}
		sort.SliceStable(full, func(i, j int) bool {
			if full[i].plugAt != full[j].plugAt {
				return full[i].plugAt < full[j].plugAt
			}
			return full[i].id < full[j].id
		})
		for _, s := range full {
			s.charged = s.need
			s.power = 0
			s.state = charger.StateFull
			events = append(events, charger.FillEvent{SessionID: s.id, At: n.now})
			n.allocate()
		}
		var kept []naiveCap
		applied := false
		for _, c := range n.changes {
			if c.at == n.now {
				n.totalCap = c.cap
				applied = true
			} else {
				kept = append(kept, c)
			}
		}
		n.changes = kept
		if applied {
			n.allocate()
		}
	}
	return events
}

// allocate 朴素分配：池内重复整份重分水填充 + 挑受害者，直到稳定。
func (n *naive) allocate() {
	var prefer, normal []*naiveSession
	for _, s := range n.activeOrdered() {
		if s.state == charger.StateFull {
			continue
		}
		if s.prio == charger.Prefer {
			prefer = append(prefer, s)
		} else {
			normal = append(normal, s)
		}
	}
	used := n.allocClass(prefer, n.totalCap)
	left := n.totalCap - used
	if left < 0 {
		left = 0
	}
	n.allocClass(normal, left)
}

// activeOrdered 按插枪先后（并列按会话 ID）返回在站车辆，消除 map 随机序。
func (n *naive) activeOrdered() []*naiveSession {
	list := make([]*naiveSession, 0, len(n.portSess))
	for _, s := range n.portSess {
		list = append(list, s)
	}
	sort.SliceStable(list, func(i, j int) bool {
		if list[i].plugAt != list[j].plugAt {
			return list[i].plugAt < list[j].plugAt
		}
		return list[i].id < list[j].id
	})
	return list
}

func (n *naive) allocClass(pool []*naiveSession, budget int) int {
	orig := map[int64]charger.State{}
	for _, s := range pool {
		orig[s.id] = s.state
	}
	got := naiveWaterfill(pool, budget)
	for {
		var bad []*naiveSession
		for i, s := range pool {
			if got[i] < s.min {
				bad = append(bad, s)
			}
		}
		if len(bad) == 0 {
			for i, s := range pool {
				s.power = got[i]
				if got[i] > 0 {
					s.state = charger.StateCharging
				} else {
					s.state = charger.StateWaiting
				}
			}
			t := 0
			for _, x := range got {
				t += x
			}
			return t
		}
		v := bad[0]
		for _, s := range bad[1:] {
			ws, wv := orig[s.id] == charger.StateWaiting, orig[v.id] == charger.StateWaiting
			if ws != wv {
				if ws {
					v = s
				}
			} else if s.plugAt > v.plugAt {
				v = s
			}
		}
		v.power = 0
		v.state = charger.StateWaiting
		next := make([]*naiveSession, 0, len(pool)-1)
		for _, s := range pool {
			if s != v {
				next = append(next, s)
			}
		}
		pool = next
		got = naiveWaterfill(pool, budget)
	}
}

// naiveWaterfill 用“每轮给未封顶者各 +1；最后一轮放不下时余量按
// 插枪先后逐台 +1”的最直白循环实现，与实现的分档封顶算法独立编写。
func naiveWaterfill(pool []*naiveSession, budget int) []int {
	got := make([]int, len(pool))
	if len(pool) == 0 || budget <= 0 {
		return got
	}
	rem := budget
	for rem > 0 {
		var live []int
		for j, s := range pool {
			if got[j] < s.selfCap {
				live = append(live, j)
			}
		}
		if len(live) == 0 {
			break
		}
		if rem < len(live) {
			sort.SliceStable(live, func(a, b int) bool {
				if pool[live[a]].plugAt != pool[live[b]].plugAt {
					return pool[live[a]].plugAt < pool[live[b]].plugAt
				}
				return pool[live[a]].id < pool[live[b]].id
			})
			for k := 0; k < rem; k++ {
				got[live[k]]++
			}
			break
		}
		for _, j := range live {
			got[j]++
		}
		rem -= len(live)
	}
	return got
}

func minI(a, b int) int {
	if a < b {
		return a
	}
	return b
}

type bufLogger struct{ buf bytes.Buffer }

func (l *bufLogger) Log(s string) { l.buf.WriteString(s + "\n") }

// modelState 是差分比较用的扁平化状态。
type sessState struct {
	id                             int64
	state                          charger.State
	power, charged, need, min, max int
	prio                           charger.Priority
	fullAt, plugAt                 int64
	port                           string
}

func realStates(st *charger.Station) map[int64]sessState {
	st0 := st.Status()
	m := map[int64]sessState{}
	for _, s := range st0.Sessions {
		m[s.ID] = sessState{
			id: s.ID, state: s.State, power: s.Power, charged: s.Charged,
			need: s.Need, min: s.MinP, max: s.MaxP, prio: s.Prio,
			fullAt: s.FullAt, plugAt: s.PlugAt, port: s.PortID,
		}
	}
	return m
}

func naiveStates(n *naive) map[int64]sessState {
	m := map[int64]sessState{}
	for id, s := range n.sessions {
		st := sessState{
			id: s.id, state: s.state, power: s.power, charged: s.charged,
			need: s.need, min: s.min, max: s.max, prio: s.prio,
			fullAt: -1, plugAt: s.plugAt, port: s.port,
		}
		m[id] = st
	}
	return m
}

func sameStates(a, b map[int64]sessState) (string, bool) {
	if len(a) != len(b) {
		return fmt.Sprintf("会话数 %d vs %d", len(a), len(b)), false
	}
	for id, x := range a {
		y, ok := b[id]
		if !ok {
			return fmt.Sprintf("会话 %d 缺失于模型", id), false
		}
		if x.state != y.state || x.power != y.power || x.charged != y.charged ||
			x.prio != y.prio || x.port != y.port ||
			x.need != y.need || x.min != y.min || x.max != y.max {
			return fmt.Sprintf("id=%d 实现=%+v 模型=%+v", id, x, y), false
		}
	}
	return "", true
}

func checkInvariants(t *testing.T, st *charger.Station) {
	t.Helper()
	st0 := st.Status()
	sum := 0
	ports := map[string]bool{}
	for _, s := range st0.Sessions {
		if s.State == charger.StateEnded {
			continue
		}
		if ports[s.PortID] {
			t.Fatalf("接口 %s 被多车占用", s.PortID)
		}
		ports[s.PortID] = true
		if s.State == charger.StateCharging {
			if s.Power < s.MinP {
				t.Fatalf("id=%d 充电功率 %d < min %d", s.ID, s.Power, s.MinP)
			}
			if s.Power > s.MaxP {
				t.Fatalf("id=%d 功率超车辆上限", s.ID)
			}
			if s.Charged > s.Need {
				t.Fatalf("id=%d 电量超需求", s.ID)
			}
			sum += s.Power
		} else if s.Power != 0 {
			t.Fatalf("id=%d 状态=%v 但功率=%d", s.ID, s.State, s.Power)
		}
		if s.Charged > s.Need {
			t.Fatalf("id=%d 电量超需求", s.ID)
		}
	}
	if sum > st0.TotalCap {
		t.Fatalf("总功率 %d 超总上限 %d", sum, st0.TotalCap)
	}
}

func TestRandomDifferential(t *testing.T) {
	const trials = 1500
	for trial := 0; trial < trials; trial++ {
		rng := rand.New(rand.NewSource(int64(trial*7919 + 1)))
		runOneRandom(t, rng, false, trial)
	}
}

func TestRandomDifferentialLogged(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	runOneRandom(t, rng, true, -1)
}

func runOneRandom(t *testing.T, rng *rand.Rand, log bool, trial int) {
	t.Helper()
	nPorts := 1 + rng.Intn(4)
	var ports []charger.Port
	portNames := make([]string, 0, nPorts)
	for i := 0; i < nPorts; i++ {
		name := string(rune('A' + i))
		portNames = append(portNames, name)
		ports = append(ports, charger.Port{ID: name, Cap: 2 + rng.Intn(9)})
	}
	initialCap := 1 + rng.Intn(12)

	st, err := charger.NewStation(initialCap, ports)
	if err != nil {
		t.Fatal(err)
	}
	var lg *bufLogger
	if log {
		lg = &bufLogger{}
		st.SetLogger(lg)
	}
	n := newNaive(initialCap, ports)

	var logb strings.Builder
	fmt.Fprintf(&logb, "INIT cap=%d ports=%+v\n", initialCap, ports)

	realID := map[int64]int64{} // 朴素模型序号 -> 实现 ID（二者同种子下同步分配）
	var activeOps []int64

	nOps := 40 + rng.Intn(60)
	for k := 0; k < nOps; k++ {
		now := n.now
		// 推进占比稍高，让充满事件频发。
		choice := rng.Intn(100)
		var o op
		switch {
		case choice < 30:
			free := []string{}
			busy := map[string]bool{}
			for p, s := range n.portSess {
				if s != nil {
					busy[p] = true
				}
			}
			for _, p := range portNames {
				if !busy[p] {
					free = append(free, p)
					_ = free
				}
			}
			port := portNames[rng.Intn(len(portNames))] // 也允许故意打占用/不存在
			if rng.Intn(10) == 0 {
				port = "Z"
			}
			o = op{kind: opPlug, at: now + int64(rng.Intn(4)), port: port,
				need: 1 + rng.Intn(80), max: 1 + rng.Intn(8), min: rng.Intn(5),
				prio: charger.Priority(rng.Intn(2))}
			if o.min > o.max {
				o.min = o.max
			}
			if rng.Intn(12) == 0 {
				o.min = o.max + 1 // 故意非法
			}
		case choice < 45:
			o = op{kind: opAdvance, at: now + int64(rng.Intn(12))}
		case choice < 60:
			o = op{kind: opSetCap, at: now + int64(rng.Intn(8)), capVal: 1 + rng.Intn(14)}
			if rng.Intn(15) == 0 {
				o.capVal = 0 // 故意非法
			}
		case choice < 72:
			id := pickNaiveID(rng, n)
			o = op{kind: opSetPrio, id: id, at: now + int64(rng.Intn(4)),
				prio: charger.Priority(rng.Intn(2))}
		default:
			id := pickNaiveID(rng, n)
			o = op{kind: opUnplug, id: id, at: now + int64(rng.Intn(4))}
		}

		fmt.Fprintf(&logb, "OP%d %s\n", k, opString(o))

		var rID int64
		var rCharged int
		var rErr error
		var rFills []charger.FillEvent
		switch o.kind {
		case opPlug:
			id, e := st.Plug(charger.PlugRequest{
				At: o.at, PortID: o.port, Need: o.need, MaxP: o.max, MinP: o.min, Prio: o.prio,
			})
			rID, rErr = id, e
		case opUnplug:
			real := mapNaiveToReal(n, realID, o.id)
			c, e := st.Unplug(real, o.at)
			rCharged, rErr = c, e
		case opSetCap:
			rErr = st.SetCap(o.at, o.capVal)
		case opSetPrio:
			real := mapNaiveToReal(n, realID, o.id)
			rErr = st.SetPriority(real, o.at, o.prio)
		case opAdvance:
			rFills, rErr = st.AdvanceTo(o.at)
		}

		nID, nCharged, nErr, nFills := n.apply(o)

		if !sameErr(rErr, nErr) {
			t.Fatalf("trial=%d op=%s 错误不一致: 实现=%v 模型=%v\n日志:\n%s",
				int64(trial), opString(o), rErr, nErr, logb.String())
		}
		if rErr == nil {
			switch o.kind {
			case opPlug:
				realID[nID] = rID
				activeOps = append(activeOps, rID)
			case opUnplug:
				if rCharged != nCharged {
					t.Fatalf("拔枪电量不一致: %d vs %d\n%s", rCharged, nCharged, logb.String())
				}
			case opAdvance:
				if len(rFills) != len(nFills) {
					t.Fatalf("充满事件数不一致: %+v vs %+v\n%s", rFills, nFills, logb.String())
				}
				for i := range rFills {
					ri, ni := rFills[i], nFills[i]
					if ri.At != ni.At || realID[ni.SessionID] != ri.SessionID {
						t.Fatalf("充满事件不一致: %+v vs %+v\n%s", ri, ni, logb.String())
					}
				}
			}
		}

		// 状态全面对齐（朴素模型的 id 需映射）。
		rs := realStates(st)
		ns := naiveStates(n)
		mapped := map[int64]sessState{}
		for nid, v := range ns {
			rid := realID[nid]
			v.id = rid
			mapped[rid] = v
		}
		if msg, ok := sameStates(rs, mapped); !ok {
			dump := "REAL:\n"
			for _, v := range rs {
				dump += fmt.Sprintf("  %+v\n", v)
			}
			dump += "NAIVE:\n"
			for _, v := range mapped {
				dump += fmt.Sprintf("  %+v\n", v)
			}
			t.Fatalf("trial=%d 状态不一致: %s\nnow real=%d naive=%d\n日志:\n%s\n%s",
				int64(trial), msg, st.Status().Now, n.now, logb.String(), dump)
		}
		if st.Status().Now != n.now {
			t.Fatalf("时钟不一致: %d vs %d", st.Status().Now, n.now)
		}
		if st.Status().TotalCap != n.totalCap {
			t.Fatalf("总上限不一致: %d vs %d", st.Status().TotalCap, n.totalCap)
		}
		checkInvariants(t, st)
	}
	if log && lg != nil {
		t.Logf("操作日志:\n%s", lg.buf.String())
		t.Logf("判定日志:\n%s", logb.String())
	}
	_ = activeOps
}

func pickNaiveID(rng *rand.Rand, n *naive) int64 {
	if len(n.sessions) == 0 {
		return 1 + rng.Int63n(5) // 大概率不存在
	}
	ids := make([]int64, 0, len(n.sessions))
	for id := range n.sessions {
		ids = append(ids, id)
	}
	pick := ids[rng.Intn(len(ids))]
	if rng.Intn(8) == 0 {
		return pick + 1000 // 故意不存在
	}
	return pick
}

func mapNaiveToReal(n *naive, m map[int64]int64, nid int64) int64 {
	if rid, ok := m[nid]; ok {
		return rid
	}
	return nid // 不存在时传一个实现侧也不存在的 id
}

func sameErr(a, b error) bool {
	return (a == nil) == (b == nil)
}

func opString(o op) string {
	switch o.kind {
	case opPlug:
		return fmt.Sprintf("PLUG at=%d port=%s need=%d max=%d min=%d prio=%d",
			o.at, o.port, o.need, o.max, o.min, o.prio)
	case opUnplug:
		return fmt.Sprintf("UNPLUG id=%d at=%d", o.id, o.at)
	case opSetCap:
		return fmt.Sprintf("SETCAP at=%d cap=%d", o.at, o.capVal)
	case opSetPrio:
		return fmt.Sprintf("SETPRIO id=%d at=%d prio=%d", o.id, o.at, o.prio)
	default:
		return fmt.Sprintf("ADVANCE at=%d", o.at)
	}
}

var _ = sync.Mutex{}

func newTestStation(t testing.TB, total int, ports ...charger.Port) *charger.Station {
	t.Helper()
	st, err := charger.NewStation(total, ports)
	if err != nil {
		t.Fatalf("NewStation: %v", err)
	}
	return st
}

func TestMultipleFillsAndFillAtLastSecond(t *testing.T) {
	st := newTestStation(t, 100,
		charger.Port{ID: "A", Cap: 10}, charger.Port{ID: "B", Cap: 10})
	a, _ := st.Plug(charger.PlugRequest{At: 0, PortID: "A", Need: 30, MaxP: 10, MinP: 0})
	b, _ := st.Plug(charger.PlugRequest{At: 0, PortID: "B", Need: 20, MaxP: 10, MinP: 0})
	events, err := st.AdvanceTo(6)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 || events[0].SessionID != b || events[0].At != 2 ||
		events[1].SessionID != a || events[1].At != 3 {
		t.Fatalf("充满次序: %+v", events)
	}
	if s := snap(t, st, a); s.State != charger.StateFull || s.Charged != 30 || s.FullAt != 3 {
		t.Fatalf("A: %+v", s)
	}
	// 充满后仍占用接口：同口再插应报占用。
	if _, err := st.Plug(charger.PlugRequest{At: 6, PortID: "A", Need: 1, MaxP: 1, MinP: 0}); !errors.Is(err, charger.ErrPortOccupied) {
		t.Fatalf("充满车仍占口: %v", err)
	}

	st2 := newTestStation(t, 100, charger.Port{ID: "A", Cap: 10})
	c, _ := st2.Plug(charger.PlugRequest{At: 0, PortID: "A", Need: 50, MaxP: 10, MinP: 0})
	ev, err := st2.AdvanceTo(5)
	if err != nil {
		t.Fatal(err)
	}
	if len(ev) != 1 || ev[0].At != 5 || ev[0].SessionID != c {
		t.Fatalf("末尾充满: %+v", ev)
	}
	if s := snap(t, st2, c); s.State != charger.StateFull || s.Charged != 50 {
		t.Fatalf("末尾电量: %+v", s)
	}
}

func TestCapChangeSameInstantAsFill(t *testing.T) {
	st := newTestStation(t, 100,
		charger.Port{ID: "A", Cap: 10}, charger.Port{ID: "B", Cap: 10})
	a, _ := st.Plug(charger.PlugRequest{At: 0, PortID: "A", Need: 20, MaxP: 10, MinP: 0})
	b, _ := st.Plug(charger.PlugRequest{At: 0, PortID: "B", Need: 1000, MaxP: 10, MinP: 8})
	if err := st.SetCap(2, 5); err != nil {
		t.Fatal(err)
	}
	events, err := st.AdvanceTo(5)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].At != 2 || events[0].SessionID != a {
		t.Fatalf("同刻充满事件: %+v", events)
	}
	// A 在 t=2 释放后，上限 5 无法满足 B 的 min=8 -> B 等待。
	if s := snap(t, st, b); s.State != charger.StateWaiting || s.Power != 0 {
		t.Fatalf("上限同刻下降后B: %+v", s)
	}
	if st.Status().TotalCap != 5 {
		t.Fatal("总上限应为5")
	}
}

func TestUnplugReturnsChargedAndPriorityChange(t *testing.T) {
	st := newTestStation(t, 100, charger.Port{ID: "A", Cap: 10}, charger.Port{ID: "B", Cap: 10})
	a, _ := st.Plug(charger.PlugRequest{At: 0, PortID: "A", Need: 1000, MaxP: 10, MinP: 0})
	b, _ := st.Plug(charger.PlugRequest{At: 0, PortID: "B", Need: 1000, MaxP: 10, MinP: 0})
	if _, err := st.AdvanceTo(3); err != nil {
		t.Fatal(err)
	}
	// B 调高优先：优先类别内独得 10（其自身上限10）。
	if err := st.SetPriority(b, 3, charger.Prefer); err != nil {
		t.Fatal(err)
	}
	if s := snap(t, st, b); s.Power != 10 || s.State != charger.StateCharging {
		t.Fatalf("B升优先: %+v", s)
	}
	// 总上限 100 充足：优先 B 拿 10 后，普通 A 仍拿满自身上限 10。
	if s := snap(t, st, a); s.State != charger.StateCharging || s.Power != 10 {
		t.Fatalf("普通A仍应满功率: %+v", s)
	}
	if _, err := st.AdvanceTo(4); err != nil {
		t.Fatal(err)
	}
	c, err := st.Unplug(b, 4)
	if err != nil {
		t.Fatal(err)
	}
	if c != 40 { // 3 秒 *10 + 1 秒 *10
		t.Fatalf("B已充电量=%d want 40", c)
	}
	ca, err := st.Unplug(a, 4)
	if err != nil {
		t.Fatal(err)
	}
	if ca != 40 {
		t.Fatalf("A已充电量=%d want 40", ca)
	}
}

func TestPreferStarvesNormalUnderTightCap(t *testing.T) {
	// 总上限紧张时，优先车吃饱才轮到普通车：普通车因达不到下限而等待。
	st := newTestStation(t, 10, charger.Port{ID: "A", Cap: 10}, charger.Port{ID: "B", Cap: 10})
	a, _ := st.Plug(charger.PlugRequest{At: 0, PortID: "A", Need: 1000, MaxP: 10, MinP: 3})
	b, _ := st.Plug(charger.PlugRequest{At: 0, PortID: "B", Need: 1000, MaxP: 10, MinP: 4, Prio: charger.Prefer})
	if s := snap(t, st, b); s.Power != 10 {
		t.Fatalf("优先B: %+v", s)
	}
	if s := snap(t, st, a); s.State != charger.StateWaiting || s.Power != 0 {
		t.Fatalf("普通A应等待: %+v", s)
	}
}

func TestMinEqualShareAndRemainderOrder(t *testing.T) {
	st := newTestStation(t, 10,
		charger.Port{ID: "A", Cap: 6}, charger.Port{ID: "B", Cap: 6}, charger.Port{ID: "C", Cap: 6})
	var ids [3]int64
	for i, p := range []string{"A", "B", "C"} {
		id, err := st.Plug(charger.PlugRequest{At: 0, PortID: p, Need: 100, MaxP: 6, MinP: 0})
		if err != nil {
			t.Fatal(err)
		}
		ids[i] = id
	}
	want := []int{4, 3, 3}
	for i, id := range ids {
		if got := snap(t, st, id).Power; got != want[i] {
			t.Fatalf("s%d power=%d want %d", i, got, want[i])
		}
	}

	if err := st.SetCap(0, 9); err != nil {
		t.Fatal(err)
	}
	for _, id := range ids {
		if s := snap(t, st, id); s.Power != 3 || s.State != charger.StateCharging {
			t.Fatalf("id=%d power=%d state=%v", id, s.Power, s.State)
		}
	}

	if err := st.SetCap(0, 8); err != nil {
		t.Fatal(err)
	}
	// 三台 min=0，8 按插枪序分水 -> [3,3,2]，全部不低于下限 0，无人等待。
	want2 := []int{3, 3, 2}
	for i, id := range ids {
		if s := snap(t, st, id); s.Power != want2[i] || s.State != charger.StateCharging {
			t.Fatalf("cap=8 id=%d %+v want power=%d", id, s, want2[i])
		}
	}

}

func TestPreferArrivalAndNoSameClassPreemption(t *testing.T) {
	st := newTestStation(t, 6,
		charger.Port{ID: "A", Cap: 6}, charger.Port{ID: "B", Cap: 6},
		charger.Port{ID: "C", Cap: 6})
	n1, _ := st.Plug(charger.PlugRequest{At: 0, PortID: "A", Need: 100, MaxP: 6, MinP: 3})
	n2, _ := st.Plug(charger.PlugRequest{At: 0, PortID: "B", Need: 100, MaxP: 6, MinP: 3})
	if snap(t, st, n1).Power != 3 || snap(t, st, n2).Power != 3 {
		t.Fatal("两台普通各3")
	}
	// 新插同类别车 min=3：若三台齐分各 2，老车会跌破 3；
	// 连锁挂起时最晚插枪的原等待新车先退出，老车守住 3（滞回）。
	n3, _ := st.Plug(charger.PlugRequest{At: 1, PortID: "C", Need: 100, MaxP: 6, MinP: 3})
	if s := snap(t, st, n3); s.State != charger.StateWaiting || s.Power != 0 {
		t.Fatalf("同类别新车应等待: %+v", s)
	}
	if snap(t, st, n1).Power != 3 || snap(t, st, n2).Power != 3 {
		t.Fatal("同类别新车不得把老车挤到低于最低功率（滞回）")
	}
	if _, err := st.Unplug(n3, 2); err != nil {
		t.Fatal(err)
	}
	p1, _ := st.Plug(charger.PlugRequest{At: 2, PortID: "C", Need: 100, MaxP: 6, MinP: 4, Prio: charger.Prefer})
	if s := snap(t, st, p1); s.Power != 6 || s.State != charger.StateCharging {
		t.Fatalf("优先车: %+v", s)
	}
	if s := snap(t, st, n1); s.State != charger.StateWaiting || s.Power != 0 {
		t.Fatalf("普通车1应被挤: %+v", s)
	}
	if s := snap(t, st, n2); s.State != charger.StateWaiting || s.Power != 0 {
		t.Fatalf("普通车2应被挤: %+v", s)
	}
}

func TestCapDropChainAndRecovery(t *testing.T) {
	st := newTestStation(t, 10,
		charger.Port{ID: "A", Cap: 4}, charger.Port{ID: "B", Cap: 4},
		charger.Port{ID: "C", Cap: 4}, charger.Port{ID: "D", Cap: 4})
	var ids [4]int64
	for i, p := range []string{"A", "B", "C", "D"} {
		id, _ := st.Plug(charger.PlugRequest{At: 0, PortID: p, Need: 1000, MaxP: 4, MinP: 2})
		ids[i] = id
	}
	if err := st.SetCap(0, 5); err != nil {
		t.Fatal(err)
	}
	charging := 0
	for _, id := range ids {
		s := snap(t, st, id)
		switch s.State {
		case charger.StateCharging:
			charging++
			if s.Power < 2 {
				t.Fatalf("充电中低于min: %+v", s)
			}
		case charger.StateWaiting:
			if s.Power != 0 {
				t.Fatalf("等待者功率非0: %+v", s)
			}
		}
	}
	if charging != 2 {
		t.Fatalf("应剩2台充电, got %d", charging)
	}
	if err := st.SetCap(0, 10); err != nil {
		t.Fatal(err)
	}
	for _, id := range ids {
		if s := snap(t, st, id); s.State != charger.StateCharging || s.Power < 2 {
			t.Fatalf("恢复: %+v", s)
		}
	}
}

func snap(t *testing.T, st *charger.Station, id int64) charger.Snapshot {
	t.Helper()
	s, ok := st.SessionStatus(id)
	if !ok {
		t.Fatalf("session %d missing", id)
	}
	return s
}

func TestInvalidArgsAndRejectOrder(t *testing.T) {
	st := newTestStation(t, 10, charger.Port{ID: "A", Cap: 5})

	if _, err := st.Plug(charger.PlugRequest{At: 0, PortID: "A", Need: 0, MaxP: 5, MinP: 1}); !errors.Is(err, charger.ErrInvalidArg) {
		t.Fatalf("need=0: %v", err)
	}
	if _, err := st.Plug(charger.PlugRequest{At: 0, PortID: "A", Need: 10, MaxP: 5, MinP: 6}); !errors.Is(err, charger.ErrInvalidArg) {
		t.Fatalf("min>max: %v", err)
	}
	if _, err := st.Plug(charger.PlugRequest{At: -1, PortID: "A", Need: 10, MaxP: 5, MinP: 1}); !errors.Is(err, charger.ErrInvalidArg) {
		t.Fatalf("at<0 应属参数非法: %v", err)
	}
	if _, err := st.Plug(charger.PlugRequest{At: -1, PortID: "X", Need: 10, MaxP: 5, MinP: 1}); !errors.Is(err, charger.ErrInvalidArg) {
		t.Fatalf("参数非法优先: %v", err)
	}

	id, err := st.Plug(charger.PlugRequest{At: 0, PortID: "A", Need: 100, MaxP: 5, MinP: 3})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.Plug(charger.PlugRequest{At: 0, PortID: "A", Need: 1, MaxP: 1, MinP: 0}); !errors.Is(err, charger.ErrPortOccupied) {
		t.Fatalf("占用: %v", err)
	}
	if err := st.SetPriority(999, 0, charger.Prefer); !errors.Is(err, charger.ErrNoSession) {
		t.Fatal(err)
	}
	if _, err := st.AdvanceTo(10); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Unplug(id, 100); err != nil {
		t.Fatal(err)
	}
	if err := st.SetPriority(id, 100, charger.Prefer); !errors.Is(err, charger.ErrWrongState) {
		t.Fatalf("已结束会话改优先级: %v", err)
	}
	if _, err := st.Unplug(id, 101); !errors.Is(err, charger.ErrNoSession) {
		t.Fatalf("重复拔枪: %v", err)
	}
	if _, err := st.AdvanceTo(50); !errors.Is(err, charger.ErrClockBack) {
		t.Fatalf("时钟回退: %v", err)
	}
	if err := st.SetCap(-1, 5); !errors.Is(err, charger.ErrInvalidArg) {
		t.Fatal(err)
	}
	if err := st.SetCap(0, 0); !errors.Is(err, charger.ErrInvalidArg) {
		t.Fatal(err)
	}
	if _, err := charger.NewStation(0, []charger.Port{{ID: "A", Cap: 1}}); !errors.Is(err, charger.ErrInvalidArg) {
		t.Fatal(err)
	}
	if _, err := charger.NewStation(5, []charger.Port{{ID: "A", Cap: 0}}); !errors.Is(err, charger.ErrInvalidArg) {
		t.Fatal(err)
	}
}

func TestConcurrentCalls(t *testing.T) {
	const n = 6
	var ports []charger.Port
	for i := 0; i < n; i++ {
		ports = append(ports, charger.Port{ID: string(rune('A' + i)), Cap: 6})
	}
	st, _ := charger.NewStation(20, ports)

	var wg sync.WaitGroup
	var ready sync.WaitGroup
	var start sync.WaitGroup
	start.Add(1)
	ready.Add(n)
	ids := make([]int64, n)
	for g := 0; g < n; g++ {
		port := string(rune('A' + g))
		id, err := st.Plug(charger.PlugRequest{
			At: 0, PortID: port, Need: 1_000_000, MaxP: 6, MinP: 1,
		})
		if err != nil {
			t.Fatalf("plug: %v", err)
		}
		ids[g] = id
	}

	for g := 0; g < n; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			id := ids[g]
			ready.Done()
			start.Wait()
			for k := 0; k < 30; k++ {
				if _, err := st.AdvanceTo(int64(k + 1)); err != nil {
					// 并发推进等价于某种串行序；较早目标可能回退，属正常。
					if !errors.Is(err, charger.ErrClockBack) {
						t.Errorf("advance: %v", err)
					}
				}
				st.Status()
				if err := st.SetCap(int64(k+1), 20-(k%5)); err != nil {
					if !errors.Is(err, charger.ErrClockBack) {
						t.Errorf("setcap: %v", err)
					}
				}
			}
			if _, err := st.Unplug(id, 40); err != nil {
				t.Errorf("unplug: %v", err)
			}
		}(g)
	}
	start.Done()
	wg.Wait()

	// 串行化后不变量必须成立：总功率不超上限、接口不重复占用。
	status := st.Status()
	sum := 0
	seen := map[string]bool{}
	for _, s := range status.Sessions {
		if s.State == charger.StateCharging {
			sum += s.Power
			if seen[s.PortID] {
				t.Fatalf("接口 %s 重复", s.PortID)
			}
			seen[s.PortID] = true
			if s.Power < s.MinP || s.Power > s.MaxP {
				t.Fatalf("功率越界 %+v", s)
			}
		}
	}
	if sum > status.TotalCap {
		t.Fatalf("总功率 %d > %d", sum, status.TotalCap)
	}
}

func TestAdvanceCostIndependentOfSeconds(t *testing.T) {
	// 区间内无充满、无上限变更时，推进开销只取决于在站车辆数，
	// 不应随秒数线性增长。用大跨度推进验证结果正确且快速。
	mk := func(numCars int) *charger.Station {
		var ports []charger.Port
		for i := 0; i < numCars; i++ {
			ports = append(ports, charger.Port{ID: string(rune('A' + i)), Cap: 10})
		}
		st, _ := charger.NewStation(10*numCars, ports)
		for _, p := range ports {
			if _, err := st.Plug(charger.PlugRequest{
				At: 0, PortID: p.ID, Need: 10*1_000_000_000 + 1, MaxP: 10, MinP: 1,
			}); err != nil {
				t.Fatal(err)
			}
		}
		return st
	}

	st := mk(8)
	const horizon int64 = 1_000_000_000 // 十亿秒，逐秒实现会极慢
	ev, err := st.AdvanceTo(horizon)
	if err != nil {
		t.Fatal(err)
	}
	if len(ev) != 0 {
		t.Fatalf("不应有充满: %v", ev)
	}
	for _, s := range st.Status().Sessions {
		// 每台功率 10，历经 horizon 秒。
		if s.Charged != int(10*horizon) {
			t.Fatalf("id=%d charged=%d want %d", s.ID, s.Charged, 10*horizon)
		}
	}
}

func TestEventCostIndependentOfEndedSessions(t *testing.T) {
	// 已结束会话数量增长不应拖慢后续事件。
	var ports []charger.Port
	for i := 0; i < 4; i++ {
		ports = append(ports, charger.Port{ID: string(rune('A' + i)), Cap: 8})
	}
	st, _ := charger.NewStation(20, ports)

	const ended = 4000
	for k := 0; k < ended; k++ {
		at := int64(k * 2)
		p := ports[k%len(ports)]
		id, err := st.Plug(charger.PlugRequest{
			At: at, PortID: p.ID, Need: 5, MaxP: 8, MinP: 0,
		})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := st.Unplug(id, at); err != nil {
			t.Fatal(err)
		}
	}

	// 在大量已结束会话之后，当前在站的操作与推进仍只依赖在站车辆。
	id, err := st.Plug(charger.PlugRequest{
		At: int64(ended * 2), PortID: "A", Need: 30, MaxP: 8, MinP: 0,
	})
	if err != nil {
		t.Fatal(err)
	}
	ev, err := st.AdvanceTo(int64(ended*2) + 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(ev) != 1 || ev[0].SessionID != id || ev[0].At != int64(ended*2)+4 {
		t.Fatalf("充满事件: %+v", ev)
	}
}

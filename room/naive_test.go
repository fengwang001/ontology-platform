package room

// 朴素模拟：每次全量排序的参照实现，与堆实现逐操作对照。
// 语义与 room.System 完全一致，仅数据结构不同（切片 + 全量排序）。

import (
	"errors"
	"fmt"
	"math/rand"
	"reflect"
	"sort"
	"testing"

	"ontology/queue"
	"ontology/triage"
)

type nPatient struct {
	id     string
	reg    int
	level  int
	q      int
	la     int
	callAt int
	miss   int
	state  queue.State
}

type naive struct {
	r       [5]int
	a       int
	maxNow  int
	nextReg int
	pats    map[string]*nPatient
	kinds   map[string]Kind
	occ     map[string]string // room -> patient
}

func newNaive(r1, r2, r3, r4, a int) *naive {
	return &naive{
		r:       [5]int{0, r1, r2, r3, r4},
		a:       a,
		maxNow:  -1,
		nextReg: 1,
		pats:    make(map[string]*nPatient),
		kinds:   make(map[string]Kind),
		occ:     make(map[string]string),
	}
}

func (n *naive) checkNow(now int) error {
	if now < 0 || now > maxNow {
		return ErrInvalidParam
	}
	if now < n.maxNow {
		return ErrClock
	}
	return nil
}

// settle 全量扫描已叫号者，按 (callAt+A, reg) 升序落地全部过号。
func (n *naive) settle(now int) {
	var due []*nPatient
	for _, p := range n.pats {
		if p.state == queue.Called && now-p.callAt > n.a {
			due = append(due, p)
		}
	}
	sort.Slice(due, func(i, j int) bool {
		if due[i].callAt != due[j].callAt {
			return due[i].callAt < due[j].callAt
		}
		return due[i].reg < due[j].reg
	})
	for _, p := range due {
		p.miss++
		if p.miss >= 3 {
			p.state = queue.Gone
		} else {
			p.state = queue.Waiting
			p.q = p.callAt + n.a
		}
		for rid, pid := range n.occ {
			if pid == p.id {
				delete(n.occ, rid)
			}
		}
	}
	n.maxNow = now
}

func (n *naive) addRoom(id string, kind Kind) error {
	if id == "" || !kind.valid() {
		return ErrInvalidParam
	}
	if _, ok := n.kinds[id]; ok {
		return ErrExistence
	}
	n.kinds[id] = kind
	return nil
}

func (n *naive) register(now int, id string, v triage.Vitals) error {
	if id == "" || !v.Valid() {
		return ErrInvalidParam
	}
	if err := n.checkNow(now); err != nil {
		return err
	}
	if _, ok := n.pats[id]; ok {
		return ErrExistence
	}
	n.settle(now)
	n.pats[id] = &nPatient{
		id: id, reg: n.nextReg, level: triage.Level(v),
		q: now, la: now, state: queue.Waiting,
	}
	n.nextReg++
	return nil
}

func (n *naive) reassess(now int, id string, v triage.Vitals) error {
	if id == "" || !v.Valid() {
		return ErrInvalidParam
	}
	if err := n.checkNow(now); err != nil {
		return err
	}
	p, ok := n.pats[id]
	if !ok {
		return ErrExistence
	}
	n.settle(now)
	if p.state != queue.Waiting {
		return ErrState
	}
	lv := triage.Level(v)
	p.la = now
	if lv > p.level { // 降级重新排队；升级或不变 q 保留
		p.q = now
	}
	p.level = lv
	return nil
}

func (n *naive) call(now int, room string) (string, []string, error) {
	if err := n.checkNow(now); err != nil {
		return "", nil, err
	}
	kind, ok := n.kinds[room]
	if !ok {
		return "", nil, ErrExistence
	}
	n.settle(now)
	if n.occ[room] != "" {
		return "", nil, ErrState
	}
	var cand []*nPatient
	for _, p := range n.pats {
		if p.state != queue.Waiting {
			continue
		}
		for _, lv := range kind.levels() {
			if p.level == lv {
				cand = append(cand, p)
			}
		}
	}
	sort.Slice(cand, func(i, j int) bool {
		if cand[i].level != cand[j].level {
			return cand[i].level < cand[j].level
		}
		if cand[i].q != cand[j].q {
			return cand[i].q < cand[j].q
		}
		return cand[i].reg < cand[j].reg
	})
	var skipped []string
	for _, p := range cand {
		if now-p.la > n.r[p.level] { // 逾期者跳过，恰等不逾期
			skipped = append(skipped, p.id)
			continue
		}
		p.state = queue.Called
		p.callAt = now
		n.occ[room] = p.id
		return p.id, skipped, nil
	}
	return "", nil, ErrNoCallable
}

func (n *naive) arrive(now int, id string) error {
	if id == "" {
		return ErrInvalidParam
	}
	if err := n.checkNow(now); err != nil {
		return err
	}
	p, ok := n.pats[id]
	if !ok {
		return ErrExistence
	}
	n.settle(now)
	if p.state != queue.Called {
		return ErrState
	}
	p.state = queue.InService
	return nil
}

func (n *naive) finish(now int, room string) error {
	if err := n.checkNow(now); err != nil {
		return err
	}
	if _, ok := n.kinds[room]; !ok {
		return ErrExistence
	}
	n.settle(now)
	pid := n.occ[room]
	if pid == "" {
		return ErrState
	}
	p := n.pats[pid]
	if p.state != queue.InService {
		return ErrState
	}
	p.state = queue.Gone
	delete(n.occ, room)
	return nil
}

// patSnap 为患者状态快照，用于逐操作对拍。
type patSnap struct {
	Reg    int
	Level  int
	Q      int
	LA     int
	CallAt int
	Miss   int
	State  queue.State
}

func snapReal(s *System) (map[string]patSnap, map[string]string, int) {
	ps := make(map[string]patSnap)
	for _, p := range s.q.Snapshot() {
		ps[p.ID] = patSnap{p.Reg, p.Level, p.Q, p.LA, p.CallAt, p.Miss, p.State}
	}
	rm := make(map[string]string)
	for id, r := range s.rooms {
		rm[id] = r.Patient
	}
	return ps, rm, s.maxNow
}

func snapNaive(n *naive) (map[string]patSnap, map[string]string, int) {
	ps := make(map[string]patSnap)
	for id, p := range n.pats {
		ps[id] = patSnap{p.reg, p.level, p.q, p.la, p.callAt, p.miss, p.state}
	}
	rm := make(map[string]string)
	for id := range n.kinds {
		rm[id] = n.occ[id]
	}
	return ps, rm, n.maxNow
}

// op 为一条随机操作。
type op struct {
	kind    string
	now     int
	patient string
	room    string
	rkind   Kind
	v       triage.Vitals
}

func (o op) String() string {
	switch o.kind {
	case "register", "reassess":
		return fmt.Sprintf("%s(now=%d, %s, %+v)", o.kind, o.now, o.patient, o.v)
	case "call", "finish":
		return fmt.Sprintf("%s(now=%d, %s)", o.kind, o.now, o.room)
	case "arrive":
		return fmt.Sprintf("arrive(now=%d, %s)", o.now, o.patient)
	default:
		return fmt.Sprintf("addroom(%s, %d)", o.room, o.rkind)
	}
}

// opResult 为一次操作的统一结果。
type opResult struct {
	called  string
	skipped []string
	err     error
}

func errCode(err error) string {
	switch {
	case err == nil:
		return "ok"
	case errors.Is(err, ErrInvalidParam):
		return "param"
	case errors.Is(err, ErrClock):
		return "clock"
	case errors.Is(err, ErrExistence):
		return "exist"
	case errors.Is(err, ErrState):
		return "state"
	case errors.Is(err, ErrNoCallable):
		return "nocall"
	default:
		return "?"
	}
}

func (r opResult) String() string {
	return fmt.Sprintf("called=%q skipped=%v err=%s", r.called, r.skipped, errCode(r.err))
}

func sameResult(a, b opResult) bool {
	return a.called == b.called && equalStrs(a.skipped, b.skipped) && errCode(a.err) == errCode(b.err)
}

func runOnSystem(s *System, o op) opResult {
	switch o.kind {
	case "register":
		return opResult{err: s.Register(o.now, o.patient, o.v)}
	case "reassess":
		return opResult{err: s.Reassess(o.now, o.patient, o.v)}
	case "call":
		c, sk, err := s.Call(o.now, o.room)
		return opResult{c, sk, err}
	case "arrive":
		return opResult{err: s.Arrive(o.now, o.patient)}
	case "finish":
		return opResult{err: s.Finish(o.now, o.room)}
	default:
		return opResult{err: s.AddRoom(o.room, o.rkind)}
	}
}

func runOnNaive(n *naive, o op) opResult {
	switch o.kind {
	case "register":
		return opResult{err: n.register(o.now, o.patient, o.v)}
	case "reassess":
		return opResult{err: n.reassess(o.now, o.patient, o.v)}
	case "call":
		c, sk, err := n.call(o.now, o.room)
		return opResult{c, sk, err}
	case "arrive":
		return opResult{err: n.arrive(o.now, o.patient)}
	case "finish":
		return opResult{err: n.finish(o.now, o.room)}
	default:
		return opResult{err: n.addRoom(o.room, o.rkind)}
	}
}

func genOp(rng *rand.Rand, now int, roomIDs []string) op {
	patient := fmt.Sprintf("p%d", rng.Intn(8))
	room := roomIDs[rng.Intn(len(roomIDs))]
	if rng.Intn(20) == 0 {
		room = "ghost"
	}
	v := triage.Vitals{
		HR:            rng.Intn(310) - 5,
		SBP:           rng.Intn(310) - 5,
		SpO2:          rng.Intn(106) - 3,
		Consciousness: "AVPUAVPUX"[rng.Intn(9)],
	}
	switch d := rng.Intn(100); {
	case d < 25:
		return op{kind: "register", now: now, patient: patient, v: v}
	case d < 45:
		return op{kind: "reassess", now: now, patient: patient, v: v}
	case d < 65:
		return op{kind: "call", now: now, room: room}
	case d < 80:
		return op{kind: "arrive", now: now, patient: patient}
	case d < 90:
		return op{kind: "finish", now: now, room: room}
	default:
		return op{kind: "addroom", room: fmt.Sprintf("rx%d", rng.Intn(6)), rkind: Kind(rng.Intn(4))}
	}
}

// TestRandomVsNaive 用 1500 组随机操作序列将堆实现与朴素全量排序模拟逐操作对照，
// 并同时跑第二份堆实现验证重放结果相同。
func TestRandomVsNaive(t *testing.T) {
	const sequences = 1500
	for seq := 0; seq < sequences; seq++ {
		rng := rand.New(rand.NewSource(int64(seq)*7919 + 1))
		r1, r2, r3, r4 := 1+rng.Intn(12), 1+rng.Intn(12), 1+rng.Intn(12), 1+rng.Intn(12)
		a := 1 + rng.Intn(8)
		real1, err := New(r1, r2, r3, r4, a)
		if err != nil {
			t.Fatal(err)
		}
		real2, err := New(r1, r2, r3, r4, a)
		if err != nil {
			t.Fatal(err)
		}
		nv := newNaive(r1, r2, r3, r4, a)

		nRooms := 1 + rng.Intn(3)
		roomIDs := make([]string, 0, nRooms)
		for i := 0; i < nRooms; i++ {
			id := fmt.Sprintf("rm%d", i)
			kind := Clinic
			if rng.Intn(2) == 0 {
				kind = Resuscitation
			}
			if err := real1.AddRoom(id, kind); err != nil {
				t.Fatalf("seq=%d AddRoom: %v", seq, err)
			}
			if err := real2.AddRoom(id, kind); err != nil {
				t.Fatalf("seq=%d AddRoom: %v", seq, err)
			}
			if err := nv.addRoom(id, kind); err != nil {
				t.Fatalf("seq=%d AddRoom: %v", seq, err)
			}
			roomIDs = append(roomIDs, id)
		}

		now := 0
		ops := 30 + rng.Intn(20)
		for opIdx := 0; opIdx < ops; opIdx++ {
			switch dice := rng.Intn(100); {
			case dice < 8:
				now -= rng.Intn(6) // 时钟回退或越界
			case dice < 10:
				now = 1_000_000_000 + rng.Intn(3) // 越界
			default:
				now += rng.Intn(6)
			}
			o := genOp(rng, now, roomIDs)
			got1 := runOnSystem(real1, o)
			got2 := runOnSystem(real2, o)
			want := runOnNaive(nv, o)
			t.Logf("seq=%d op=%d 输入=%s 输出(real)=%s 输出(naive)=%s 判定依据=堆实现与全量排序模拟一致",
				seq, opIdx, o, got1, want)
			if !sameResult(got1, got2) {
				t.Fatalf("seq=%d op=%d 重放不一致: %s\n第一次=%s\n第二次=%s", seq, opIdx, o, got1, got2)
			}
			if !sameResult(got1, want) {
				t.Fatalf("seq=%d op=%d 与朴素模拟不一致: %s\nreal=%s\nnaive=%s", seq, opIdx, o, got1, want)
			}
			psR, rmR, mnR := snapReal(real1)
			psN, rmN, mnN := snapNaive(nv)
			if !reflect.DeepEqual(psR, psN) || !reflect.DeepEqual(rmR, rmN) || mnR != mnN {
				t.Fatalf("seq=%d op=%d 状态分歧: %s\nreal: pats=%v rooms=%v maxNow=%d\nnaive: pats=%v rooms=%v maxNow=%d",
					seq, opIdx, o, psR, rmR, mnR, psN, rmN, mnN)
			}
		}
	}
}

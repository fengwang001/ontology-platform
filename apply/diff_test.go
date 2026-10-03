package apply_test

import (
	"fmt"
	"math/rand"
	"os"
	"sort"
	"strings"
	"testing"

	"ontology/apply"
)

// ---- 朴素模拟器：严格按题面规则逐步写成，刻意不使用被实现代码的任何结构 ----

type simKey struct {
	s, kind int
	id      int64
}

type simEvent struct {
	op            string // U / D / T
	s, kind       int
	id, a, b, now int64
}

type simRow struct {
	tid   int64
	alive bool
	a, b  int64 // 已改写的目标引用
}

type simPending struct {
	key    simKey
	a, b   int64
	aseq   int
	arrive int64
	wait   simKey
}

type simResult struct {
	status apply.Status
	tid    int64
	dead   int
}

type sim struct {
	t, q    int64
	allow   map[int]bool
	maxNow  int64
	nextSeq int
	nextTid map[int]int64
	rows    map[simKey]*simRow
	pending map[simKey]*simPending
	dead    []simKey
	log     []string
}

func newSim(t int64, q int, allow []int) *sim {
	m := map[int]bool{}
	for _, s := range allow {
		m[s] = true
	}
	return &sim{
		t: t, q: int64(q), allow: m,
		nextTid: map[int]int64{apply.KindDept: 1, apply.KindEmp: 1},
		rows:    map[simKey]*simRow{},
		pending: map[simKey]*simPending{},
	}
}

func validSimEvent(ev simEvent) bool {
	if ev.op == "T" {
		return ev.now >= 0 && ev.now <= 1_000_000_000_000
	}
	if ev.s < 1 || ev.s > 16 || ev.now < 0 || ev.now > 1_000_000_000_000 {
		return false
	}
	if ev.kind != apply.KindDept && ev.kind != apply.KindEmp || ev.id < 1 || ev.id > 1e9 {
		return false
	}
	if ev.op != "U" {
		return true // Delete 无引用字段
	}
	if ev.a < 0 || ev.a > 1e9 || ev.b < 0 || ev.b > 1e9 {
		return false
	}
	if ev.kind == apply.KindDept && ev.b != 0 {
		return false
	}
	if ev.kind == apply.KindEmp && ev.a == 0 {
		return false
	}
	return true
}

func refKey(k simKey, isB bool, ref int64) simKey {
	if isB {
		return simKey{k.s, apply.KindEmp, ref}
	}
	return simKey{k.s, apply.KindDept, ref}
}

func (s *sim) ready(k simKey, a, b int64) (bool, simKey) {
	check := func(rk simKey, ref int64) simKey {
		if ref == 0 || rk == k {
			return simKey{}
		}
		r, ok := s.rows[rk]
		if ok && r.alive {
			return simKey{}
		}
		return rk
	}
	if wk := check(refKey(k, false, a), a); wk != (simKey{}) {
		return false, wk
	}
	if k.kind == apply.KindEmp {
		if wk := check(refKey(k, true, b), b); wk != (simKey{}) {
			return false, wk
		}
	}
	return true, simKey{}
}

func (s *sim) land(k simKey, a, b int64) int64 {
	r := s.rows[k]
	if r == nil {
		tid := s.nextTid[k.kind]
		s.nextTid[k.kind]++
		r = &simRow{tid: tid}
		s.rows[k] = r
	}
	r.alive = true
	mapRef := func(isB bool, ref int64) int64 {
		if ref == 0 {
			return 0
		}
		rk := refKey(k, isB, ref)
		if rk == k {
			return r.tid
		}
		return s.rows[rk].tid
	}
	r.a = mapRef(false, a)
	r.b = mapRef(true, b)
	return r.tid
}

// expire 把到期挂起行按 aseq 升序移入死信，返回数量。
func (s *sim) expire(now int64) int {
	var due []*simPending
	for _, p := range s.pending {
		if now-p.arrive >= s.t {
			due = append(due, p)
		}
	}
	sort.Slice(due, func(i, j int) bool { return due[i].aseq < due[j].aseq })
	for _, p := range due {
		delete(s.pending, p.key)
		s.dead = append(s.dead, p.key)
	}
	return len(due)
}

func (s *sim) expiringCount(now int64) int {
	n := 0
	for _, p := range s.pending {
		if now-p.arrive >= s.t {
			n++
		}
	}
	return n
}

// release 以 root 为触发键执行 BFS 释放，返回 (落库数, 重挂数)。
func (s *sim) release(root simKey) (int, int) {
	work := []simKey{root}
	landed, rehung := 0, 0
	for len(work) > 0 {
		k := work[0]
		work = work[1:]
		// 以 k 为等待键的挂起行，按 aseq 升序。
		var waiters []*simPending
		for _, p := range s.pending {
			if p.wait == k {
				waiters = append(waiters, p)
			}
		}
		sort.Slice(waiters, func(i, j int) bool { return waiters[i].aseq < waiters[j].aseq })
		for _, p := range waiters {
			if ok, _ := s.ready(p.key, p.a, p.b); ok {
				delete(s.pending, p.key)
				s.land(p.key, p.a, p.b)
				work = append(work, p.key)
				landed++
			} else {
				_, wk := s.ready(p.key, p.a, p.b)
				p.wait = wk
				rehung++
			}
		}
	}
	return landed, rehung
}

func (s *sim) handle(ev simEvent) simResult {
	if !validSimEvent(ev) {
		return simResult{status: apply.StatusErrParam}
	}
	if ev.op == "T" {
		if ev.now < s.maxNow {
			return simResult{status: apply.StatusErrClock}
		}
		n := s.expire(ev.now)
		s.maxNow = ev.now
		return simResult{status: apply.StatusApplied, dead: n}
	}
	if !s.allow[ev.s] {
		return simResult{status: apply.StatusErrDenied}
	}
	if ev.now < s.maxNow {
		return simResult{status: apply.StatusErrClock}
	}
	k := simKey{ev.s, ev.kind, ev.id}
	if ev.op == "D" {
		n := s.expire(ev.now)
		s.maxNow = ev.now
		if _, ok := s.pending[k]; ok {
			delete(s.pending, k)
			return simResult{status: apply.StatusDroppedPending, dead: n}
		}
		r := s.rows[k]
		if r == nil || !r.alive {
			if r != nil {
				return simResult{status: apply.StatusErrUnknown, tid: r.tid, dead: n}
			}
			return simResult{status: apply.StatusErrUnknown, dead: n}
		}
		r.alive = false
		return simResult{status: apply.StatusDeleted, tid: r.tid, dead: n}
	}
	// Upsert
	ok0, wk := s.ready(k, ev.a, ev.b)
	_, existing := s.pending[k]
	if !ok0 && !existing {
		if int64(len(s.pending))-int64(s.expiringCount(ev.now))+1 > s.q {
			return simResult{status: apply.StatusErrFull}
		}
	}
	n := s.expire(ev.now)
	s.maxNow = ev.now
	ok0, wk = s.ready(k, ev.a, ev.b)
	_, existing = s.pending[k]
	if !ok0 {
		if existing {
			s.pending[k].a = ev.a
			s.pending[k].b = ev.b
			s.pending[k].wait = wk
		} else {
			s.nextSeq++
			s.pending[k] = &simPending{
				key: k, a: ev.a, b: ev.b, aseq: s.nextSeq, arrive: ev.now, wait: wk,
			}
		}
		return simResult{status: apply.StatusPending, dead: n}
	}
	delete(s.pending, k)
	tid := s.land(k, ev.a, ev.b)
	s.release(k)
	return simResult{status: apply.StatusApplied, tid: tid, dead: n}
}

// ---- 随机事件序列生成与对拍 ----

func statusName(st apply.Status) string {
	switch st {
	case apply.StatusApplied:
		return "Applied"
	case apply.StatusPending:
		return "Pending"
	case apply.StatusDeleted:
		return "Deleted"
	case apply.StatusDroppedPending:
		return "DroppedPending"
	case apply.StatusErrUnknown:
		return "ErrUnknown"
	case apply.StatusErrDenied:
		return "ErrDenied"
	case apply.StatusErrFull:
		return "ErrFull"
	case apply.StatusErrParam:
		return "ErrParam"
	case apply.StatusErrClock:
		return "ErrClock"
	}
	return "?"
}

func evString(ev simEvent) string {
	if ev.op == "T" {
		return fmt.Sprintf("Tick(now=%d)", ev.now)
	}
	return fmt.Sprintf("%s(s=%d kind=%d id=%d a=%d b=%d now=%d)",
		map[string]string{"U": "Upsert", "D": "Delete"}[ev.op],
		ev.s, ev.kind, ev.id, ev.a, ev.b, ev.now)
}

// snapshot 比较两边全量状态；返回差异描述，空串表示一致。
func compareStates(t *testing.T, e *apply.Engine, s *sim) string {
	var diffs []string

	// 存活行：tid 与改写后的引用。
	engRows := map[simKey]apply.Row{}
	for _, r := range e.Snapshot() {
		engRows[simKey{r.Key.S, r.Key.Kind, r.Key.Id}] = r
	}
	simAlive := map[simKey]*simRow{}
	for k, r := range s.rows {
		if r.alive {
			simAlive[k] = r
		}
	}
	if len(engRows) != len(simAlive) {
		diffs = append(diffs, fmt.Sprintf("alive count: eng=%d sim=%d", len(engRows), len(simAlive)))
	}
	for k, sr := range simAlive {
		er, ok := engRows[k]
		if !ok {
			diffs = append(diffs, fmt.Sprintf("sim alive row %+v missing in engine", k))
			continue
		}
		if er.Tid != sr.tid || er.A != sr.a || er.B != sr.b {
			diffs = append(diffs, fmt.Sprintf("row %+v eng(tid=%d a=%d b=%d) sim(tid=%d a=%d b=%d)",
				k, er.Tid, er.A, er.B, sr.tid, sr.a, sr.b))
		}
	}
	for k := range engRows {
		if _, ok := simAlive[k]; !ok {
			diffs = append(diffs, fmt.Sprintf("engine alive row %+v missing in sim", k))
		}
	}

	// 每表 next（tid 连续、落库序）。
	if n := e.Next(apply.KindDept); n != s.nextTid[apply.KindDept] {
		diffs = append(diffs, fmt.Sprintf("dept next eng=%d sim=%d", n, s.nextTid[apply.KindDept]))
	}
	if n := e.Next(apply.KindEmp); n != s.nextTid[apply.KindEmp] {
		diffs = append(diffs, fmt.Sprintf("emp next eng=%d sim=%d", n, s.nextTid[apply.KindEmp]))
	}

	// 挂起行：aseq、arrive、wait、载荷。
	engPend := map[simKey]struct {
		a, b, arrive int64
		aseq         int
		wait         simKey
	}{}
	for _, p := range e.PendingAll() {
		engPend[simKey{p.Entry.Key.S, p.Entry.Key.Kind, p.Entry.Key.Id}] = struct {
			a, b, arrive int64
			aseq         int
			wait         simKey
		}{
			p.Entry.A, p.Entry.B, p.Arrive, p.Aseq,
			simKey{p.Wait.S, p.Wait.Kind, p.Wait.Id},
		}
	}
	if len(engPend) != len(s.pending) {
		diffs = append(diffs, fmt.Sprintf("pending count: eng=%d sim=%d", len(engPend), len(s.pending)))
	}
	for k, sp := range s.pending {
		ep, ok := engPend[k]
		if !ok {
			diffs = append(diffs, fmt.Sprintf("sim pending %+v missing", k))
			continue
		}
		if ep.a != sp.a || ep.b != sp.b || ep.aseq != sp.aseq || ep.arrive != sp.arrive || ep.wait != sp.wait {
			diffs = append(diffs, fmt.Sprintf(
				"pending %+v eng(a=%d b=%d aseq=%d arrive=%d wait=%+v) sim(a=%d b=%d aseq=%d arrive=%d wait=%+v)",
				k, ep.a, ep.b, ep.aseq, ep.arrive, ep.wait, sp.a, sp.b, sp.aseq, sp.arrive, sp.wait))
		}
	}
	for k := range engPend {
		if _, ok := s.pending[k]; !ok {
			diffs = append(diffs, fmt.Sprintf("engine pending %+v missing", k))
		}
	}

	// 死信次序。
	ed := e.Dead()
	if len(ed) != len(s.dead) {
		diffs = append(diffs, fmt.Sprintf("dead count: eng=%d sim=%d", len(ed), len(s.dead)))
	}
	for i := 0; i < len(ed) && i < len(s.dead); i++ {
		gk := simKey{ed[i].S, ed[i].Kind, ed[i].Id}
		if gk != s.dead[i] {
			diffs = append(diffs, fmt.Sprintf("dead[%d] eng=%+v sim=%+v", i, gk, s.dead[i]))
		}
	}
	return strings.Join(diffs, "\n")
}

func genCase(rng *rand.Rand) (int64, int, []int, []simEvent) {
	t := int64(rng.Intn(40) + 1)
	q := rng.Intn(4) + 1
	nAllow := rng.Intn(3) + 1
	allowSet := map[int]bool{}
	for len(allowSet) < nAllow {
		allowSet[rng.Intn(3)+1] = true
	}
	var allow []int
	for s := range allowSet {
		allow = append(allow, s)
	}
	nEvents := rng.Intn(60) + 20
	evs := make([]simEvent, 0, nEvents)
	now := int64(0)
	// 源 id 取自小池子，制造替换、删除、再 Upsert 与引用聚集。
	const idPool = 12
	for i := 0; i < nEvents; i++ {
		switch rng.Intn(10) {
		case 0:
			now += int64(rng.Intn(8))
			evs = append(evs, simEvent{op: "T", now: now})
		case 1, 2, 3:
			s := allow[rng.Intn(len(allow))]
			kind := rng.Intn(2) + 1
			id := int64(rng.Intn(idPool) + 1)
			evs = append(evs, simEvent{op: "D", s: s, kind: kind, id: id, now: now})
		default:
			s := allow[rng.Intn(len(allow))]
			kind := rng.Intn(2) + 1
			id := int64(rng.Intn(idPool) + 1)
			var a, b int64
			switch {
			case kind == apply.KindDept && rng.Intn(4) == 0:
				a = 0
			case rng.Intn(6) == 0:
				a = id // 自引用
			default:
				a = int64(rng.Intn(idPool) + 1)
			}
			if kind == apply.KindEmp && rng.Intn(2) == 0 {
				if rng.Intn(6) == 0 {
					b = id
				} else {
					b = int64(rng.Intn(idPool) + 1)
				}
			}
			evs = append(evs, simEvent{op: "U", s: s, kind: kind, id: id, a: a, b: b, now: now})
		}
		// 少量注入拒绝类事件：越权分片 / 时钟回退 / 非法参数。
		switch rng.Intn(15) {
		case 0:
			evs = append(evs, simEvent{op: "U", s: 16, kind: apply.KindDept, id: 1, now: now})
		case 1:
			if now > 0 {
				evs = append(evs, simEvent{op: "U", s: allow[0], kind: apply.KindDept, id: 1, now: now - 1})
			}
		case 2:
			evs = append(evs, simEvent{op: "U", s: allow[0], kind: apply.KindDept, id: 1, b: 1, now: now})
		case 3:
			evs = append(evs, simEvent{op: "U", s: allow[0], kind: apply.KindEmp, id: 1, a: 0, now: now})
		}
	}
	return t, q, allow, evs
}

func TestDifferential(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping differential fuzz in short mode")
	}
	fullLog := os.Getenv("DIFF_FULL_LOG") == "1"
	const cases = 2000
	var rng *rand.Rand
	// 固定种子，保证可精确复现。
	rng = rand.New(rand.NewSource(20261003))
	for c := 0; c < cases; c++ {
		T, Q, allow, evs := genCase(rng)
		eng, err := apply.New(T, Q, allow)
		if err != nil {
			t.Fatalf("case %d New: %v", c, err)
		}
		sm := newSim(T, Q, allow)
		var trace []string
		trace = append(trace, fmt.Sprintf("== case %d T=%d Q=%d allow=%v ==", c, T, Q, allow))
		failed := false
		for i, ev := range evs {
			var er apply.Result
			switch ev.op {
			case "U":
				er = eng.Upsert(ev.s, ev.kind, ev.id, ev.a, ev.b, ev.now)
			case "D":
				er = eng.Delete(ev.s, ev.kind, ev.id, ev.now)
			case "T":
				er = eng.Tick(ev.now)
			}
			sr := sm.handle(ev)
			basis := "matches"
			if er.Status != sr.status || er.Tid != sr.tid || er.Dead != sr.dead {
				basis = fmt.Sprintf("MISMATCH eng(%s,tid=%d,dead=%d) sim(%s,tid=%d,dead=%d)",
					statusName(er.Status), er.Tid, er.Dead, statusName(sr.status), sr.tid, sr.dead)
				failed = true
			}
			trace = append(trace, fmt.Sprintf("  [%d] %s -> eng(%s,tid=%d,dead=%d) sim(%s,tid=%d,dead=%d) %s",
				i, evString(ev),
				statusName(er.Status), er.Tid, er.Dead,
				statusName(sr.status), sr.tid, sr.dead, basis))
		}
		if diff := compareStates(t, eng, sm); diff != "" {
			failed = true
			trace = append(trace, "STATE DIFF:\n"+diff)
		}
		if failed {
			t.Fatalf("case %d mismatch\n%s", c, strings.Join(trace, "\n"))
		} else if fullLog || c < 3 {
			t.Log("\n" + strings.Join(trace, "\n"))
		}
	}
	t.Logf("differential: %d random cases all matched", cases)
}

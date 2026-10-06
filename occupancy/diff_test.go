package occupancy

import (
	"fmt"
	"math/rand"
	"strings"
	"testing"
)

// opKind 是随机序列里的操作种类。
type opKind int

const (
	opApply opKind = iota
	opExtend
	opRevoke
	opQuery
)

// difWorld 同时驱动 Service 与 NaiveModel，并在每次操作后比对全部可见状态。
type difWorld struct {
	t     *testing.T
	rng   *rand.Rand
	svc   *Service
	naive *NaiveModel
	log   *strings.Builder
	roads []string
	clock int64
	ids   []int64
}

func newDifWorld(t *testing.T, seed int64, log *strings.Builder) *difWorld {
	t.Helper()
	cfg := Config{
		Roads: []Road{
			{ID: "a", Lanes: 3, Corridor: "K", Detour: []string{"b", "c"}},
			{ID: "b", Lanes: 2, Corridor: "K", Detour: []string{"c"}},
			{ID: "c", Lanes: 4, Corridor: "K"},
			{ID: "d", Lanes: 2, Corridor: "L", Detour: []string{"a"}},
		},
		CorridorCap: map[string]int{"K": 3, "L": 2},
	}
	svc, err := NewService(cfg)
	if !err.None() {
		t.Fatal(err)
	}
	svc.SetLogger(func(line string) { fmt.Fprintln(log, "  svc:  ", line) })
	nv, err := NewNaiveModel(cfg)
	if !err.None() {
		t.Fatal(err)
	}
	return &difWorld{
		t: t, rng: rand.New(rand.NewSource(seed)),
		svc: svc, naive: nv, log: log,
		roads: []string{"a", "b", "c", "d"},
	}
}

func (w *difWorld) roadLanes(road string) int { return w.svc.lanes[road] }

func (w *difWorld) step() {
	// 时间只会前进，偶尔回退以触发时钟回退路径。
	if w.rng.Intn(7) != 0 {
		w.clock += int64(w.rng.Intn(4))
	}
	op := w.rng.Intn(10)
	switch {
	case op < 5:
		w.doApply()
	case op == 5:
		w.doApply(true)
	case op < 8:
		w.doExtend()
	case op < 9:
		w.doRevoke()
	default:
		w.doQuery()
	}
}

func (w *difWorld) doApply(forceEmergency ...bool) {
	road := w.roads[w.rng.Intn(len(w.roads))]
	lanes := 1 + w.rng.Intn(w.roadLanes(road)+1) // 含超限情形
	start := w.clock + int64(w.rng.Intn(6))
	end := start + 1 + int64(w.rng.Intn(12))
	prio := Regular
	if len(forceEmergency) > 0 || w.rng.Intn(4) == 0 {
		prio = Emergency
	}
	opTime := w.clock
	if w.rng.Intn(8) == 0 {
		opTime = w.clock - int64(1+w.rng.Intn(3)) // 故意时钟回退
	}
	req := ApplyRequest{OpTime: opTime, Road: road, Lanes: lanes, Start: start, End: end, Priority: prio}
	fmt.Fprintf(w.log, "APPLY op=%d road=%s lanes=%d [%d,%d) prio=%s\n",
		opTime, road, lanes, start, end, prio)
	r1 := w.svc.Apply(req)
	r2 := w.naive.Apply(req)
	w.cmpApply(req, r1, r2)
	if r1.Reason.None() {
		w.ids = append(w.ids, r1.PermitID)
		w.clock = opTime
	}
}

func (w *difWorld) doExtend() {
	if len(w.ids) == 0 {
		w.doApply()
		return
	}
	id := w.ids[w.rng.Intn(len(w.ids))]
	p1, _ := w.svc.Permit(id)
	newEnd := p1.Interval.Start + 1 + int64(w.rng.Intn(30))
	req := ExtendRequest{OpTime: w.clock, PermitID: id, NewEnd: newEnd}
	fmt.Fprintf(w.log, "EXTEND op=%d id=%d newEnd=%d\n", w.clock, id, newEnd)
	r1 := w.svc.Extend(req)
	r2 := w.naive.Extend(req)
	w.cmpMutation("extend", id, r1.Reason, r2.Reason, r1.Permit, r2.Permit)
	if r1.Reason.None() {
		w.clock = req.OpTime
	}
}

func (w *difWorld) doRevoke() {
	if len(w.ids) == 0 {
		w.doApply()
		return
	}
	id := w.ids[w.rng.Intn(len(w.ids))]
	req := RevokeRequest{OpTime: w.clock, PermitID: id}
	fmt.Fprintf(w.log, "REVOKE op=%d id=%d\n", w.clock, id)
	r1 := w.svc.Revoke(req)
	r2 := w.naive.Revoke(req)
	w.cmpMutation("revoke", id, r1.Reason, r2.Reason, r1.Permit, r2.Permit)
	if r1.Reason.None() {
		w.clock = req.OpTime
	}
}

func (w *difWorld) doQuery() {
	road := w.roads[w.rng.Intn(len(w.roads))]
	at := int64(w.rng.Intn(60))
	fmt.Fprintf(w.log, "QUERY road=%s at=%d\n", road, at)
	q1, e1 := w.svc.Query(QueryRequest{Road: road, At: at})
	q2, e2 := w.naive.Query(QueryRequest{Road: road, At: at})
	if e1.Code != e2.Code {
		w.die("query error code %v != %v", e1, e2)
	}
	if q1.ClosedLanes != q2.ClosedLanes {
		w.die("query road=%s at=%d lanes %d != %d\nq1=%+v\nq2=%+v",
			road, at, q1.ClosedLanes, q2.ClosedLanes, q1.Active, q2.Active)
	}
	if len(q1.Active) != len(q2.Active) {
		w.die("query road=%s at=%d active %d != %d", road, at, len(q1.Active), len(q2.Active))
	}
}

func (w *difWorld) cmpApply(req ApplyRequest, a, b ApplyResult) {
	if a.Reason.Code != b.Reason.Code {
		w.die("APPLY %v code svc=%v naive=%v (svc detail=%q)",
			req, a.Reason.Code, b.Reason.Code, a.Reason.Detail)
	}
	if a.Reason.None() {
		if a.PermitID != b.PermitID {
			w.die("apply id %d != %d", a.PermitID, b.PermitID)
		}
		w.cmpPermit(a.Permit, b.Permit)
		w.cmpPreempt(a.Preempted, b.Preempted)
	}
}

func (w *difWorld) cmpPreempt(a, b []PreemptRecord) {
	if len(a) != len(b) {
		w.die("preempt len %d != %d\na=%+v\nb=%+v", len(a), len(b), a, b)
	}
	for i := range a {
		x, y := a[i], b[i]
		if x.PermitID != y.PermitID || x.Truncated != y.Truncated ||
			x.NewStart != y.NewStart || x.NewEnd != y.NewEnd ||
			x.Approved != y.Approved || x.Reason.Code != y.Reason.Code {
			w.die("preempt[%d] svc=%+v naive=%+v", i, x, y)
		}
	}
}

func (w *difWorld) cmpMutation(kind string, id int64, e1, e2 CodeError, p1, p2 Permit) {
	if e1.Code != e2.Code {
		w.die("%s id=%d code svc=%v naive=%v (svc=%q)", kind, id, e1.Code, e2.Code, e1.Detail)
	}
	if e1.None() {
		w.cmpPermit(p1, p2)
	}
}

func (w *difWorld) cmpPermit(a, b Permit) {
	if a.ID != b.ID || a.Road != b.Road || a.Lanes != b.Lanes ||
		a.Interval != b.Interval || a.Status != b.Status || a.Priority != b.Priority {
		w.die("permit svc=%+v naive=%+v", a, b)
	}
}

func (w *difWorld) die(format string, args ...interface{}) {
	w.t.Fatalf("MISMATCH: "+format+"\n--- operation log ---\n%s",
		append(args, w.log.String())...)
}

func TestRandomDifferential(t *testing.T) {
	if testing.Short() {
		t.Skip()
	}
	for seed := int64(0); seed < 40; seed++ {
		var log strings.Builder
		w := newDifWorld(t, seed, &log)
		for i := 0; i < 1500; i++ {
			w.step()
		}
	}
}

// TestRandomDifferentialLogged 用固定种子跑一条序列并打印全部操作与判定依据，
// 便于人工复核；go test -v 可直接看到日志。
func TestRandomDifferentialLogged(t *testing.T) {
	var log strings.Builder
	w := newDifWorld(t, 20261006, &log)
	for i := 0; i < 120; i++ {
		w.step()
	}
	t.Logf("differential trace (seed=20261006):\n%s", log.String())
}

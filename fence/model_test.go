package fence

// This file holds an independently written naive reference model. It uses
// no spatial index (every point query scans every fence), no incremental
// counters (every count scans every vehicle) and its own float64
// ray-casting geometry. The randomized differential test at the bottom
// drives the real Service and this model through identical operation
// sequences and requires identical outcomes, fees, tasks and state.

import (
	"fmt"
	"math/rand"
	"reflect"
	"sort"
	"testing"
	"time"
)

// --- naive geometry -------------------------------------------------------

func mCross(a, b, c Point) int64 {
	return (b.X-a.X)*(c.Y-a.Y) - (b.Y-a.Y)*(c.X-a.X)
}

func mOnBoundary(poly []Point, p Point) bool {
	n := len(poly)
	for i := 0; i < n; i++ {
		a, b := poly[i], poly[(i+1)%n]
		if mCross(a, b, p) == 0 &&
			p.X >= min(a.X, b.X) && p.X <= max(a.X, b.X) &&
			p.Y >= min(a.Y, b.Y) && p.Y <= max(a.Y, b.Y) {
			return true
		}
	}
	return false
}

// mInPoly: boundary-inclusive point-in-polygon via float64 ray casting.
func mInPoly(poly []Point, p Point) bool {
	if mOnBoundary(poly, p) {
		return true
	}
	inside := false
	n := len(poly)
	for i := 0; i < n; i++ {
		a, b := poly[i], poly[(i+1)%n]
		if (a.Y > p.Y) != (b.Y > p.Y) {
			xint := float64(a.X) + float64(p.Y-a.Y)*float64(b.X-a.X)/float64(b.Y-a.Y)
			if float64(p.X) < xint {
				inside = !inside
			}
		}
	}
	return inside
}

// --- naive model ----------------------------------------------------------

type mFence struct {
	id     string
	typ    FenceType
	poly   []Point
	cap    int
	parent string
}

type mVehicle struct {
	state VehicleState
	fence string
}

type mTask struct {
	id      string
	kind    TaskKind
	status  TaskStatus
	src     string
	dst     string
	veh     string
	move    int
	created int64
	claims  []ClaimRecord
}

type naive struct {
	cfg      Config
	loc      *time.Location
	fences   []mFence
	vehicles map[string]*mVehicle
	tasks    []*mTask
	rewards  map[string]bool
	ledger   []LedgerEntry
	last     int64
	has      bool
}

func newNaive(cfg Config) *naive {
	loc, err := time.LoadLocation(cfg.Timezone)
	if err != nil {
		panic(err)
	}
	return &naive{
		cfg:      cfg,
		loc:      loc,
		vehicles: make(map[string]*mVehicle),
		rewards:  make(map[string]bool),
	}
}

func (m *naive) clock(ts int64) ErrCode {
	if m.has && ts < m.last {
		return ErrClockRollback
	}
	return 0
}

func (m *naive) fenceByID(id string) *mFence {
	for i := range m.fences {
		if m.fences[i].id == id {
			return &m.fences[i]
		}
	}
	return nil
}

// register appends a fence, resolving the parent operating fence for inner
// fences by full vertex containment. The differential test only registers
// valid fences, so no validation happens here; accepted registration still
// advances the clock like the real service.
func (m *naive) register(f Fence, ts int64) {
	nf := mFence{id: f.ID, typ: f.Type, poly: f.Vertices, cap: f.Capacity}
	if f.Type != Operating {
		for _, g := range m.fences {
			if g.typ != Operating {
				continue
			}
			inside := true
			for _, v := range f.Vertices {
				if !mInPoly(g.poly, v) {
					inside = false
					break
				}
			}
			if inside {
				nf.parent = g.id
			}
		}
	}
	m.fences = append(m.fences, nf)
	m.last, m.has = ts, true
}

// locate scans every fence for every point, collecting all containers and
// resolving by priority.
func (m *naive) locate(p Point) (Attribution, string) {
	var reward, nopark, oper string
	for _, f := range m.fences {
		if !mInPoly(f.poly, p) {
			continue
		}
		switch f.typ {
		case Reward:
			reward = f.id
		case NoParking:
			nopark = f.id
		case Operating:
			oper = f.id
		}
	}
	switch {
	case reward != "":
		return Attribution{Reward, reward}, fmt.Sprintf("point %v inside reward fence %s", p, reward)
	case nopark != "":
		return Attribution{NoParking, nopark}, fmt.Sprintf("point %v inside no-parking fence %s", p, nopark)
	case oper != "":
		return Attribution{Operating, oper}, fmt.Sprintf("point %v inside operating fence %s", p, oper)
	default:
		return Attribution{Outside, ""}, fmt.Sprintf("point %v outside all %d fences", p, len(m.fences))
	}
}

// count recomputes a fence's vehicle count by scanning every vehicle.
func (m *naive) count(fenceID string) int {
	n := 0
	for _, v := range m.vehicles {
		if v.state == VehicleParked && v.fence == fenceID {
			n++
		}
	}
	return n
}

func (m *naive) dayOf(ts int64) string {
	y, mo, d := time.Unix(ts, 0).In(m.loc).Date()
	return fmt.Sprintf("%04d-%02d-%02d", y, int(mo), d)
}

func (m *naive) addLedger(ts int64, user, veh, fence string, kind LedgerKind, amount int64) {
	m.ledger = append(m.ledger, LedgerEntry{
		Seq: len(m.ledger) + 1, Time: ts, UserID: user,
		VehicleID: veh, FenceID: fence, Kind: kind, Amount: amount,
	})
}

func (m *naive) addVehicle(id string, ts int64) ErrCode {
	if id == "" {
		return ErrInvalidArgument
	}
	if _, dup := m.vehicles[id]; dup {
		return ErrInvalidArgument
	}
	if c := m.clock(ts); c != 0 {
		return c
	}
	m.vehicles[id] = &mVehicle{state: VehicleIdle}
	m.last, m.has = ts, true
	return 0
}

func (m *naive) unlock(id string, ts int64) ErrCode {
	if id == "" {
		return ErrInvalidArgument
	}
	if c := m.clock(ts); c != 0 {
		return c
	}
	v, ok := m.vehicles[id]
	if !ok {
		return ErrVehicleNotFound
	}
	switch v.state {
	case VehicleParked, VehicleIdle:
		v.state = VehicleRiding
		v.fence = ""
	default:
		return ErrInvalidArgument
	}
	m.last, m.has = ts, true
	return 0
}

func (m *naive) effStatus(t *mTask, ts int64) TaskStatus {
	if t.status == TaskInProgress && len(t.claims) > 0 &&
		ts-t.claims[len(t.claims)-1].ClaimedAt >= m.cfg.ClaimTimeoutSec {
		return TaskPending
	}
	return t.status
}

func (m *naive) ret(vehID, userID string, p Point, ts int64) (ReturnResult, ErrCode, string) {
	var res ReturnResult
	if vehID == "" || userID == "" {
		return res, ErrInvalidArgument, "empty id"
	}
	if c := m.clock(ts); c != 0 {
		return res, c, "clock rollback"
	}
	v, ok := m.vehicles[vehID]
	if !ok {
		return res, ErrVehicleNotFound, "no such vehicle"
	}
	if v.state != VehicleRiding {
		return res, ErrVehicleNotRiding, fmt.Sprintf("vehicle state %s", v.state)
	}
	attr, why := m.locate(p)
	switch attr.Type {
	case NoParking:
		return res, ErrNoParkingReturn, why
	case Reward, Operating:
		f := m.fenceByID(attr.FenceID)
		if m.count(f.id) >= f.cap {
			return res, ErrFenceFull, fmt.Sprintf("%s; fence %s full %d/%d", why, f.id, m.count(f.id), f.cap)
		}
		v.state, v.fence = VehicleParked, f.id
		res.FenceID = f.id
		if f.typ == Reward {
			key := fmt.Sprintf("%s|%s|%s", userID, f.id, m.dayOf(ts))
			if !m.rewards[key] {
				m.rewards[key] = true
				res.Reward = m.cfg.RewardAmount
				m.addLedger(ts, userID, vehID, f.id, LedgerReward, res.Reward)
			}
		}
		if tid := m.maybeEvacuate(f.id, ts); tid != "" {
			res.TaskIDs = append(res.TaskIDs, tid)
		}
		m.last, m.has = ts, true
		return res, 0, why
	default:
		res.Outside = true
		res.Fee = m.cfg.OutsideFee
		m.addLedger(ts, userID, vehID, "", LedgerOutsideFee, res.Fee)
		v.state = VehicleInTransit
		res.TaskIDs = append(res.TaskIDs, m.newTask(RecallTask, "", m.cfg.DefaultOperatingFenceID, vehID, 1, ts))
		m.last, m.has = ts, true
		return res, 0, why
	}
}

func (m *naive) newTask(kind TaskKind, src, dst, veh string, move int, ts int64) string {
	id := fmt.Sprintf("T%d", len(m.tasks)+1)
	m.tasks = append(m.tasks, &mTask{
		id: id, kind: kind, status: TaskPending,
		src: src, dst: dst, veh: veh, move: move, created: ts,
	})
	return id
}

func (m *naive) maybeEvacuate(fenceID string, ts int64) string {
	f := m.fenceByID(fenceID)
	num, den := m.cfg.EvacuationNum, m.cfg.EvacuationDen
	if num <= 0 || f.cap <= 0 {
		return ""
	}
	count := int64(m.count(fenceID))
	if count*den < num*int64(f.cap) {
		return ""
	}
	for _, t := range m.tasks {
		if t.kind == EvacuateTask && t.src == fenceID && t.status != TaskCompleted {
			return ""
		}
	}
	dest := m.leastLoaded(f)
	if dest == "" {
		return ""
	}
	threshold := (num*int64(f.cap) + den - 1) / den
	move := int(count-threshold) + 1
	if move < 1 {
		move = 1
	}
	return m.newTask(EvacuateTask, fenceID, dest, "", move, ts)
}

func (m *naive) leastLoaded(f *mFence) string {
	best := ""
	bestCount := 0
	consider := func(id string) {
		c := m.count(id)
		if best == "" || c < bestCount || (c == bestCount && id < best) {
			best, bestCount = id, c
		}
	}
	if f.typ == Operating {
		for _, g := range m.fences {
			if g.parent == f.id && g.typ == Reward {
				consider(g.id)
			}
		}
		return best
	}
	if f.parent != "" {
		consider(f.parent)
	}
	for _, g := range m.fences {
		if g.id != f.id && g.parent == f.parent && g.typ == Reward {
			consider(g.id)
		}
	}
	return best
}

func (m *naive) claim(taskID, disp string, ts int64) ErrCode {
	if taskID == "" || disp == "" {
		return ErrInvalidArgument
	}
	if c := m.clock(ts); c != 0 {
		return c
	}
	var t *mTask
	for _, x := range m.tasks {
		if x.id == taskID {
			t = x
		}
	}
	if t == nil {
		return ErrTaskNotFound
	}
	if m.effStatus(t, ts) != TaskPending {
		return ErrTaskAlreadyClaimed
	}
	if t.status == TaskInProgress {
		t.claims[len(t.claims)-1].Expired = true
	}
	t.status = TaskInProgress
	t.claims = append(t.claims, ClaimRecord{DispatcherID: disp, ClaimedAt: ts})
	m.last, m.has = ts, true
	return 0
}

func (m *naive) complete(taskID string, p Point, ts int64) (CompleteResult, ErrCode, string) {
	var res CompleteResult
	if taskID == "" {
		return res, ErrInvalidArgument, "empty task id"
	}
	if c := m.clock(ts); c != 0 {
		return res, c, "clock rollback"
	}
	attr, why := m.locate(p)
	if attr.Type == Reward || attr.Type == Operating {
		f := m.fenceByID(attr.FenceID)
		if m.count(f.id) >= f.cap {
			return res, ErrFenceFull, fmt.Sprintf("%s; drop fence full", why)
		}
	}
	var task *mTask
	for _, x := range m.tasks {
		if x.id == taskID {
			task = x
		}
	}
	if task == nil {
		return res, ErrTaskNotFound, why
	}
	if m.effStatus(task, ts) != TaskInProgress {
		return res, ErrTaskNotInProgress, why
	}
	if attr.Type == NoParking || attr.Type == Outside {
		return res, ErrInvalidDropPoint, why
	}
	f := m.fenceByID(attr.FenceID)
	if task.kind == RecallTask {
		v := m.vehicles[task.veh]
		v.state, v.fence = VehicleParked, f.id
		res.Moved = 1
	} else {
		free := f.cap - m.count(f.id)
		var ids []string
		for id, v := range m.vehicles {
			if v.state == VehicleParked && v.fence == task.src {
				ids = append(ids, id)
			}
		}
		sort.Strings(ids)
		moved := 0
		for _, id := range ids {
			if moved >= task.move || moved >= free {
				break
			}
			m.vehicles[id].fence = f.id
			moved++
		}
		res.Moved = moved
	}
	task.status = TaskCompleted
	res.FenceID = f.id
	if tid := m.maybeEvacuate(f.id, ts); tid != "" {
		res.TaskIDs = append(res.TaskIDs, tid)
	}
	m.last, m.has = ts, true
	return res, 0, why
}

// --- differential test ----------------------------------------------------

// diffEnv drives a Service and the naive model through identical operation
// sequences and compares every outcome.
type diffEnv struct {
	t   *testing.T
	svc *Service
	m   *naive
	rng *rand.Rand
	ts  int64
}

func (e *diffEnv) codeOf(err error) ErrCode {
	if err == nil {
		return 0
	}
	return err.(*Error).Code
}

func (e *diffEnv) checkState(step int) {
	t := e.t
	d := e.svc.Dump()
	if d.LastTS != e.m.last {
		t.Fatalf("step %d: clock diverged: svc=%d model=%d", step, d.LastTS, e.m.last)
	}
	for _, f := range e.m.fences {
		if got, want := d.Counts[f.id], e.m.count(f.id); got != want {
			t.Fatalf("step %d: count[%s]=%d, model=%d", step, f.id, got, want)
		}
	}
	if len(d.Vehicles) != len(e.m.vehicles) {
		t.Fatalf("step %d: vehicles %d vs %d", step, len(d.Vehicles), len(e.m.vehicles))
	}
	for id, mv := range e.m.vehicles {
		dv, ok := d.Vehicles[id]
		if !ok || dv.State != mv.state || dv.FenceID != mv.fence {
			t.Fatalf("step %d: vehicle %s svc=%+v model=%+v", step, id, dv, mv)
		}
	}
	if len(d.Tasks) != len(e.m.tasks) {
		t.Fatalf("step %d: tasks %d vs %d", step, len(d.Tasks), len(e.m.tasks))
	}
	for i, mt := range e.m.tasks {
		dt := d.Tasks[i]
		if dt.ID != mt.id || dt.Kind != mt.kind || dt.SourceFenceID != mt.src ||
			dt.DestFenceID != mt.dst || dt.VehicleID != mt.veh ||
			dt.MoveCount != mt.move || dt.CreatedAt != mt.created {
			t.Fatalf("step %d: task %d svc=%+v model=%+v", step, i, dt, mt)
		}
		if want := e.m.effStatus(mt, e.m.last); dt.Status != want {
			t.Fatalf("step %d: task %s status svc=%v model=%v", step, mt.id, dt.Status, want)
		}
		if !reflect.DeepEqual(dt.Claims, mt.claims) {
			t.Fatalf("step %d: task %s claims svc=%+v model=%+v", step, mt.id, dt.Claims, mt.claims)
		}
	}
	if !reflect.DeepEqual(d.Ledger, e.m.ledger) {
		t.Fatalf("step %d: ledger svc=%+v model=%+v", step, d.Ledger, e.m.ledger)
	}
	if d.RewardsGranted != len(e.m.rewards) {
		t.Fatalf("step %d: rewards %d vs %d", step, d.RewardsGranted, len(e.m.rewards))
	}
}

func TestDifferentialRandom(t *testing.T) {
	cfg := testConfig()
	svc, err := NewService(cfg)
	if err != nil {
		t.Fatal(err)
	}
	e := &diffEnv{
		t:   t,
		svc: svc,
		m:   newNaive(cfg),
		rng: rand.New(rand.NewSource(20261007)),
		ts:  time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC).Unix(),
	}

	// Fence layout: four disjoint operating zones, each with a reward
	// fence touching its corner and a no-parking fence inside.
	type zone struct {
		x0, y0 int64
	}
	zones := []zone{{0, 0}, {100, 0}, {0, 100}, {100, 100}}
	var rewardPt, noparkPt, operPt, boundaryPt []Point
	for i, z := range zones {
		oid := fmt.Sprintf("O%d", i+1)
		rid := fmt.Sprintf("R%d", i+1)
		nid := fmt.Sprintf("N%d", i+1)
		for _, f := range []Fence{
			rect(oid, Operating, z.x0, z.y0, z.x0+60, z.y0+60, 3+i),
			rect(rid, Reward, z.x0, z.y0, z.x0+15, z.y0+15, 2),
			rect(nid, NoParking, z.x0+30, z.y0+30, z.x0+45, z.y0+45, 2),
		} {
			if err := e.svc.RegisterFence(f, e.ts); err != nil {
				t.Fatalf("register %s: %v", f.ID, err)
			}
			e.m.register(f, e.ts)
		}
		rewardPt = append(rewardPt, Point{z.x0 + 5, z.y0 + 5})
		noparkPt = append(noparkPt, Point{z.x0 + 35, z.y0 + 35})
		operPt = append(operPt, Point{z.x0 + 55, z.y0 + 55})
		boundaryPt = append(boundaryPt, Point{z.x0 + 7, z.y0})
	}
	outsidePts := []Point{{500, 500}, {-50, -50}, {80, 80}}
	pickPoint := func() Point {
		switch e.rng.Intn(6) {
		case 0:
			return rewardPt[e.rng.Intn(len(rewardPt))]
		case 1:
			return noparkPt[e.rng.Intn(len(noparkPt))]
		case 2, 3:
			return operPt[e.rng.Intn(len(operPt))]
		case 4:
			return boundaryPt[e.rng.Intn(len(boundaryPt))]
		default:
			return outsidePts[e.rng.Intn(len(outsidePts))]
		}
	}

	const totalVehicles = 40
	added := 0
	vehicleIDs := func() string {
		if added == 0 || e.rng.Intn(20) == 0 {
			return "v-ghost" // occasionally an unknown vehicle
		}
		return fmt.Sprintf("v%d", e.rng.Intn(added))
	}
	taskIDs := func() string {
		if len(e.m.tasks) == 0 || e.rng.Intn(10) == 0 {
			return fmt.Sprintf("T%d", len(e.m.tasks)+1) // likely unknown
		}
		return e.m.tasks[e.rng.Intn(len(e.m.tasks))].id
	}

	const steps = 3000
	for step := 0; step < steps; step++ {
		e.ts += e.rng.Int63n(4)
		// Occasionally roll the clock back: both sides must reject.
		rollback := e.rng.Intn(50) == 0
		ts := e.ts
		if rollback {
			ts -= 1000
		}
		op := e.rng.Intn(100)
		switch {
		case op < 10 && added < totalVehicles:
			id := fmt.Sprintf("v%d", added)
			sErr := e.svc.AddVehicle(id, ts)
			mCode := e.m.addVehicle(id, ts)
			e.logf(step, "AddVehicle(%s,%d)", id, ts)
			e.mustSameCode(step, sErr, mCode)
			if mCode == 0 {
				added++
			}
		case op < 30:
			id := vehicleIDs()
			sErr := e.svc.Unlock(id, ts)
			mCode := e.m.unlock(id, ts)
			e.logf(step, "Unlock(%s,%d)", id, ts)
			e.mustSameCode(step, sErr, mCode)
		case op < 60:
			id := vehicleIDs()
			user := fmt.Sprintf("u%d", e.rng.Intn(4))
			p := pickPoint()
			sRes, sErr := e.svc.Return(id, user, p, ts)
			mRes, mCode, why := e.m.ret(id, user, p, ts)
			e.logf(step, "Return(%s,%s,%v,%d) -> %v", id, user, p, ts, why)
			e.mustSameCode(step, sErr, mCode)
			if mCode == 0 && !reflect.DeepEqual(sRes, mRes) {
				t.Fatalf("step %d: return result svc=%+v model=%+v", step, sRes, mRes)
			}
		case op < 75:
			id := taskIDs()
			disp := fmt.Sprintf("d%d", e.rng.Intn(3))
			sErr := e.svc.ClaimTask(id, disp, ts)
			mCode := e.m.claim(id, disp, ts)
			e.logf(step, "ClaimTask(%s,%s,%d)", id, disp, ts)
			e.mustSameCode(step, sErr, mCode)
		case op < 90:
			id := taskIDs()
			p := pickPoint()
			sRes, sErr := e.svc.CompleteTask(id, p, ts)
			mRes, mCode, why := e.m.complete(id, p, ts)
			e.logf(step, "CompleteTask(%s,%v,%d) -> %v", id, p, ts, why)
			e.mustSameCode(step, sErr, mCode)
			if mCode == 0 && !reflect.DeepEqual(sRes, mRes) {
				t.Fatalf("step %d: complete result svc=%+v model=%+v", step, sRes, mRes)
			}
		default:
			p := pickPoint()
			_, why := e.m.locate(p)
			got := e.svc.Locate(p)
			want, _ := e.m.locate(p)
			e.logf(step, "Locate(%v) -> %v", p, why)
			if got != want {
				t.Fatalf("step %d: Locate(%v) svc=%+v model=%+v", step, p, got, want)
			}
		}
		e.checkState(step)
	}
	t.Logf("differential run finished: %d vehicles, %d tasks, %d ledger entries",
		len(e.m.vehicles), len(e.m.tasks), len(e.m.ledger))
}

func (e *diffEnv) logf(step int, format string, args ...any) {
	e.t.Logf("step %04d ts=%d %s", step, e.ts, fmt.Sprintf(format, args...))
}

func (e *diffEnv) mustSameCode(step int, sErr error, mCode ErrCode) {
	e.t.Helper()
	sCode := e.codeOf(sErr)
	if sCode != mCode {
		e.t.Fatalf("step %d: error code svc=%v model=%v", step, sCode, mCode)
	}
}

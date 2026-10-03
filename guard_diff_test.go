package ontology

import (
	"fmt"
	"math/rand"
	"strings"
	"testing"

	import_breaker "ontology/breaker"
)

// 本文件按题面规则逐步书写一份朴素参考模型，与真实 Guard 在相同随机
// 操作序列下逐操作比对结果与全部可观测状态，并记录输入/输出/判定依据。

type refState int

const (
	refClosed refState = iota
	refOpen
	refHalfOpen
)

type refIDState int

const (
	refQueued refIDState = iota + 1
	refActive
	refReleased
	refTimedOut
	refCancelled
)

type refEntry struct{ failed, slow bool }

type refRec struct {
	state refIDState
	epoch int64
}

type refWaiter struct {
	id int64
	at int64
}

type refModel struct {
	p                         Params
	state                     refState
	epoch                     int64
	openedAt                  int64
	probeIssued, probeSuccess int
	win                       []refEntry // 朴素：直接切片保留窗内全部元素
	winFail, winSlow          int
	active                    int
	queue                     []refWaiter
	records                   map[int64]*refRec
	nextID                    int64
	maxNow                    int64
	trace                     strings.Builder
}

func newRefModel(p Params) *refModel {
	return &refModel{p: p, records: map[int64]*refRec{}, nextID: 1}
}

func (m *refModel) log(format string, args ...any) {
	fmt.Fprintf(&m.trace, "    | "+format+"\n", args...)
}

type refOutcome int

const (
	outGranted refOutcome = iota
	outQueued
	outOpen
	outHalfFull
	outFull
	outErr
)

type refResult struct {
	out refOutcome
	id  int64
	err string
}

// settle 对应题面结算两步：超时出队；Open 到期转 HalfOpen。
func (m *refModel) settle(now int64) {
	i := 0
	for i < len(m.queue) {
		w := m.queue[i]
		if w.at+m.p.Wt > now {
			break
		}
		m.records[w.id].state = refTimedOut
		m.log("settle: id=%d enqueuedAt=%d +Wt=%d <= now=%d -> timeout", w.id, w.at, m.p.Wt, now)
		i++
	}
	m.queue = m.queue[i:]
	if m.state == refOpen && now >= m.openedAt+m.p.O {
		m.state = refHalfOpen
		m.epoch++
		m.probeIssued = 0
		m.probeSuccess = 0
		m.log("settle: now=%d >= openedAt=%d+O=%d -> HalfOpen epoch=%d", now, m.openedAt, m.p.O, m.epoch)
	}
}

func (m *refModel) checkTime(now int64) string {
	if now < 0 || now > 1_000_000_000_000_000 {
		return "invalid time"
	}
	if now < m.maxNow {
		return "clock rollback"
	}
	return ""
}

func (m *refModel) acquire(now int64) refResult {
	if e := m.checkTime(now); e != "" {
		m.log("Acquire(now=%d) ERROR %s (state unchanged)", now, e)
		return refResult{out: outErr, err: e}
	}
	m.maxNow = now
	m.settle(now)
	switch m.state {
	case refOpen:
		m.log("Acquire(now=%d) -> reject Open", now)
		return refResult{out: outOpen}
	case refHalfOpen:
		if m.probeIssued >= m.p.H {
			m.log("Acquire(now=%d) -> reject HalfOpenFull (issued=%d H=%d)", now, m.probeIssued, m.p.H)
			return refResult{out: outHalfFull}
		}
		if m.active >= m.p.C {
			m.log("Acquire(now=%d) -> reject Full (no free permit in HalfOpen)", now)
			return refResult{out: outFull}
		}
		id := m.nextID
		m.nextID++
		m.records[id] = &refRec{state: refActive, epoch: m.epoch}
		m.active++
		m.probeIssued++
		m.log("Acquire(now=%d) -> Granted id=%d as probe (issued=%d)", now, id, m.probeIssued)
		return refResult{out: outGranted, id: id}
	default:
		if m.active < m.p.C {
			id := m.nextID
			m.nextID++
			m.records[id] = &refRec{state: refActive, epoch: m.epoch}
			m.active++
			m.log("Acquire(now=%d) -> Granted id=%d", now, id)
			return refResult{out: outGranted, id: id}
		}
		if len(m.queue) < m.p.Q {
			id := m.nextID
			m.nextID++
			m.records[id] = &refRec{state: refQueued}
			m.queue = append(m.queue, refWaiter{id: id, at: now})
			m.log("Acquire(now=%d) -> Queued id=%d qlen=%d", now, id, len(m.queue))
			return refResult{out: outQueued, id: id}
		}
		m.log("Acquire(now=%d) -> reject Full (active=%d qlen=%d)", now, m.active, len(m.queue))
		return refResult{out: outFull}
	}
}

func (m *refModel) addWindow(failed, slow bool) {
	if len(m.win) == m.p.N {
		old := m.win[0]
		m.win = m.win[1:]
		if old.failed {
			m.winFail--
		}
		if old.slow {
			m.winSlow--
		}
	}
	m.win = append(m.win, refEntry{failed, slow})
	if failed {
		m.winFail++
	}
	if slow {
		m.winSlow++
	}
}

func (m *refModel) trip(now int64) {
	m.state = refOpen
	m.epoch++
	m.openedAt = now
	m.probeIssued = 0
	m.probeSuccess = 0
	m.win = nil
	m.winFail = 0
	m.winSlow = 0
	for _, w := range m.queue {
		m.records[w.id].state = refCancelled
		m.log("trip: id=%d cancelled in queue order", w.id)
	}
	m.queue = nil
	m.log("-> Open openedAt=%d epoch=%d, queue cancelled, ring cleared", now, m.epoch)
}

func (m *refModel) toClosed() {
	m.state = refClosed
	m.epoch++
	m.probeIssued = 0
	m.probeSuccess = 0
	m.win = nil
	m.winFail = 0
	m.winSlow = 0
	m.log("-> Closed epoch=%d ring cleared", m.epoch)
}

func (m *refModel) release(id int64, ok bool, dur, now int64) refResult {
	if id <= 0 {
		m.log("Release(id=%d) ERROR invalid id", id)
		return refResult{out: outErr, err: "invalid argument"}
	}
	if dur < 0 {
		m.log("Release(id=%d dur=%d) ERROR invalid dur", id, dur)
		return refResult{out: outErr, err: "invalid argument"}
	}
	if e := m.checkTime(now); e != "" {
		m.log("Release(id=%d now=%d) ERROR %s", id, now, e)
		return refResult{out: outErr, err: e}
	}
	rec, known := m.records[id]
	if !known || rec.state != refActive {
		m.log("Release(id=%d) ERROR permit not active (known=%v)", id, known)
		return refResult{out: outErr, err: "permit not active"}
	}
	m.maxNow = now
	m.settle(now)
	permEpoch := rec.epoch
	rec.state = refReleased
	m.active--
	m.log("Release(id=%d ok=%v dur=%d now=%d) permit returned, epochAtGrant=%d curEpoch=%d",
		id, ok, dur, now, permEpoch, m.epoch)

	if permEpoch == m.epoch {
		switch m.state {
		case refClosed:
			failed, slow := !ok, dur >= m.p.S
			m.addWindow(failed, slow)
			n := len(m.win)
			should := n >= m.p.M &&
				(m.winFail*100 >= m.p.F*n || m.winSlow*100 >= m.p.SR*n)
			m.log("ring n=%d fail=%d slow=%d -> trip=%v (failCheck=%v slowCheck=%v)",
				n, m.winFail, m.winSlow, should,
				n >= m.p.M && m.winFail*100 >= m.p.F*n,
				n >= m.p.M && m.winSlow*100 >= m.p.SR*n)
			if should {
				m.trip(now)
			}
		case refHalfOpen:
			if !ok || dur >= m.p.S {
				m.log("probe id=%d bad (ok=%v dur=%d>=S=%v) -> reopen", id, ok, dur, dur >= m.p.S)
				m.trip(now)
			} else {
				m.probeSuccess++
				m.log("probe id=%d success (%d/%d)", id, m.probeSuccess, m.p.H)
				if m.probeSuccess >= m.p.H {
					m.toClosed()
				}
			}
		}
	} else {
		m.log("stale epoch: result not recorded in ring")
	}

	if m.state == refClosed {
		for m.active < m.p.C && len(m.queue) > 0 {
			w := m.queue[0]
			m.queue = m.queue[1:]
			m.active++
			m.records[w.id].state = refActive
			m.records[w.id].epoch = m.epoch
			m.log("grant queued id=%d at now=%d (becomes active, epoch=%d)", w.id, now, m.epoch)
		}
	}
	return refResult{}
}

func (m *refModel) status(id int64, now int64) (refIDState, string) {
	if id <= 0 {
		return 0, "invalid argument"
	}
	if e := m.checkTime(now); e != "" {
		return 0, e
	}
	rec, known := m.records[id]
	if !known {
		return 0, "unknown id"
	}
	m.maxNow = now
	m.settle(now)
	return rec.state, ""
}

// observe 模拟 Snapshot 操作：时间合法则推进 maxNow 并结算，返回是否合法。
func (m *refModel) observe(now int64) bool {
	if m.checkTime(now) != "" {
		return false
	}
	m.maxNow = now
	m.settle(now)
	return true
}

type opKind int

const (
	opAcquire opKind = iota
	opRelease
	opStatus
)

type randOp struct {
	kind     opKind
	now      int64
	id       int64
	ok       bool
	dur      int64
	timeMode int // 0 正常递增；1 回退；2 越界
}

func errorCategory(err error) string {
	switch {
	case err == nil:
		return ""
	case isSentinel(err, ErrInvalidArgument):
		return "invalid argument"
	case isSentinel(err, ErrInvalidTime):
		return "invalid time"
	case isSentinel(err, ErrClockRollback):
		return "clock rollback"
	case isSentinel(err, ErrPermitNotActive):
		return "permit not active"
	case isSentinel(err, ErrUnknownID):
		return "unknown id"
	default:
		return err.Error()
	}
}

func isSentinel(err, target error) bool {
	for err != nil {
		if err == target {
			return true
		}
		type unwrapper interface{ Unwrap() error }
		u, ok := err.(unwrapper)
		if !ok {
			return false
		}
		err = u.Unwrap()
	}
	return false
}

var outcomeName = map[refOutcome]string{
	outGranted: "Granted", outQueued: "Queued",
	outOpen: "RejectOpen", outHalfFull: "RejectHalfOpenFull",
	outFull: "RejectFull", outErr: "Error",
}

var stateName = map[refState]string{refClosed: "Closed", refOpen: "Open", refHalfOpen: "HalfOpen"}

// randomParams 选取小而多样的参数，使全部行为都能在短序列中出现。
func randomParams(r *rand.Rand) Params {
	n := 1 + r.Intn(6)
	m := 1 + r.Intn(n)
	return Params{
		N: n, M: m,
		F:  1 + r.Intn(100),
		SR: 1 + r.Intn(100),
		S:  int64(1 + r.Intn(120)),
		O:  int64(1 + r.Intn(12)),
		Wt: int64(1 + r.Intn(10)),
		H:  1 + r.Intn(4),
		C:  1 + r.Intn(4),
		Q:  r.Intn(4),
	}
}

func generateOps(r *rand.Rand, length int) []randOp {
	var ops []randOp
	var now int64
	for i := 0; i < length; i++ {
		op := randOp{}
		switch r.Intn(10) {
		case 0, 1, 2, 3, 4:
			op.kind = opAcquire
		case 5, 6, 7, 8:
			op.kind = opRelease
		default:
			op.kind = opStatus
		}
		// 时间：约 12% 异常（回退或越界），验证错误路径与“不改变状态”。
		op.timeMode = 0
		if r.Intn(8) == 0 {
			if r.Intn(2) == 0 {
				op.timeMode = 1
			} else {
				op.timeMode = 2
			}
		}
		now += int64(r.Intn(5))
		op.now = now
		if op.timeMode == 1 && now > 0 {
			op.now = now - int64(1+r.Intn(3))
		}
		if op.timeMode == 2 {
			op.now = 1_000_000_000_000_001
		}
		if op.kind != opAcquire {
			op.id = -1 // 由 playBoth 按统一规则选取，保证两边一致
		}
		if op.kind == opRelease {
			op.ok = r.Intn(100) < 45
			op.dur = int64(r.Intn(160))
			if r.Intn(12) == 0 {
				op.dur = -int64(1 + r.Intn(5))
			}
		}
		ops = append(ops, op)
	}
	return ops
}

func realOutcome(r AcquireResult) refOutcome {
	switch r.Outcome {
	case Granted:
		return outGranted
	case Queued:
		return outQueued
	case RejectedOpen:
		return outOpen
	case RejectedHalfOpenFull:
		return outHalfFull
	default:
		return outFull
	}
}

func realStatus(s IDStatus) refIDState {
	switch s {
	case StatusQueued:
		return refQueued
	case StatusActive:
		return refActive
	case StatusReleased:
		return refReleased
	case StatusTimedOut:
		return refTimedOut
	default:
		return refCancelled
	}
}

// TestDifferentialNaiveModel 与朴素模型对照 2000 组随机序列，
// 每组失败时打印含输入、输出与判定依据的完整日志。
func TestDifferentialNaiveModel(t *testing.T) {
	const groups, length = 2000, 60
	for seed := int64(1); seed <= groups; seed++ {
		r := rand.New(rand.NewSource(seed))
		p := randomParams(r)
		ops := generateOps(r, length)
		msg, ok := playBoth(t, p, ops, false)
		if !ok {
			t.Fatalf("seed=%d params=%+v mismatch:\n%s", seed, p, msg)
		}
	}
}

// TestDeterministicReplay 相同序列重放两次，结果与快照完全一致。
func TestDeterministicReplay(t *testing.T) {
	for seed := int64(1); seed <= 50; seed++ {
		r := rand.New(rand.NewSource(10_000 + seed))
		p := randomParams(r)
		ops := generateOps(r, 80)
		first, ok1 := playBoth(t, p, ops, false)
		second, ok2 := playBoth(t, p, ops, false)
		if !ok1 || !ok2 || first != second {
			t.Fatalf("seed=%d replay differs", seed)
		}
	}
}

// TestDifferentialSampleLog 在 -v 模式下打印一组完整的输入/输出/判定依据日志。
func TestDifferentialSampleLog(t *testing.T) {
	r := rand.New(rand.NewSource(42))
	p := randomParams(r)
	ops := generateOps(r, 40)
	msg, ok := playBoth(t, p, ops, true)
	if !ok {
		t.Fatalf("sample mismatch:\n%s", msg)
	}
	t.Logf("differential sample log:\n%s", msg)
}

// playBoth 在真实 Guard 与朴素模型上执行同一序列，逐操作比对。
// 返回完整日志与是否一致。id<0 的操作按共享 rng 选取：
// 70% 从真实已发编号池中取（覆盖在役/排队/已归还/超时/撤销），
// 30% 编造未发 id。两边使用完全相同的输入序列。
func playBoth(t *testing.T, p Params, ops []randOp, verbose bool) (string, bool) {
	t.Helper()
	g, err := New(p)
	if err != nil {
		t.Fatalf("params %+v unexpectedly invalid: %v", p, err)
	}
	m := newRefModel(p)
	seed := timeSeed(p, ops)
	r := rand.New(rand.NewSource(seed))
	var log strings.Builder
	fmt.Fprintf(&log, "params=%+v (model rng-seed=%d)\n", p, seed)

	chooseID := func() int64 {
		var maxID int64
		for id := range g.records {
			if id > maxID {
				maxID = id
			}
		}
		if maxID > 0 && r.Intn(10) < 7 {
			return int64(1 + r.Intn(int(maxID))) // 可能命中任何结局，也可能未发
		}
		return maxID + int64(1+r.Intn(3))
	}

	for i, op := range ops {
		if op.kind != opAcquire {
			op.id = chooseID()
			ops[i].id = op.id // 固定下来，保证重放一致
		}
		fmt.Fprintf(&log, "[%02d] ", i)
		switch op.kind {
		case opAcquire:
			fmt.Fprintf(&log, "Acquire(now=%d)\n", op.now)
			got, gerr := g.Acquire(op.now)
			want := m.acquire(op.now)
			if gerr != nil {
				if want.out != outErr || errorCategory(gerr) != want.err {
					return finishLog(&log, &m.trace, verbose,
						"Acquire error mismatch: real=%v(%s) wantErr=%s", got, errorCategory(gerr), want.err)
				}
				fmt.Fprintf(&log, "    => ERROR %s\n", errorCategory(gerr))
			} else {
				if realOutcome(got) != want.out || got.ID != want.id {
					return finishLog(&log, &m.trace, verbose,
						"Acquire mismatch: real=%s(%d) want=%s(%d)",
						outcomeName[realOutcome(got)], got.ID, outcomeName[want.out], want.id)
				}
				fmt.Fprintf(&log, "    => %s id=%d\n", outcomeName[want.out], want.id)
			}
		case opRelease:
			fmt.Fprintf(&log, "Release(id=%d ok=%v dur=%d now=%d)\n", op.id, op.ok, op.dur, op.now)
			gerr := g.Release(op.id, op.ok, op.dur, op.now)
			want := m.release(op.id, op.ok, op.dur, op.now)
			if (gerr != nil) != (want.out == outErr) ||
				(gerr != nil && errorCategory(gerr) != want.err) {
				return finishLog(&log, &m.trace, verbose,
					"Release mismatch: realErr=%s wantErr=%s", errorCategory(gerr), want.err)
			}
			if gerr != nil {
				fmt.Fprintf(&log, "    => ERROR %s\n", errorCategory(gerr))
			} else {
				fmt.Fprintf(&log, "    => ok\n")
			}
		case opStatus:
			fmt.Fprintf(&log, "Status(id=%d now=%d)\n", op.id, op.now)
			got, gerr := g.Status(op.id, op.now)
			want, werr := m.status(op.id, op.now)
			if (gerr != nil) != (werr != "") ||
				(gerr != nil && errorCategory(gerr) != werr) {
				return finishLog(&log, &m.trace, verbose,
					"Status mismatch: realErr=%s wantErr=%s", errorCategory(gerr), werr)
			}
			if gerr == nil && realStatus(got) != want {
				return finishLog(&log, &m.trace, verbose,
					"Status mismatch: real=%d want=%d", got, want)
			}
			if gerr != nil {
				fmt.Fprintf(&log, "    => ERROR %s\n", errorCategory(gerr))
			} else {
				fmt.Fprintf(&log, "    => status=%d\n", want)
			}
		}
		log.WriteString(m.trace.String())
		m.trace.Reset()

		// 操作后对称地做一次 Snapshot（时间非法的错误操作两边都不推进）。
		if m.checkTime(op.now) == "" {
			snapshot, snapErr := g.Snapshot(op.now)
			if snapErr != nil {
				return finishLog(&log, &m.trace, verbose,
					"unexpected snapshot error: %v", snapErr)
			}
			if !m.observe(op.now) {
				return finishLog(&log, &m.trace, verbose, "ref observe unexpectedly failed")
			}
			gs := snapshot
			ws := currentSnap(m)
			if gs.State != breakerState(ws.state) || gs.Epoch != ws.epoch ||
				gs.Count != ws.count || gs.Failed != ws.failed || gs.Slow != ws.slow ||
				gs.Active != ws.active || gs.Queued != ws.queued {
				return finishLog(&log, &m.trace, verbose,
					"snapshot mismatch: real=%+v want=%+v", gs, ws)
			}
		}

		// 不变量：已发编号数 == 五种结局 id 数之和；在役<=C、队列<=Q；
		// Open/HalfOpen 队列必空。
		if !checkInvariants(t, g, op.now, &log) {
			return finishLog(&log, &m.trace, verbose, "invariant violated")
		}
	}
	if verbose {
		return log.String(), true
	}
	return log.String(), true
}

func finishLog(head, trace *strings.Builder, verbose bool, format string, args ...any) (string, bool) {
	fmt.Fprintf(head, "!!! MISMATCH: "+format+"\n", args...)
	head.WriteString(trace.String())
	return head.String(), false
}

type refSnap struct {
	state               refState
	epoch               int64
	count, failed, slow int
	active, queued      int
}

func currentSnap(m *refModel) refSnap {
	return refSnap{
		state: m.state, epoch: m.epoch,
		count: len(m.win), failed: m.winFail, slow: m.winSlow,
		active: m.active, queued: len(m.queue),
	}
}

func breakerState(s refState) (out import_breaker.State) {
	switch s {
	case refOpen:
		return import_breaker.Open
	case refHalfOpen:
		return import_breaker.HalfOpen
	default:
		return import_breaker.Closed
	}
}

func checkInvariants(t *testing.T, g *Guard, now int64, log *strings.Builder) bool {
	t.Helper()
	snap, err := g.Snapshot(now)
	if err != nil {
		return true // 回退时刻无法快照，跳过
	}
	if snap.Active < 0 || snap.Active > capPermits(g) || snap.Queued < 0 || snap.Queued > g.bh.QueueCap() {
		fmt.Fprintf(log, "invariant: active=%d queued=%d out of bounds C=%d Q=%d\n",
			snap.Active, snap.Queued, capPermits(g), g.bh.QueueCap())
		return false
	}
	if snap.State != import_breaker.Closed && snap.Queued != 0 {
		fmt.Fprintf(log, "invariant: queue non-empty in state=%v\n", snap.State)
		return false
	}
	// 已发编号数恒等于五种结局 id 数之和（每个已发 id 恰有一个结局）。
	var nQ, nA, nR, nT, nC int
	for _, rec := range g.records {
		switch rec.state {
		case stQueued:
			nQ++
		case stActive:
			nA++
		case stReleased:
			nR++
		case stTimedOut:
			nT++
		case stCancelled:
			nC++
		}
	}
	if total := nQ + nA + nR + nT + nC; int64(total) != g.nextID-1 {
		fmt.Fprintf(log, "invariant: issued=%d but outcomes sum=%d (q=%d a=%d r=%d t=%d c=%d)\n",
			g.nextID-1, total, nQ, nA, nR, nT, nC)
		return false
	}
	if snap.Active != nA || snap.Queued != nQ {
		fmt.Fprintf(log, "invariant: ledger active=%d/%d queued=%d/%d\n",
			snap.Active, nA, snap.Queued, nQ)
		return false
	}
	return true
}

func capPermits(g *Guard) int { return g.bh.Active() + g.bh.Free() }

func timeSeed(p Params, ops []randOp) int64 {
	h := int64(17)
	add := func(x int64) { h = h*31 + x }
	add(int64(p.N))
	add(int64(p.M))
	add(int64(p.F))
	add(int64(p.SR))
	add(p.S)
	add(p.O)
	add(p.Wt)
	add(int64(p.H))
	add(int64(p.C))
	add(int64(p.Q))
	for _, op := range ops {
		add(int64(op.kind))
		add(op.now)
		add(op.dur)
		if op.ok {
			add(1)
		}
	}
	if h < 0 {
		h = -h
	}
	return h
}

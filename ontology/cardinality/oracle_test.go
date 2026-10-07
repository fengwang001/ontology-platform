package cardinality

import (
	"fmt"
	"math/rand"
	"sort"
	"sync"
	"testing"
	"time"
)

// oracleReservation 是朴素串行参考模型中一个进行中预留。
type oracleReservation struct {
	id       string
	deadline int64
}

// NaiveOracle 是一个独立实现的、朴素的全局串行名额分配模型：
// 所有操作严格按调用顺序逐个执行，用一把大锁串行化，判定规则与
// Manager 完全相同（先回收过期，再基线冲突、已确认满、进行中占用）。
// 它不共享 Manager 的任何代码，仅按需求文字独立实现，用作对照基准。
type NaiveOracle struct {
	mu        sync.Mutex
	limit     int64
	committed int64
	clock     int64
	ttl       int64
	active    map[string]oracleReservation
	log       []Event
}

func NewNaiveOracle(limit int64, ttl time.Duration, startNanos int64) *NaiveOracle {
	return &NaiveOracle{
		limit:  limit,
		clock:  startNanos,
		ttl:    ttl.Nanoseconds(),
		active: make(map[string]oracleReservation),
	}
}

func (o *NaiveOracle) advance(now int64) {
	if now > o.clock {
		o.clock = now
	}
}

// reapLocked 删除全部已过租约的预留（deadline <= now），
// 与生产实现保持同一边界语义。
func (o *NaiveOracle) reapLocked(now int64) {
	for id, r := range o.active {
		if r.deadline <= now {
			delete(o.active, id)
			o.log = append(o.log, Event{
				Kind: EventExpire, Seq: int64(len(o.log)),
				Scope: testScope, ReservationID: id,
				Committed: o.committed, Inflight: len(o.active), NowNanos: now,
			})
		}
	}
}

func (o *NaiveOracle) Reserve(id string, expectedV, now int64) (Decision, int64) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.advance(now)
	o.reapLocked(o.clock)

	d := Decision{Scope: testScope, ExpectedV: expectedV, ReservationID: id, NowNanos: o.clock}
	live := len(o.active)
	d.ObservedV = o.committed

	switch {
	case expectedV != o.committed:
		d.Reason = ReasonBaselineConflict
	case o.committed >= o.limit:
		d.Reason = ReasonCommittedFull
	case o.committed+int64(live) >= o.limit:
		d.Reason = ReasonInflightOccupied
	default:
		d.Accepted = true
		deadline := o.clock + o.ttl
		o.active[id] = oracleReservation{id: id, deadline: deadline}
		live++
		d.Snapshot = CounterSnapshot{
			Committed: o.committed, Inflight: live, Limit: o.limit,
			Remaining: o.limit - o.committed - int64(live), Seq: o.committed,
		}
		d.Seq = int64(len(o.log))
		o.log = append(o.log, Event{
			Kind: EventReserve, Seq: d.Seq, Scope: testScope, ReservationID: id,
			Decision: copyDecision(&d), Committed: o.committed,
			Inflight: len(o.active), NowNanos: o.clock,
		})
		return d, deadline
	}

	d.Snapshot = CounterSnapshot{
		Committed: o.committed, Inflight: live, Limit: o.limit,
		Remaining: o.limit - o.committed - int64(live), Seq: o.committed,
	}
	d.Seq = int64(len(o.log))
	o.log = append(o.log, Event{
		Kind: EventReserve, Seq: d.Seq, Scope: testScope, ReservationID: id,
		Decision: copyDecision(&d), Committed: o.committed,
		Inflight: len(o.active), NowNanos: o.clock,
	})
	return d, 0
}

func (o *NaiveOracle) finalize(id string, now int64, success bool) bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.advance(now)
	o.reapLocked(o.clock)
	r, ok := o.active[id]
	if !ok {
		return false
	}
	if r.deadline <= o.clock {
		delete(o.active, id)
		return false
	}
	delete(o.active, id)
	if success {
		o.committed++
	}
	kind := EventAbort
	if success {
		kind = EventCommit
	}
	o.log = append(o.log, Event{
		Kind: kind, Seq: int64(len(o.log)), Scope: testScope, ReservationID: id,
		Committed: o.committed, Inflight: len(o.active), NowNanos: o.clock,
	})
	return true
}

func (o *NaiveOracle) Commit(id string, now int64) bool { return o.finalize(id, now, true) }
func (o *NaiveOracle) Abort(id string, now int64) bool  { return o.finalize(id, now, false) }

func (o *NaiveOracle) Sweep(now int64) []string {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.advance(now)
	before := len(o.active)
	o.reapLocked(o.clock)
	if len(o.active) == before {
		return nil
	}
	reaped := make([]string, 0, before-len(o.active))
	// 收割结果从日志中提取，保证顺序无关的 ID 集合比较。
	for i := len(o.log) - (before - len(o.active)); i < len(o.log); i++ {
		reaped = append(reaped, o.log[i].ReservationID)
	}
	return reaped
}

// reapForReplay 供审计重放使用：在指定时刻执行一次收割。
func (o *NaiveOracle) reapForReplay(now int64) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.advance(now)
	o.reapLocked(o.clock)
}

// expireOneForReplay 按审计顺序逐个重放过期事件：只删除该 ID，
// 与管理器在一次 sweep 中逐个记录 EventExpire 的顺序精确对齐。
func (o *NaiveOracle) expireOneForReplay(id string, now int64) bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.advance(now)
	r, ok := o.active[id]
	if !ok || r.deadline > o.clock {
		return false
	}
	delete(o.active, id)
	return true
}

func copyDecision(d *Decision) *Decision {
	cp := *d
	return &cp
}

// opKind 标识脚本操作类型。
type opKind int

const (
	opReserve opKind = iota
	opCommit
	opAbort
	opSweep
	opAdvance
)

type scriptOp struct {
	kind      opKind
	id        string
	expectedV int64
	timeNanos int64
}

type opResult struct {
	accepted bool
	reason   ReasonCode
	outcome  bool // commit/abort/sweep 语义化结果
	swept    []string
}

// 生成一个随机操作脚本：在单作用域上交替进行预留、提交、中止、
// 时钟推进（触发租约过期）与显式清扫。expectedV 以一定概率故意
// 落后，用来覆盖基线冲突分支。
func generateScript(rng *rand.Rand, n int, ttlNanos int64) []scriptOp {
	var ops []scriptOp
	var live []string
	now := int64(0)
	version := int64(0)
	idCounter := 0
	newID := func() string {
		idCounter++
		return fmt.Sprintf("g%04d", idCounter)
	}

	for len(ops) < n {
		roll := rng.Intn(100)
		switch {
		case roll < 55:
			ev := version
			if rng.Intn(5) == 0 && version > 0 {
				ev = rng.Int63n(version) // 故意使用落后基线
			}
			id := newID()
			ops = append(ops, scriptOp{kind: opReserve, id: id, expectedV: ev, timeNanos: now})
			live = append(live, id)
			// 模型中无法预知是否真的获批，执行侧负责以真实存活集合终结。
		case roll < 75 && len(live) > 0:
			idx := rng.Intn(len(live))
			id := live[idx]
			live = append(live[:idx], live[idx+1:]...)
			success := rng.Intn(2) == 0
			k := opCommit
			if !success {
				k = opAbort
			}
			ops = append(ops, scriptOp{kind: k, id: id, timeNanos: now})
			if k == opCommit {
				version++
			}
		case roll < 85:
			jump := rng.Int63n(ttlNanos * 3)
			now += jump
			ops = append(ops, scriptOp{kind: opAdvance, timeNanos: now})
			live = nil // 生成器侧无法精确知道哪些过期，仅用于概率调度
		default:
			ops = append(ops, scriptOp{kind: opSweep, timeNanos: now})
		}
	}
	return ops
}

// runScriptOnManager 在真实 Manager 上按脚本串行执行（FakeClock 注入）。
func runScriptOnManager(t *testing.T, m *Manager, clk *FakeClock, ops []scriptOp) []opResult {
	t.Helper()
	results := make([]opResult, len(ops))
	for i, op := range ops {
		switch op.kind {
		case opAdvance:
			clk.Set(op.timeNanos)
		case opReserve:
			d, _ := m.Reserve(ReserveRequest{
				Scope: testScope, ExpectedV: op.expectedV, ReservationID: op.id,
			})
			results[i] = opResult{accepted: d.Accepted, reason: d.Reason}
		case opCommit:
			results[i] = opResult{outcome: m.Commit(op.id)}
		case opAbort:
			results[i] = opResult{outcome: m.Abort(op.id)}
		case opSweep:
			results[i] = opResult{swept: m.Sweep()}
		}
	}
	return results
}

// runScriptOnOracle 在朴素串行模型上执行同一脚本。
func runScriptOnOracle(o *NaiveOracle, ops []scriptOp) []opResult {
	results := make([]opResult, len(ops))
	for i, op := range ops {
		switch op.kind {
		case opAdvance:
			o.advance(op.timeNanos)
		case opReserve:
			d, _ := o.Reserve(op.id, op.expectedV, op.timeNanos)
			results[i] = opResult{accepted: d.Accepted, reason: d.Reason}
		case opCommit:
			results[i] = opResult{outcome: o.Commit(op.id, op.timeNanos)}
		case opAbort:
			results[i] = opResult{outcome: o.Abort(op.id, op.timeNanos)}
		case opSweep:
			results[i] = opResult{swept: o.Sweep(op.timeNanos)}
		}
	}
	return results
}

func sortStrings(s []string) []string {
	cp := append([]string(nil), s...)
	sort.Strings(cp)
	return cp
}

func eqStringSet(a, b []string) bool {
	xa, xb := sortStrings(a), sortStrings(b)
	if len(xa) != len(xb) {
		return false
	}
	for i := range xa {
		if xa[i] != xb[i] {
			return false
		}
	}
	return true
}

// 随机脚本 × 双实现对照：每一步的接受/拒绝原因、终结结果、
// 收割集合与最终关联数必须完全一致；这即证明存在一种等价串行顺序
// （脚本顺序），并发实现的结果与朴素全局串行模型一致。
func TestRandomizedScriptMatchesNaiveOracle(t *testing.T) {
	const ttl = 5 * time.Second
	rng := rand.New(rand.NewSource(20261007))

	for trial := 0; trial < 300; trial++ {
		limit := int64(1 + rng.Intn(5))
		trialRNG := rand.New(rand.NewSource(int64(trial*7919 + 17)))
		ops := generateScript(trialRNG, 120, ttl.Nanoseconds())

		clk := NewFakeClock(0)
		mgr := NewManager(ttl, clk)
		if err := mgr.EnsureScope(testScope, Limit(limit), 0); err != nil {
			t.Fatal(err)
		}
		oracle := NewNaiveOracle(limit, ttl, 0)

		got := runScriptOnManager(t, mgr, clk, ops)
		want := runScriptOnOracle(oracle, ops)

		for i := range ops {
			if ops[i].kind == opAdvance {
				continue
			}
			if got[i].accepted != want[i].accepted ||
				got[i].reason != want[i].reason ||
				got[i].outcome != want[i].outcome ||
				!eqStringSet(got[i].swept, want[i].swept) {
				t.Fatalf("trial %d op %d (%+v): got %+v want %+v",
					trial, i, ops[i], got[i], want[i])
			}
			// 收割场景再次显式校验：两个模型的 swept 集合必须相同（顺序无关）。
			if ops[i].kind == opSweep && !eqStringSet(got[i].swept, want[i].swept) {
				t.Fatalf("trial %d sweep %d: got %v want %v",
					trial, i, got[i].swept, want[i].swept)
			}
		}

		mc, _ := mgr.Committed(testScope)
		// 比较存活集合前，在两侧同一最终时刻各做一次显式收割，
		// 排除"惰性回收触发时机"这一观测口径差异。
		finalNow := clk.NowNanos()
		mgr.Sweep()
		oracle.Sweep(finalNow)
		if mc != oracle.committed {
			t.Fatalf("trial %d final committed: manager=%d oracle=%d",
				trial, mc, oracle.committed)
		}
		if mc > limit {
			t.Fatalf("trial %d over limit: %d > %d", trial, mc, limit)
		}
		if st := mgr.Stats(); st.Inflight != len(oracle.active) {
			t.Fatalf("trial %d inflight mismatch: %d vs %d",
				trial, st.Inflight, len(oracle.active))
		}
	}
}

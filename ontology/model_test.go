package ontology

import (
	"fmt"
	"math/rand"
	"sync"
	"testing"
	"time"
)

// naiveModel 是独立实现的"朴素全局串行名额分配模型"：
// 单把锁、每次操作都线性扫描所有活跃请求与全部已确认关联。
// 它刻意不共享 Guard 的任何代码或数据结构，作为对照预言机（oracle）。
type naiveModel struct {
	mu       sync.Mutex
	capacity int
	version  int64
	links    map[Link]struct{}
	active   map[string]naiveActive
	final    map[string]Outcome
}

type naiveActive struct {
	id, source, target string
	ver                int64
	deadline           time.Time
}

func newNaiveModel(capacity int) *naiveModel {
	return &naiveModel{
		capacity: capacity,
		links:    map[Link]struct{}{},
		active:   map[string]naiveActive{},
		final:    map[string]Outcome{},
	}
}

type naiveDecision struct {
	admitted bool
	reason   RejectReason
}

func (m *naiveModel) reap(now time.Time) {
	for id, a := range m.active {
		if !a.deadline.After(now) {
			delete(m.active, id)
			m.final[id] = OutcomeExpired
		}
	}
}

func (m *naiveModel) begin(id, src, tgt string, observed int64, now time.Time, ttl time.Duration) naiveDecision {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.reap(now)

	if observed != m.version {
		return naiveDecision{reason: RejectBaselineConflict}
	}
	link := Link{SourceID: src, TargetID: tgt}
	if _, ok := m.links[link]; ok {
		return naiveDecision{reason: RejectDuplicate}
	}
	if len(m.links) >= m.capacity {
		return naiveDecision{reason: RejectConfirmedFull}
	}
	if len(m.links)+len(m.active) >= m.capacity {
		return naiveDecision{reason: RejectInFlight}
	}
	if _, busy := m.active[id]; busy {
		return naiveDecision{reason: RejectInFlight}
	}
	m.active[id] = naiveActive{id: id, source: src, target: tgt, ver: observed, deadline: now.Add(ttl)}
	m.final[id] = OutcomePending
	return naiveDecision{admitted: true}
}

func (m *naiveModel) heartbeat(id string, now time.Time, ttl time.Duration) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	a, ok := m.active[id]
	if !ok {
		return false
	}
	a.deadline = now.Add(ttl)
	m.active[id] = a
	return true
}

func (m *naiveModel) commit(id string) (ok bool, version int64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	a, ok := m.active[id]
	if !ok {
		return false, m.version
	}
	delete(m.active, id)
	link := Link{SourceID: a.source, TargetID: a.target}
	if _, exists := m.links[link]; exists {
		m.final[id] = OutcomeRolledBack
		return false, m.version
	}
	m.links[link] = struct{}{}
	m.version++
	m.final[id] = OutcomeCommitted
	return true, m.version
}

func (m *naiveModel) rollback(id string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.active[id]; !ok {
		return false
	}
	delete(m.active, id)
	m.final[id] = OutcomeRolledBack
	return true
}

func (m *naiveModel) state(now time.Time) (confirmed, inFlight int, version int64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.reap(now)
	return len(m.links), len(m.active), m.version
}

// opKind 是随机序列中的操作类别。
type opKind int

const (
	opBegin opKind = iota
	opCommit
	opRollback
	opHeartbeat
	opAdvance
)

type op struct {
	kind opKind
	id   string
	src  string
	ver  int64
	dt   time.Duration
}

// TestRandomizedEquivalence 随机并发序列对照朴素模型，并校验决策日志可重放。
func TestRandomizedEquivalence(t *testing.T) {
	const ttl = 10 * time.Second
	const seeds = 200
	const opsPerSeed = 120

	for seed := int64(0); seed < seeds; seed++ {
		rng := rand.New(rand.NewSource(seed))
		clk := newFakeClock()
		g := NewGuard(Config{Clock: clk, LeaseTTL: ttl})
		cap := 1 + rng.Intn(4)
		if err := g.EnsureScope(testScope, cap); err != nil {
			t.Fatal(err)
		}
		oracle := newNaiveModel(cap)

		var tracked []string
		for step := 0; step < opsPerSeed; step++ {
			o := randomOp(rng, step, &tracked)
			switch o.kind {
			case opBegin:
				if o.ver == -1 {
					snap, _ := g.Snapshot(testScope)
					o.ver = snap.Version
				}
				now := clk.Now()
				got := g.Begin(beginReq(o.id, o.src, "dept-1", o.ver))
				want := oracle.begin(o.id, o.src, "dept-1", o.ver, now, ttl)
				if got.Admitted != want.admitted || (!got.Admitted && got.Reason != want.reason) {
					t.Fatalf("seed=%d step=%d BEGIN %s ver=%d: got(admit=%v reason=%v) want(admit=%v reason=%v)",
						seed, step, o.id, o.ver, got.Admitted, got.Reason, want.admitted, want.reason)
				}
				if got.Admitted {
					tracked = append(tracked, o.id)
				}
			case opCommit:
				gotCR := g.Commit(o.id)
				wantOK, wantVer := oracle.commit(o.id)
				if gotCR.OK != wantOK {
					t.Fatalf("seed=%d COMMIT %s: got=%v want=%v", seed, o.id, gotCR.OK, wantOK)
				}
				if gotCR.Version != wantVer {
					t.Fatalf("seed=%d COMMIT %s version: got=%d want=%d", seed, o.id, gotCR.Version, wantVer)
				}
			case opRollback:
				got := g.Rollback(o.id)
				want := oracle.rollback(o.id)
				if got != want {
					t.Fatalf("seed=%d ROLLBACK %s: got=%v want=%v", seed, o.id, got, want)
				}
			case opHeartbeat:
				got := g.Heartbeat(o.id)
				want := oracle.heartbeat(o.id, clk.Now(), ttl)
				if got != want {
					t.Fatalf("seed=%d HEARTBEAT %s: got=%v want=%v", seed, o.id, got, want)
				}
			case opAdvance:
				clk.Advance(o.dt)
				g.Reap()
			}

			// 每一步后两模型的可观测状态必须一致。
			gSnap, _ := g.Snapshot(testScope)
			wConf, wFlight, wVer := oracle.state(clk.Now())
			if gSnap.Confirmed != wConf || gSnap.InFlight != wFlight || gSnap.Version != wVer {
				t.Fatalf("seed=%d step=%d state mismatch: guard(c=%d f=%d v=%d) oracle(c=%d f=%d v=%d)",
					seed, step, gSnap.Confirmed, gSnap.InFlight, gSnap.Version, wConf, wFlight, wVer)
			}
			if gSnap.Confirmed+gSnap.InFlight > cap {
				t.Fatalf("seed=%d step=%d over capacity: %d+%d > %d",
					seed, step, gSnap.Confirmed, gSnap.InFlight, cap)
			}
		}

		// 最终裁定逐请求一致。
		for _, id := range tracked {
			goc, gknown := g.Outcome(id)
			oracle.mu.Lock()
			woc, wknown := oracle.final[id]
			oracle.mu.Unlock()
			if gknown != wknown || goc != woc {
				t.Fatalf("seed=%d outcome %s: guard=(%v,%v) oracle=(%v,%v)",
					seed, id, goc, gknown, woc, wknown)
			}
		}

		// 决策日志必须可重放：按日志重建的状态等于最终快照。
		replay := replayJournal(t, g.Journal().Events(), cap)
		finalSnap, _ := g.Snapshot(testScope)
		if replay.confirmed != finalSnap.Confirmed || replay.inFlight != finalSnap.InFlight || replay.version != finalSnap.Version {
			t.Fatalf("seed=%d replay mismatch: replay=%+v final=%+v", seed, replay, finalSnap)
		}
	}
}

// randomOp 生成一个操作；observed 版本以高概率取真实当前版本以覆盖成功路径。
func randomOp(rng *rand.Rand, step int, tracked *[]string) op {
	k := rng.Intn(10)
	switch {
	case k < 5:
		// 70% 概率带正确基线（取 -1 占位，调用处无法知道当前版本，
		// 这里直接用 0..4 随机，模型与守卫用同一值，对照重点是判定一致性）。
		ver := int64(0)
		if rng.Intn(10) < 7 {
			ver = -1 // 哨兵：由执行器替换为守卫当前版本
		} else {
			ver = int64(rng.Intn(8))
		}
		return op{kind: opBegin, id: fmt.Sprintf("s%d-r%d", step, rng.Intn(6)),
			src: fmt.Sprintf("emp-%d", rng.Intn(8)), ver: ver}
	case k < 7 && len(*tracked) > 0:
		id := (*tracked)[rng.Intn(len(*tracked))]
		if k == 5 {
			return op{kind: opCommit, id: id}
		}
		return op{kind: opRollback, id: id}
	case k == 7 && len(*tracked) > 0:
		return op{kind: opHeartbeat, id: (*tracked)[rng.Intn(len(*tracked))]}
	default:
		return op{kind: opAdvance, dt: time.Duration(1+rng.Intn(12)) * time.Second}
	}
}

// replayState 是从决策日志重放得到的状态。
type replayState struct {
	confirmed int
	inFlight  int
	version   int64
}

// replayJournal 用一个全新的朴素解释器重放全部事件，验证日志足以重建状态。
func replayJournal(t *testing.T, events []Event, cap int) replayState {
	t.Helper()
	links := map[Link]bool{}
	pending := map[string]bool{}
	var version int64

	// 同一 (source,target) 关联键用于去重，与提交顺序无关。
	linkOf := map[string]Link{}

	for _, e := range events {
		switch e.Kind {
		case EventAdmit:
			if links[Link{SourceID: e.Source, TargetID: e.Target}] || pending[e.RequestID] {
				t.Fatalf("replay: ADMIT for existing link/request seq=%d", e.Seq)
			}
			pending[e.RequestID] = true
			linkOf[e.RequestID] = Link{SourceID: e.Source, TargetID: e.Target}
		case EventReject:
			if pending[e.RequestID] {
				t.Fatalf("replay: REJECT marked pending seq=%d", e.Seq)
			}
		case EventCommit:
			if !pending[e.RequestID] {
				t.Fatalf("replay: COMMIT without pending seq=%d", e.Seq)
			}
			delete(pending, e.RequestID)
			links[linkOf[e.RequestID]] = true
			version++
		case EventRollback, EventExpire:
			delete(pending, e.RequestID)
		}
		if len(links)+len(pending) > cap {
			t.Fatalf("replay: over capacity at seq=%d: %d+%d > %d",
				e.Seq, len(links), len(pending), cap)
		}
		if e.Confirmed != len(links) || e.InFlight != len(pending) ||
			(e.Kind == EventCommit && e.Baseline != version) {
			t.Fatalf("replay: event counters inconsistent at seq=%d kind=%s "+
				"event(c=%d f=%d b=%d) replay(c=%d f=%d v=%d)",
				e.Seq, e.Kind, e.Confirmed, e.InFlight, e.Baseline,
				len(links), len(pending), version)
		}
	}
	return replayState{confirmed: len(links), inFlight: len(pending), version: version}
}

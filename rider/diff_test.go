package rider

import (
	"fmt"
	"math/rand"
	"sync"
	"sync/atomic"
	"testing"
)

// ---- 随机操作序列：生产实现 vs 朴素全量重算模型 ----

type fuzzOp struct {
	kind   int // 0 rider 1 event 2 appeal 3 rule 4 query
	now    int64
	rider  string
	t      int64
	typ    EventType
	root   string
	event  int64
	appeal int64
	upheld bool
	period int
}

func fuzzConfig() Config {
	return Config{
		PeriodLength:         8,
		PeriodOrigin:         3,
		EventScores:          map[EventType]int{EventTypeLateDelivery: 1, EventTypeCustomerComplaint: 2, EventTypeRejectOrder: 3, EventTypeFaultCancel: 4},
		Thresholds:           []int{3, 7, 12},
		AppealWindow:         15,
		ClusterSpan:          4,
		MaxLevelDrop:         1,
		CompensationPerLevel: 100,
	}
}

var fuzzTypes = []EventType{EventTypeLateDelivery, EventTypeCustomerComplaint, EventTypeRejectOrder, EventTypeFaultCancel}

func generateOps(rng *rand.Rand, n int) []fuzzOp {
	var ops []fuzzOp
	riders := []string{"a", "b", "c"}
	roots := []string{"", "", "shop1", "shop2", "zone9"}
	var now int64
	type pend struct {
		rider string
		id    int64
	}
	var pending []pend
	known := map[string][]int64{}
	for i := 0; i < n; i++ {
		// 偶发时钟回退（两侧都必须拒绝且无副作用）。
		if rng.Intn(20) == 0 {
			ops = append(ops, fuzzOp{kind: 1, now: now - 1, rider: "a", t: now, typ: fuzzTypes[0], root: ""})
			continue
		}
		now += int64(rng.Intn(3))
		op := fuzzOp{now: now}
		r := riders[rng.Intn(len(riders))]
		op.rider = r
		switch rng.Intn(10) {
		case 0, 1:
			op.kind = 0
		case 2, 3, 4, 5:
			op.kind = 1
			op.t = now - int64(rng.Intn(18))
			op.typ = fuzzTypes[rng.Intn(len(fuzzTypes))]
			op.root = roots[rng.Intn(len(roots))]
		case 6, 7:
			if evs := known[r]; len(evs) > 0 {
				op.kind = 2
				op.event = evs[rng.Intn(len(evs))]
			} else {
				op.kind = 1
				op.t = now
				op.typ = fuzzTypes[0]
			}
		case 8:
			if len(pending) > 0 {
				pi := rng.Intn(len(pending))
				op.kind = 3
				op.appeal = pending[pi].id
				op.rider = pending[pi].rider
				op.upheld = rng.Intn(2) == 0
				pending = append(pending[:pi], pending[pi+1:]...)
			} else {
				op.kind = 4
				op.period = int((now - 3) / 8)
			}
		default:
			op.kind = 4
			op.period = int((now-3)/8) - rng.Intn(3)
		}
		ops = append(ops, op)
	}
	return ops
}

// applyNaive 在朴素模型上执行，并回填新登记事件/申诉以便后续步骤引用。
func applyNaive(t *testing.T, m *naiveModel, op fuzzOp, step int) nResult {
	var r nResult
	switch op.kind {
	case 0:
		r = m.registerRider(op.now, op.rider)
	case 1:
		r = m.registerEvent(op.now, op.rider, op.t, op.typ, op.root)
	case 2:
		r = m.submitAppeal(op.now, op.rider, op.event)
	case 3:
		r = m.ruleAppeal(op.now, op.rider, op.appeal, op.upheld)
	case 4:
		r = m.query(op.now, op.rider, op.period)
	}
	t.Logf("[naive] step=%d op=%+v => err=%v id=%d comp=%v q=%+v", step, op, r.err, r.id, r.comp, r.q)
	return r
}

func TestDifferential(t *testing.T) {
	for seed := int64(1); seed <= 40; seed++ {
		t.Run(fmt.Sprintf("seed=%d", seed), func(t *testing.T) {
			rng := rand.New(rand.NewSource(seed))
			cfg := fuzzConfig()
			sys, err := New(cfg)
			if err != nil {
				t.Fatal(err)
			}
			nm := newNaive(cfg)
			ops := generateOps(rng, 300)
			known := map[string][]int64{}
			pending := map[int64]string{}

			for step, op := range ops {
				nr := applyNaive(t, nm, op, step)
				var prErr error
				var prID int64
				var prComp *Compensation
				var prQ QueryResult
				switch op.kind {
				case 0:
					prErr = sys.RegisterRider(op.now, op.rider)
				case 1:
					prID, prErr = sys.RegisterEvent(op.now, op.rider, op.t, op.typ, op.root)
				case 2:
					prID, prErr = sys.SubmitAppeal(op.now, op.rider, op.event)
				case 3:
					prComp, prErr = sys.RuleAppeal(op.now, op.rider, op.appeal, op.upheld)
				case 4:
					prQ, prErr = sys.Query(op.now, op.rider, op.period)
				}
				t.Logf("[prod ] step=%d op=%+v => err=%v id=%d comp=%v q=%+v", step, op, CodeOf(prErr), prID, prComp, prQ)
				if CodeOf(prErr) != nr.err {
					t.Fatalf("step %d error mismatch: prod=%v naive=%v op=%+v", step, CodeOf(prErr), nr.err, op)
				}
				if prErr == nil {
					if op.kind == 1 || op.kind == 2 {
						if prID != nr.id {
							t.Fatalf("step %d id mismatch prod=%d naive=%d", step, prID, nr.id)
						}
						if op.kind == 1 {
							known[op.rider] = append(known[op.rider], prID)
						}
						if op.kind == 2 {
							pending[prID] = op.rider
						}
					}
					if op.kind == 3 {
						_ = pending
						if (prComp == nil) != (nr.comp == nil) {
							t.Fatalf("step %d comp presence mismatch prod=%v naive=%v", step, prComp, nr.comp)
						}
						if prComp != nil && prComp.Amount != nr.comp.Amount {
							t.Fatalf("step %d comp amount mismatch prod=%d naive=%d", step, prComp.Amount, nr.comp.Amount)
						}
					}
					if op.kind == 4 && prQ != nr.q {
						t.Fatalf("step %d query mismatch prod=%+v naive=%+v", step, prQ, nr.q)
					}
				}
			}
			if got, want := len(sys.CompensationLog()), len(nm.comps); got != want {
				t.Fatalf("compensation log length %d != %d", got, want)
			}
			for i := range nm.comps {
				if sys.CompensationLog()[i] != nm.comps[i] {
					t.Fatalf("comp %d mismatch prod=%+v naive=%+v", i, sys.CompensationLog()[i], nm.comps[i])
				}
			}
			// 不变量：每周期 liveScore 恒等于 counting 之和。
			for _, rs := range sys.riders {
				for p, ps := range rs.periods {
					sum := 0
					for _, sc := range ps.counting {
						sum += sc
					}
					if sum != ps.liveScore {
						t.Fatalf("invariant broken rider=%s period=%d live=%d sum=%d", rs.id, p, ps.liveScore, sum)
					}
				}
			}
		})
	}
}

// ---- 并发：多 goroutine 以全序时间戳登记，成簇结果必须自洽且可复现 ----

func TestConcurrentRegistration(t *testing.T) {
	cfg := testConfig()
	sys, _ := New(cfg)
	if err := sys.RegisterRider(0, "a"); err != nil {
		t.Fatal(err)
	}
	var clock int64
	const workers = 8
	const each = 60
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < each; i++ {
				_ = atomic.AddInt64(&clock, 1)
				// 事件时刻取 [-5,-1]，落在周期 -1（右端点 0）；接受时刻不晚于 0，故尚未结算。
				et := int64(-(i%5 + 1))
				if _, err := sys.RegisterEventAtomicClock("a", et, fuzzTypes[i%4], "shop1"); err != nil {
					t.Errorf("register: %v", err)
					return
				}
			}
		}()
	}
	wg.Wait()
	// 事件时刻 -5..-1 落入周期 -1（[-10,0)）；在 now=0 查询，右端点恰为 0，左闭右开故已结算。
	q, err := sys.Query(0, "a", -1)
	if err != nil {
		t.Fatal(err)
	}
	// 全部 root=shop1，时刻只在 -5..-1：排序后相邻差恒为 1 <= span=5，
	// 故全部事件同属一簇，唯一计扣分者是全局最早者（t=-5 且 seq 最小）。
	var evs []*Event
	for _, e := range sys.events {
		if e.RiderID == "a" && !e.Revoked {
			evs = append(evs, e)
		}
	}
	var first *Event
	for _, e := range evs {
		if first == nil || e.Time < first.Time || (e.Time == first.Time && e.Seq < first.Seq) {
			first = e
		}
	}
	want := cfg.EventScores[first.Type]
	if q.Score != want {
		t.Fatalf("concurrent cluster score mismatch prod=%d want=%d", q.Score, want)
	}
}

package ledger

import (
	"errors"
	"fmt"
	"math/rand"
	"sort"
	"testing"

	"ontology/plan"
)

// 朴素模拟：独立于 ledger 实现，逐查询重算 used/resv 与各窗口占用。
type simQuery struct {
	analyst    string
	datasets   []string
	cost       int64
	window     int64
	state      int // 0 reserved 1 running 2 committed 3 cancelled
	actual     int64
	cancelFull bool // Running 状态取消时按 cost 全额计入
}

type sim struct {
	wn      int64
	db      map[string]int64
	ab      map[string]int64
	q       map[string]*simQuery
	clock   int64
	dsUsed  map[string]int64
	dsResv  map[string]int64
	winUsed map[[2]any]int64
}

func newSim(wn int64) *sim {
	return &sim{
		wn:      wn,
		db:      map[string]int64{},
		ab:      map[string]int64{},
		q:       map[string]*simQuery{},
		dsUsed:  map[string]int64{},
		dsResv:  map[string]int64{},
		winUsed: map[[2]any]int64{},
	}
}

func (s *sim) winOf(now int64) int64 { return now / s.wn }
func (s *sim) dsRemain(d string) int64 {
	return s.db[d] - s.dsUsed[d] - s.dsResv[d]
}
func (s *sim) aRemain(a string, w int64) int64 {
	return s.ab[a] - s.winUsed[[2]any{a, w}]
}

func classifyError(err error) string {
	switch {
	case err == nil:
		return "ok"
	case errors.Is(err, ErrInvalidArgument):
		return "invalid"
	case errors.Is(err, ErrClockRewind):
		return "rewind"
	case errors.Is(err, ErrDuplicateQID):
		return "dup"
	case errors.Is(err, ErrUnknownDataset):
		return "no-dataset"
	case errors.Is(err, ErrUnknownAnalyst):
		return "no-analyst"
	case errors.Is(err, ErrNotDisjoint):
		return "not-disjoint"
	case errors.Is(err, ErrDatasetExhausted):
		return "ds-exhausted"
	case errors.Is(err, ErrAnalystExhausted):
		return "a-exhausted"
	case errors.Is(err, ErrUnknownQuery):
		return "no-query"
	case errors.Is(err, ErrWrongState):
		return "wrong-state"
	case errors.Is(err, ErrOverspend):
		return "overspend"
	default:
		return "unknown:" + err.Error()
	}
}

func simPlanInvalidDatasets(datasets []string) bool {
	if len(datasets) < 1 || len(datasets) > 4 {
		return true
	}
	seen := map[string]bool{}
	for _, d := range datasets {
		if seen[d] {
			return true
		}
		seen[d] = true
	}
	return false
}

func (s *sim) simReserve(qid, analyst string, datasets []string, cost int64, planErr error, now int64) string {
	if qid == "" || now < 0 || now > 1_000_000_000_000 || simPlanInvalidDatasets(datasets) {
		return "invalid"
	}
	var notDisjoint bool
	if planErr != nil {
		if errors.Is(planErr, plan.ErrNotDisjoint) {
			notDisjoint = true
		} else {
			return "invalid"
		}
	}
	if now < s.clock {
		return "rewind"
	}
	if _, ok := s.q[qid]; ok {
		return "dup"
	}
	names := append([]string(nil), datasets...)
	sort.Strings(names)
	for _, d := range names {
		if _, ok := s.db[d]; !ok {
			return "no-dataset"
		}
	}
	if _, ok := s.ab[analyst]; !ok {
		return "no-analyst"
	}
	if notDisjoint {
		return "not-disjoint"
	}
	for _, d := range names {
		if s.dsRemain(d) < cost {
			return "ds-exhausted"
		}
	}
	w := s.winOf(now)
	if s.aRemain(analyst, w) < cost {
		return "a-exhausted"
	}
	for _, d := range names {
		s.dsResv[d] += cost
	}
	s.winUsed[[2]any{analyst, w}] += cost
	s.q[qid] = &simQuery{analyst: analyst, datasets: names, cost: cost, window: w}
	s.clock = now
	return "ok"
}

func (s *sim) simStart(qid string, now int64) string {
	if qid == "" || now < 0 || now > 1_000_000_000_000 {
		return "invalid"
	}
	if now < s.clock {
		return "rewind"
	}
	q, ok := s.q[qid]
	if !ok {
		return "no-query"
	}
	if q.state != 0 {
		return "wrong-state"
	}
	q.state = 1
	s.clock = now
	return "ok"
}

func (s *sim) simCommit(qid string, actual, now int64) string {
	if qid == "" || now < 0 || actual < 0 {
		return "invalid"
	}
	if now < s.clock {
		return "rewind"
	}
	q, ok := s.q[qid]
	if !ok {
		return "no-query"
	}
	if q.state != 1 {
		return "wrong-state"
	}
	if actual > q.cost {
		return "overspend"
	}
	for _, d := range q.datasets {
		s.dsResv[d] -= q.cost
		s.dsUsed[d] += actual
	}
	s.winUsed[[2]any{q.analyst, q.window}] += actual - q.cost
	q.actual = actual
	q.state = 2
	s.clock = now
	return "ok"
}

func (s *sim) simCancel(qid string, now int64) string {
	if qid == "" || now < 0 {
		return "invalid"
	}
	if now < s.clock {
		return "rewind"
	}
	q, ok := s.q[qid]
	if !ok {
		return "no-query"
	}
	switch q.state {
	case 0:
		for _, d := range q.datasets {
			s.dsResv[d] -= q.cost
		}
		s.winUsed[[2]any{q.analyst, q.window}] -= q.cost
		q.state = 3
		s.clock = now
		return "ok"
	case 1:
		for _, d := range q.datasets {
			s.dsResv[d] -= q.cost
			s.dsUsed[d] += q.cost
		}
		q.state = 3
		q.cancelFull = true
		s.clock = now
		return "ok"
	default:
		return "wrong-state"
	}
}

// genTree 生成深度不超过 8、节点不超过 256 的随机计划。
func genTree(rng *rand.Rand, depth int, budget *int) plan.Node {
	if depth >= 8 || *budget <= 1 || (depth > 1 && rng.Intn(3) == 0) {
		*budget--
		n := 1 + rng.Intn(4)
		parts := rng.Perm(8)[:n]
		return &plan.Leaf{Cost: 1 + rng.Intn(50), Parts: parts}
	}
	switch rng.Intn(4) {
	case 0, 1:
		n := 1 + rng.Intn(3)
		cs := make([]plan.Node, 0, n)
		for i := 0; i < n && *budget > 1; i++ {
			cs = append(cs, genTree(rng, depth+1, budget))
		}
		*budget--
		if rng.Intn(2) == 0 {
			return &plan.Seq{Children: cs}
		}
		return &plan.Par{Children: cs}
	default:
		den := 1 + rng.Intn(5)
		num := 1 + rng.Intn(den)
		*budget--
		return &plan.Sample{Num: num, Den: den, Child: genTree(rng, depth+1, budget)}
	}
}

// maybeBreakPlan 小概率构造结构非法计划或 Par 分区相交计划。
// 返回的 bool 为 true 表示结构非法（应判 invalid）。
func maybeBreakPlan(rng *rand.Rand, valid plan.Node) (plan.Node, bool) {
	switch rng.Intn(12) {
	case 0:
		return nil, true
	case 1:
		return &plan.Leaf{Cost: 0, Parts: []int{1}}, true
	case 2:
		return &plan.Seq{}, true
	case 3:
		return &plan.Sample{Num: 2, Den: 1, Child: &plan.Leaf{Cost: 1, Parts: []int{1}}}, true
	case 4:
		return &plan.Par{Children: []plan.Node{
			&plan.Leaf{Cost: 1, Parts: []int{1, 2}},
			&plan.Leaf{Cost: 1, Parts: []int{2, 3}},
		}}, false
	default:
		return valid, false
	}
}

func TestRandomAgainstNaiveSimulation(t *testing.T) {
	const groups = 1500
	const ops = 40
	for g := 0; g < groups; g++ {
		rng := rand.New(rand.NewSource(int64(g) + 1))
		t.Run(fmt.Sprintf("g%d", g), func(t *testing.T) {
			wn := int64(1 + rng.Intn(3))
			l, err := New(wn)
			if err != nil {
				t.Fatal(err)
			}
			s := newSim(wn)

			datasets := []string{"d0", "d1", "d2", "d3"}
			analysts := []string{"a0", "a1"}
			for _, d := range datasets {
				bd := int64(1 + rng.Intn(300))
				if err := l.AddDataset(d, bd); err != nil {
					t.Fatal(err)
				}
				s.db[d] = bd
			}
			for _, a := range analysts {
				ba := int64(1 + rng.Intn(120))
				if err := l.AddAnalyst(a, ba); err != nil {
					t.Fatal(err)
				}
				s.ab[a] = ba
			}

			for step := 0; step < ops; step++ {
				now := s.clock + int64(rng.Intn(3))
				op := rng.Intn(10)
				qid := fmt.Sprintf("q%d", rng.Intn(ops))

				if op < 6 {
					a := analysts[rng.Intn(len(analysts))]
					nd := 1 + rng.Intn(3)
					pool := append([]string(nil), datasets...)
					rng.Shuffle(len(pool), func(i, j int) { pool[i], pool[j] = pool[j], pool[i] })
					ds := append([]string(nil), pool[:nd]...)
					budget := 256
					p := genTree(rng, 1, &budget)
					p, _ = maybeBreakPlan(rng, p)
					ev, planErr := plan.Eval(p)
					if rng.Intn(8) == 0 {
						ds = []string{"ghost"}
					}
					if rng.Intn(10) == 0 {
						a = "ghost-analyst"
					}
					got := classifyError(l.Reserve(qid, a, ds, p, now))
					want := s.simReserve(qid, a, ds, ev.Cost, planErr, now)
					t.Logf("step %2d Reserve qid=%s a=%s ds=%v now=%d cost=%d planErr=%v => %s (naive %s)",
						step, qid, a, ds, now, ev.Cost, planErr, got, want)
					if got != want {
						t.Fatalf("step %d Reserve: got=%s want=%s", step, got, want)
					}
					continue
				}

				var got, want string
				switch op {
				case 6, 7:
					got = classifyError(l.Start(qid, now))
					want = s.simStart(qid, now)
					t.Logf("step %2d Start  qid=%s now=%d => %s (naive %s)", step, qid, now, got, want)
				case 8:
					var actual int64
					if q, ok := s.q[qid]; ok {
						if rng.Intn(2) == 0 {
							actual = rng.Int63n(q.cost + 1)
						} else {
							actual = q.cost + 1 + int64(rng.Intn(5))
						}
					} else {
						actual = int64(rng.Intn(10))
					}
					got = classifyError(l.Commit(qid, actual, now))
					want = s.simCommit(qid, actual, now)
					t.Logf("step %2d Commit qid=%s actual=%d now=%d => %s (naive %s)", step, qid, actual, now, got, want)
				default:
					got = classifyError(l.Cancel(qid, now))
					want = s.simCancel(qid, now)
					t.Logf("step %2d Cancel qid=%s now=%d => %s (naive %s)", step, qid, now, got, want)
				}
				if got != want {
					t.Fatalf("step %d op=%d: got=%s want=%s", step, op, got, want)
				}
			}

			// 终态对账：数据集 used/resv/remaining 与朴素模拟逐项一致。
			for _, d := range datasets {
				got, err := l.Remaining(d)
				if err != nil {
					t.Fatal(err)
				}
				want := s.dsRemain(d)
				if got != want {
					t.Fatalf("final Remaining(%s)=%d want %d (used=%d resv=%d)",
						d, got, want, s.dsUsed[d], s.dsResv[d])
				}
			}
			// used 等于已结束查询记账额之和（commit 计 actual；Running 取消计 cost）。
			endedUsed := map[string]int64{}
			for _, q := range s.q {
				switch q.state {
				case 2:
					for _, d := range q.datasets {
						endedUsed[d] += q.actual
					}
				case 3:
					if q.cancelFull {
						for _, d := range q.datasets {
							endedUsed[d] += q.cost
						}
					}
				}
			}
			for _, d := range datasets {
				if endedUsed[d] != s.dsUsed[d] {
					t.Fatalf("invariant used(%s)=%d but ended queries sum %d", d, s.dsUsed[d], endedUsed[d])
				}
			}
			// 分析师各窗口占用逐项对账（取查询中出现过的所有窗口；
			// 旧窗口占用无法通过只读 API 观察，故包内直接读账目）。
			windows := map[int64]bool{}
			for _, q := range s.q {
				windows[q.window] = true
			}
			l.mu.Lock()
			for _, a := range analysts {
				for w := range windows {
					got := l.analysts[a].WindowRemaining(w)
					want := s.aRemain(a, w)
					if got != want {
						t.Fatalf("final AnalystRemaining(%s, window %d)=%d want %d", a, w, got, want)
					}
				}
			}
			l.mu.Unlock()
		})
	}
}

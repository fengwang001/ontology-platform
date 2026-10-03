package ontology

// 朴素参考模拟：按题目规则逐步直接写成，刻意不与生产代码共享任何实现，
// 用于 2000 组随机操作序列的差分对照。

import (
	"errors"
	"fmt"
	"math/big"
	"math/rand"
	"sort"
	"testing"
)

type naiveArm struct {
	id     int64
	sT     int64
	cT     int64
	sR     int64
	active bool
}

type naiveFunnel struct {
	arms     []*naiveArm
	byID     map[int64]*naiveArm
	active   map[*naiveArm]struct{}
	events   []ElimEvent
	r        int
	b, g     int64
	finished bool
	winner   int64
	nexts    int64
}

func naiveNew(n int, b, g int64) *naiveFunnel {
	f := &naiveFunnel{
		byID: map[int64]*naiveArm{}, active: map[*naiveArm]struct{}{},
		r: 1, b: b, g: g,
	}
	for id := 0; id < n; id++ {
		a := &naiveArm{id: int64(id), active: true}
		f.arms = append(f.arms, a)
		f.byID[a.id] = a
		f.active[a] = struct{}{}
	}
	return f
}

func naiveQuota(b int64, r int) int64 {
	k := r - 1
	if k > 20 {
		k = 20
	}
	return b << k
}

func (f *naiveFunnel) eliminate(a *naiveArm, reason ElimReason) {
	a.active = false
	delete(f.active, a)
	f.events = append(f.events, ElimEvent{Round: f.r, Arm: a.id, Reason: reason})
	if len(f.active) == 1 {
		for w := range f.active {
			f.winner = w.id
		}
		f.finished = true
	}
}

func (f *naiveFunnel) Next() (int64, error) {
	if f.finished {
		return 0, ErrFinished
	}
	q := naiveQuota(f.b, f.r)
	var chosen *naiveArm
	list := make([]*naiveArm, 0, len(f.active))
	for a := range f.active {
		list = append(list, a)
	}
	sort.Slice(list, func(i, j int) bool {
		if list[i].sR != list[j].sR {
			return list[i].sR < list[j].sR
		}
		return list[i].id < list[j].id
	})
	for _, a := range list {
		if a.sR < q {
			chosen = a
			break
		}
	}
	chosen.sT++
	chosen.sR++
	f.nexts++
	id := chosen.id

	if chosen.cT == 0 && chosen.sT >= f.g && len(f.active) >= 2 {
		f.eliminate(chosen, ReasonGuardrail)
		if f.finished {
			return id, nil
		}
	}

	allFull := true
	for a := range f.active {
		if a.sR != q {
			allFull = false
			break
		}
	}
	if allFull {
		live := make([]*naiveArm, 0, len(f.active))
		for a := range f.active {
			live = append(live, a)
		}
		sort.Slice(live, func(i, j int) bool {
			x, y := live[i], live[j]
			cmp := new(big.Int).Mul(big.NewInt(x.cT), big.NewInt(y.sT)).Cmp(
				new(big.Int).Mul(big.NewInt(y.cT), big.NewInt(x.sT)))
			if cmp != 0 {
				return cmp > 0
			}
			return x.id < y.id
		})
		keep := (len(live) + 1) / 2
		for _, a := range live[keep:] {
			f.eliminate(a, ReasonRound)
		}
		if !f.finished {
			f.r++
			for a := range f.active {
				a.sR = 0
			}
		}
	}
	return id, nil
}

func (f *naiveFunnel) Click(id int64) error {
	a, ok := f.byID[id]
	if !ok {
		return ErrUnknownArm
	}
	if a.cT >= a.sT {
		return ErrNoExposure
	}
	a.cT++
	return nil
}

func (f *naiveFunnel) Join(id int64) error {
	if id < 0 || id > 1000 {
		return ErrInvalidArgument
	}
	if f.finished {
		return ErrFinished
	}
	if _, ok := f.byID[id]; ok {
		return ErrArmExists
	}
	if len(f.arms) >= 64 {
		return ErrCapacity
	}
	a := &naiveArm{id: id, active: true}
	f.arms = append(f.arms, a)
	f.byID[id] = a
	f.active[a] = struct{}{}
	return nil
}

type opKind int

const (
	opNext opKind = iota
	opClick
	opJoin
)

type operation struct {
	kind opKind
	id   int64
}

// compareState 对照生产实现与朴素模拟的全部可观察状态。
func compareState(t *testing.T, f *Funnel, nf *naiveFunnel, log *[]string) {
	t.Helper()
	snap := f.Snapshot()
	if len(snap) != len(nf.arms) {
		t.Fatalf("arm count %d != naive %d", len(snap), len(nf.arms))
	}
	for i, a := range nf.arms {
		s := snap[i]
		if s.ID != a.id || s.Exposure != a.sT || s.Clicks != a.cT ||
			s.RoundExposure != a.sR || s.Active != a.active {
			t.Fatalf("arm %d mismatch: real=%+v naive={id:%d sT:%d cT:%d sR:%d active:%v}",
				i, s, a.id, a.sT, a.cT, a.sR, a.active)
		}
	}
	fe := f.EliminationEvents()
	if len(fe) != len(nf.events) {
		t.Fatalf("events len %d != %d", len(fe), len(nf.events))
	}
	for i := range fe {
		if fe[i] != nf.events[i] {
			t.Fatalf("event %d real=%v naive=%v", i, fe[i], nf.events[i])
		}
	}
	if f.Round() != nf.r {
		t.Fatalf("round %d != %d", f.Round(), nf.r)
	}
	if f.Finished() != nf.finished {
		t.Fatalf("finished %v != %v", f.Finished(), nf.finished)
	}
	if w, ok := f.Winner(); ok || nf.finished {
		if ok != nf.finished || w != nf.winner {
			t.Fatalf("winner real=(%d,%v) naive=(%d,%v)", w, ok, nf.winner, nf.finished)
		}
	}
	if f.TotalExposures() != nf.nexts {
		t.Fatalf("total exposures %d != %d", f.TotalExposures(), nf.nexts)
	}
	var sum int64
	for _, s := range snap {
		sum += s.Exposure
		if s.Clicks > s.Exposure {
			t.Fatalf("invariant cT>sT: %+v", s)
		}
	}
	if sum != nf.nexts {
		t.Fatalf("invariant sum(sT)=%d != nexts=%d", sum, nf.nexts)
	}
}

// TestRandomDifferentialAgainstNaive 生成 2000 组随机操作序列，逐步对照
// 生产实现与朴素模拟；日志打印输入、输出与判定依据。
func TestRandomDifferentialAgainstNaive(t *testing.T) {
	for seed := int64(1); seed <= 2000; seed++ {
		rng := rand.New(rand.NewSource(seed))
		n := 2 + rng.Intn(8) // 2..9，控制单组规模
		b := int64(1 + rng.Intn(4))
		g := int64(1 + rng.Intn(6))
		f, err := NewFunnel(n, b, g)
		if err != nil {
			t.Fatalf("seed %d: ctor %v", seed, err)
		}
		nf := naiveNew(n, b, g)

		var log []string
		log = append(log, fmt.Sprintf("seed=%d params n=%d b=%d G=%d", seed, n, b, g))
		steps := 200 + rng.Intn(400)
		nextID := int64(50) // Join 使用的新编号池
		registered := map[int64]bool{}
		for id := int64(0); id < int64(n); id++ {
			registered[id] = true
		}

		for step := 0; step < steps; step++ {
			var op operation
			switch rng.Intn(10) {
			case 0, 1, 2, 3, 4:
				op = operation{kind: opNext}
			case 5, 6, 7:
				// 在已登记编号与可能的未登记编号间混合点击。
				if rng.Intn(5) == 0 {
					op = operation{kind: opClick, id: int64(900 + rng.Intn(20))}
				} else {
					ids := make([]int64, 0, len(registered))
					for id := range registered {
						ids = append(ids, id)
					}
					op = operation{kind: opClick, id: ids[rng.Intn(len(ids))]}
				}
			case 8, 9:
				switch rng.Intn(10) {
				case 0:
					op = operation{kind: opJoin, id: -1} // 越界
				case 1:
					op = operation{kind: opJoin, id: 1001} // 越界
				case 2, 3:
					ids := make([]int64, 0, len(registered))
					for id := range registered {
						ids = append(ids, id)
					}
					op = operation{kind: opJoin, id: ids[rng.Intn(len(ids))]} // 已存在
				default:
					id := nextID
					nextID++
					op = operation{kind: opJoin, id: id}
				}
			}

			switch op.kind {
			case opNext:
				r1, e1 := f.Next()
				r2, e2 := nf.Next()
				if (r1 != r2) || !errors.Is(e1, e2) {
					t.Fatalf("seed %d step %d Next real=(%d,%v) naive=(%d,%v)\n%s",
						seed, step, r1, e1, r2, e2, joinLog(log))
				}
				reason := "ok"
				if e1 != nil {
					reason = "rejected: " + e1.Error()
				}
				log = append(log, fmt.Sprintf("step %d Next() -> %d [%s]", step, r1, reason))
			case opClick:
				e1 := f.Click(op.id)
				e2 := nf.Click(op.id)
				if !errors.Is(e1, e2) {
					t.Fatalf("seed %d step %d Click(%d) real=%v naive=%v\n%s",
						seed, step, op.id, e1, e2, joinLog(log))
				}
				reason := "accepted (cT incremented)"
				if e1 != nil {
					reason = "rejected: " + e1.Error()
				}
				log = append(log, fmt.Sprintf("step %d Click(%d) -> %v [%s]",
					step, op.id, e1, reason))
			case opJoin:
				e1 := f.Join(op.id)
				e2 := nf.Join(op.id)
				if !errors.Is(e1, e2) {
					t.Fatalf("seed %d step %d Join(%d) real=%v naive=%v\n%s",
						seed, step, op.id, e1, e2, joinLog(log))
				}
				if e1 == nil {
					registered[op.id] = true
				}
				reason := "accepted (active arm joined current round, sR=0)"
				if e1 != nil {
					reason = "rejected: " + e1.Error()
				}
				log = append(log, fmt.Sprintf("step %d Join(%d) -> %v [%s]",
					step, op.id, e1, reason))
			}

			compareState(t, f, nf, &log)

			if f.Finished() {
				// 结束后继续验证：Next/Join 必拒、Click 记账规则一致。
				for k := 0; k < 5; k++ {
					id := int64(900 + rng.Intn(20))
					e1, e2 := f.Click(id), nf.Click(id)
					if !errors.Is(e1, e2) {
						t.Fatalf("seed %d post-finish Click(%d) %v vs %v", seed, id, e1, e2)
					}
					if e1 == nil {
						compareState(t, f, nf, &log)
					}
				}
				if _, e1 := f.Next(); !errors.Is(e1, ErrFinished) {
					t.Fatalf("seed %d post-finish Next %v", seed, e1)
				}
				if e1 := f.Join(500); !errors.Is(e1, ErrFinished) {
					t.Fatalf("seed %d post-finish Join %v", seed, e1)
				}
				log = append(log, fmt.Sprintf(
					"finished at round %d, winner=%d; post-finish checks passed",
					nf.r, nf.winner))
				break
			}
		}

		// 每组结束打印一行判定摘要（输入参数、输出终态与判定依据）。
		t.Logf("%s | final: finished=%v winner=%d round=%d nexts=%d events=%d => MATCH",
			log[0], nf.finished, nf.winner, nf.r, nf.nexts, len(nf.events))
	}
}

func joinLog(log []string) string {
	if len(log) > 40 {
		log = log[len(log)-40:]
	}
	out := ""
	for _, l := range log {
		out += l + "\n"
	}
	return out
}

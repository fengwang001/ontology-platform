package eliminator

import (
	"fmt"
	"math/rand"
	"sort"
	"strings"
	"testing"
)

// naiveArm 是朴素模拟里的创意状态。
type naiveArm struct {
	id         int
	sT, cT, sR int64
	out        bool
	outReason  string
}

// naiveSim 是按题目规则逐步写成的独立朴素模拟。
type naiveSim struct {
	b, g   int64
	r      int
	arms   map[int]*naiveArm
	order  []int
	active []int
	total  int64
	done   bool
	winner int
	events []Elimination
	log    []string
}

func newNaive(n int, b, g int64) *naiveSim {
	s := &naiveSim{b: b, g: g, r: 1, arms: map[int]*naiveArm{}, winner: -1}
	for id := 0; id < n; id++ {
		s.arms[id] = &naiveArm{id: id}
		s.order = append(s.order, id)
		s.active = append(s.active, id)
	}
	return s
}

func (s *naiveSim) q() int64 {
	k := int64(s.r - 1)
	if k > 20 {
		k = 20
	}
	return s.b * (int64(1) << k)
}

func (s *naiveSim) next() (arm int, evs []Elimination, closed bool, winner int, rej RejectReason) {
	if s.done {
		return 0, nil, false, -1, ReasonFinished
	}
	q := s.q()
	pick, best := -1, int64(0)
	for _, id := range s.active {
		a := s.arms[id]
		if a.sR < q && (pick == -1 || a.sR < best || (a.sR == best && a.id < pick)) {
			pick, best = id, a.sR
		}
	}
	a := s.arms[pick]
	a.sT++
	a.sR++
	s.total++

	// （一）护栏
	if a.cT == 0 && a.sT >= s.g && len(s.active) >= 2 {
		a.out, a.outReason = true, "guard"
		ev := Elimination{Arm: a.id, Round: s.r, Reason: "guard"}
		evs = append(evs, ev)
		s.events = append(s.events, ev)
		s.active = removeID(s.active, a.id)
		if len(s.active) == 1 {
			s.done, s.winner = true, s.active[0]
			s.log = append(s.log, fmt.Sprintf(
				"NEXT->%d 护栏淘汰%d 仅剩%d 结束", pick, a.id, s.winner))
			return pick, evs, false, s.winner, ""
		}
	}

	// （二）收轮
	allFull := true
	for _, id := range s.active {
		if s.arms[id].sR < q {
			allFull = false
		}
	}
	if !allFull {
		s.log = append(s.log, fmt.Sprintf("NEXT->%d r=%d sR=%d/%d",
			pick, s.r, a.sR, q))
		return pick, evs, false, -1, ""
	}

	list := make([]*naiveArm, 0, len(s.active))
	for _, id := range s.active {
		list = append(list, s.arms[id])
	}
	sort.SliceStable(list, func(i, j int) bool {
		x, y := list[i], list[j]
		l, rr := x.cT*y.sT, y.cT*x.sT
		if l != rr {
			return l > rr
		}
		return x.id < y.id
	})
	keep := (len(list) + 1) / 2
	drop := make([]int, 0, len(list)-keep)
	for _, a2 := range list[keep:] {
		drop = append(drop, a2.id)
	}
	sort.Ints(drop)
	for _, id := range drop {
		a2 := s.arms[id]
		a2.out, a2.outReason = true, "round"
		ev := Elimination{Arm: id, Round: s.r, Reason: "round"}
		evs = append(evs, ev)
		s.events = append(s.events, ev)
	}
	s.active = s.active[:0]
	for _, a2 := range list[:keep] {
		s.active = append(s.active, a2.id)
	}
	if len(s.active) == 1 {
		s.done, s.winner = true, s.active[0]
		s.log = append(s.log, fmt.Sprintf(
			"NEXT->%d 收轮淘汰%v 仅剩%d 结束", pick, drop, s.winner))
		return pick, evs, true, s.winner, ""
	}
	s.r++
	for _, id := range s.active {
		s.arms[id].sR = 0
	}
	s.log = append(s.log, fmt.Sprintf("NEXT->%d 收轮淘汰%v 进入r%d", pick, drop, s.r))
	return pick, evs, true, -1, ""
}

func removeID(xs []int, v int) []int {
	for i, x := range xs {
		if x == v {
			return append(xs[:i], xs[i+1:]...)
		}
	}
	return xs
}

func (s *naiveSim) click(id int) RejectReason {
	a, ok := s.arms[id]
	if !ok {
		s.log = append(s.log, fmt.Sprintf("CLICK(%d)->arm_not_found", id))
		return ReasonArmNotFound
	}
	if a.cT >= a.sT {
		s.log = append(s.log, fmt.Sprintf(
			"CLICK(%d)->click_without_exposure (cT=%d sT=%d)", id, a.cT, a.sT))
		return ReasonClickNoExposure
	}
	a.cT++
	s.log = append(s.log, fmt.Sprintf("CLICK(%d)->ok (cT=%d/sT=%d)", id, a.cT, a.sT))
	return ""
}

func (s *naiveSim) join(id int) RejectReason {
	if id < 0 || id > 1000 {
		s.log = append(s.log, fmt.Sprintf("JOIN(%d)->invalid_arguments", id))
		return ReasonInvalidArgs
	}
	if s.done {
		s.log = append(s.log, fmt.Sprintf("JOIN(%d)->already_finished", id))
		return ReasonFinished
	}
	if _, ok := s.arms[id]; ok {
		s.log = append(s.log, fmt.Sprintf("JOIN(%d)->arm_already_exists", id))
		return ReasonArmAlreadyExists
	}
	if len(s.arms) >= 64 {
		s.log = append(s.log, fmt.Sprintf("JOIN(%d)->capacity_full", id))
		return ReasonFull
	}
	s.arms[id] = &naiveArm{id: id}
	s.order = append(s.order, id)
	s.active = append(s.active, id)
	s.log = append(s.log, fmt.Sprintf("JOIN(%d)->ok", id))
	return ""
}

func (s *naiveSim) logText() string { return strings.Join(s.log, "\n") }

type opKind int

const (
	opNext opKind = iota
	opClick
	opJoin
)

type op struct {
	kind opKind
	id   int
}

func opName(k opKind) string {
	switch k {
	case opNext:
		return "Next"
	case opClick:
		return "Click"
	default:
		return "Join"
	}
}

// genOps 生成随机参数与操作序列：Next 为主，Click 偏向已登记编号，
// Join 既产生顺序新编号也产生随机/非法编号以覆盖各类拒绝。
func genOps(rng *rand.Rand) (n int, b, g int64, ops []op) {
	n = 2 + rng.Intn(8)
	b = int64(1 + rng.Intn(4))
	g = int64(1 + rng.Intn(12))
	opsN := 80 + rng.Intn(320)
	ops = make([]op, 0, opsN)
	nextJoin := n
	for i := 0; i < opsN; i++ {
		switch rng.Intn(10) {
		case 0, 1, 2, 3, 4, 5:
			ops = append(ops, op{kind: opNext})
		case 6, 7, 8:
			if rng.Intn(6) == 0 {
				ops = append(ops, op{kind: opClick, id: 500 + rng.Intn(500)})
			} else {
				ops = append(ops, op{kind: opClick, id: rng.Intn(80)})
			}
		default:
			if rng.Intn(3) == 0 && nextJoin <= 1000 {
				ops = append(ops, op{kind: opJoin, id: nextJoin})
				nextJoin++
			} else {
				ops = append(ops, op{kind: opJoin, id: rng.Intn(1003) - 1})
			}
		}
	}
	return
}

func statesEqual(s *naiveSim, e *Eliminator) string {
	snap := e.Snapshot()
	if s.done != snap.Finished {
		return fmt.Sprintf("finished sim=%v impl=%v", s.done, snap.Finished)
	}
	if s.winner != snap.Winner {
		return fmt.Sprintf("winner sim=%d impl=%d", s.winner, snap.Winner)
	}
	if s.r != snap.Round {
		return fmt.Sprintf("round sim=%d impl=%d", s.r, snap.Round)
	}
	if s.total != snap.TotalNext {
		return fmt.Sprintf("total sim=%d impl=%d", s.total, snap.TotalNext)
	}
	if len(s.order) != len(snap.Arms) {
		return fmt.Sprintf("armCount sim=%d impl=%d", len(s.order), len(snap.Arms))
	}
	for _, id := range s.order {
		a := s.arms[id]
		var z ArmState
		for _, x := range snap.Arms {
			if x.ID == id {
				z = x
			}
		}
		if a.sT != z.Exposures || a.cT != z.Clicks || a.sR != z.RoundExp ||
			a.out != z.Eliminated || a.outReason != z.ElimReason {
			return fmt.Sprintf(
				"arm %d sim={sT:%d cT:%d sR:%d out:%v %s} impl={%d %d %d %v %s}",
				id, a.sT, a.cT, a.sR, a.out, a.outReason,
				z.Exposures, z.Clicks, z.RoundExp, z.Eliminated, z.ElimReason)
		}
	}
	if fmt.Sprint(s.events) != fmt.Sprint(snap.Eliminations) {
		return fmt.Sprintf("events sim=%v impl=%v", s.events, snap.Eliminations)
	}
	return ""
}

func snapText(e *Eliminator) string {
	s := e.Snapshot()
	var b strings.Builder
	fmt.Fprintf(&b, "round=%d done=%v winner=%d total=%d\n",
		s.Round, s.Finished, s.Winner, s.TotalNext)
	for _, a := range s.Arms {
		fmt.Fprintf(&b, "  arm %d: sT=%d cT=%d sR=%d out=%v %s\n",
			a.ID, a.Exposures, a.Clicks, a.RoundExp, a.Eliminated, a.ElimReason)
	}
	fmt.Fprintf(&b, "events=%v", s.Eliminations)
	return b.String()
}

func evsText(evs []Elimination) string {
	if len(evs) == 0 {
		return ""
	}
	parts := make([]string, len(evs))
	for i, ev := range evs {
		parts[i] = fmt.Sprintf("(%d,%s,r%d)", ev.Arm, ev.Reason, ev.Round)
	}
	return "[" + strings.Join(parts, ",") + "]"
}

// TestRandomDifferential 用 2000 组随机操作序列对照朴素模拟，
// 逐步比较拒绝原因、Next 结果与完整状态；每组打印输入、输出与判定依据。
func TestRandomDifferential(t *testing.T) {
	const totalCases = 2000
	rng := rand.New(rand.NewSource(20261003))
	for c := 0; c < totalCases; c++ {
		n, b, g, ops := genOps(rng)
		sim := newNaive(n, b, g)
		impl, err := New(n, b, g)
		if err != nil {
			t.Fatalf("case %d New: %v", c, err)
		}

		var input, output strings.Builder
		fmt.Fprintf(&input, "New(n=%d,b=%d,G=%d); ", n, b, g)

		failf := func(format string, args ...any) {
			t.Fatalf("case %d "+format+"\n输入序列: %s\n朴素判定依据:\n%s\n实现快照:\n%s",
				append([]any{c}, append(args,
					input.String(), sim.logText(), snapText(impl))...)...)
		}

		for i, o := range ops {
			switch o.kind {
			case opNext:
				input.WriteString("Next ")
			case opClick:
				fmt.Fprintf(&input, "Click(%d) ", o.id)
			case opJoin:
				fmt.Fprintf(&input, "Join(%d) ", o.id)
			}

			var (
				simArm, simWinner = 0, -1
				simEvs            []Elimination
				simClosed         bool
				simRej, implRej   RejectReason
				res               NextResult
			)
			switch o.kind {
			case opNext:
				simArm, simEvs, simClosed, simWinner, simRej = sim.next()
				res, err = impl.Next()
			case opClick:
				simRej = sim.click(o.id)
				err = impl.Click(o.id)
			case opJoin:
				simRej = sim.join(o.id)
				err = impl.Join(o.id)
			}
			if err != nil {
				implRej = reasonOf(err)
			}
			if simRej != implRej {
				failf("op %d %s(%d) 拒绝原因不一致: sim=%q impl=%q",
					i, opName(o.kind), o.id, simRej, implRej)
			}
			if o.kind == opNext && simRej == "" {
				if res.Arm != simArm || res.RoundClosed != simClosed ||
					res.Winner != simWinner ||
					fmt.Sprint(res.Eliminated) != fmt.Sprint(simEvs) {
					failf("op %d Next 结果不一致: sim={arm:%d closed:%v winner:%d evs:%v} impl=%+v",
						i, simArm, simClosed, simWinner, simEvs, res)
				}
				fmt.Fprintf(&output, "Next->%d%s ", simArm, evsText(simEvs))
			} else if simRej != "" {
				fmt.Fprintf(&output, "%s(%d)->%s ", opName(o.kind), o.id, simRej)
			} else {
				output.WriteString(opName(o.kind) + "(ok) ")
			}

			if diff := statesEqual(sim, impl); diff != "" {
				failf("op %d 操作后状态不一致: %s", i, diff)
			}
		}

		t.Logf("case %d 输入: %s", c, input.String())
		t.Logf("case %d 输出: %s | 判定: done=%v winner=%d events=%v",
			c, output.String(), sim.done, sim.winner, sim.events)
	}
}

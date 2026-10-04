package hold

import (
	"errors"
	"fmt"
	"math/rand"
	"testing"

	"ontology/routing"
	"ontology/wip"
)

// naivePiece 逐件跟踪一件的位置与返工次数。
type naivePiece struct {
	step     int
	reworks  int
	done     bool
	scrapped bool
}

type naiveWO struct {
	route   string
	qty     int64
	pieces  map[string]*naivePiece
	nextID  int
	done    int64
	scrap   int64
	closed  bool
	held    bool
	fpGood  map[int]int64
	fpTotal map[int]int64
}

type naiveWorld struct {
	back []int
	insp []bool
	R    int
	y    int
	nmin int64
	wo   map[string]*naiveWO
	log  []string
}

func newNaive(back []int, insp []bool, R, y int, nmin int64) *naiveWorld {
	return &naiveWorld{
		back: back, insp: insp, R: R, y: y, nmin: nmin,
		wo: make(map[string]*naiveWO),
	}
}

func TestRandomAgainstNaive(t *testing.T) {
	if testing.Short() {
		t.Skip()
	}
	const groups = 1500
	const opsPerGroup = 60

	for g := 0; g < groups; g++ {
		rng := rand.New(rand.NewSource(int64(g + 1)))
		n := 1 + rng.Intn(5)
		back := make([]int, n)
		insp := make([]bool, n)
		for i := range back {
			back[i] = 1 + rng.Intn(i+1)
			insp[i] = rng.Intn(2) == 0
		}
		R := rng.Intn(3)
		y := 1 + rng.Intn(100)
		nmin := int64(1 + rng.Intn(20))

		rm := routing.NewManager()
		route := fmt.Sprintf("rt%d", g)
		if err := rm.Define(route, n, back, insp, R); err != nil {
			t.Fatal(err)
		}
		wm, hm, err := Config(rm, y, nmin, func(op string) bool { return op == "qe" })
		if err != nil {
			t.Fatal(err)
		}
		nw := newNaive(back, insp, R, y, nmin)

		initial := 1 + rng.Intn(3)
		var ids []string
		for j := 0; j < initial; j++ {
			id := fmt.Sprintf("g%dw%d", g, j)
			Q := int64(1 + rng.Intn(12))
			if err := hm.Open(id, route, Q); err != nil {
				t.Fatal(err)
			}
			nw.open(id, route, Q)
			ids = append(ids, id)
		}

		var ops []op
		for s := 0; s < opsPerGroup; s++ {
			id := ids[rng.Intn(len(ids))]
			nw0 := nw.wo[id]
			switch rng.Intn(4) {
			case 0:
				c, ok := pickCell(rng, nw, nw0)
				if !ok {
					continue
				}
				avail := nw.cell(nw0, c.i, c.k)
				total := int64(1 + rng.Intn(int(avail)))
				if rng.Intn(5) == 0 {
					total = avail + int64(rng.Intn(3)) // 偶发超量
				}
				g1 := rng.Int63n(total + 1)
				rest := total - g1
				s1 := rng.Int63n(rest + 1)
				ops = append(ops, op{
					kind: opReport, wo: id, i: c.i, k: c.k,
					good: g1, scrap: s1, rework: rest - s1,
				})
			case 1:
				c, ok := pickCell(rng, nw, nw0)
				if !ok {
					continue
				}
				avail := nw.cell(nw0, c.i, c.k)
				newID := fmt.Sprintf("%ss%d", id, s)
				qty := int64(1 + rng.Intn(int(avail)))
				if rng.Intn(5) == 0 {
					qty = avail + int64(1+rng.Intn(3))
				}
				ops = append(ops, op{
					kind: opSplit, wo: id, newWo: newID,
					i: c.i, k: c.k, qty: qty,
				})
			case 2:
				ops = append(ops, op{kind: opClose, wo: id})
			case 3:
				ops = append(ops, op{kind: opResume, wo: id, operator: "qe"})
			}
		}

		// 固定非法操作：验证拒绝次序与一致性。
		ops = append(ops,
			op{kind: opReport, wo: "ghost", i: 1, k: 0, good: 1},
			op{kind: opReport, wo: ids[0], i: 0, k: 0, good: 1},
			op{kind: opReport, wo: ids[0], i: 1, k: -1, good: 1},
			op{kind: opReport, wo: ids[0], i: 1, k: 0, good: -1},
			op{kind: opSplit, wo: "ghost", newWo: "x", i: 1, k: 0, qty: 1},
			op{kind: opSplit, wo: ids[0], newWo: "", i: 1, k: 0, qty: 1},
			op{kind: opClose, wo: "ghost"},
			op{kind: opResume, wo: "ghost", operator: "qe"},
			op{kind: opResume, wo: ids[0], operator: "ops"}, // 无权限
		)

		for idx, o := range ops {
			var got error
			desc := ""
			switch o.kind {
			case opReport:
				got = wm.Report(o.wo, o.i, o.k, o.good, o.scrap, o.rework)
				desc = fmt.Sprintf("REPORT %s i=%d k=%d good=%d scrap=%d rework=%d",
					o.wo, o.i, o.k, o.good, o.scrap, o.rework)
			case opSplit:
				got = wm.Split(o.wo, o.newWo, o.i, o.k, o.qty)
				desc = fmt.Sprintf("SPLIT %s->%s i=%d k=%d qty=%d",
					o.wo, o.newWo, o.i, o.k, o.qty)
				if got == nil {
					ids = append(ids, o.newWo)
				}
			case opClose:
				_, got = wm.Close(o.wo)
				desc = fmt.Sprintf("CLOSE %s", o.wo)
			case opResume:
				got = hm.Resume(o.wo, o.operator)
				desc = fmt.Sprintf("RESUME %s by=%s", o.wo, o.operator)
			}
			want := nw.apply(o)
			if !sameErr(got, want) {
				t.Fatalf("group %d op %d %s => real=%v naive=%v",
					g, idx, desc, got, want)
			}
			if g == 0 {
				basis := ""
				if o.kind == opReport && o.k == 0 && o.i >= 1 && o.i <= len(nw.insp) && nw.insp[o.i-1] {
					w0 := nw.wo[o.wo]
					if w0 != nil {
						fg, ft := w0.fpGood[o.i], w0.fpTotal[o.i]
						basis = fmt.Sprintf(" | fp@%d=%d/%d Y=%d Nmin=%d held=%v (judge: %d>=%d && %d*100<%d*%d)",
							o.i, fg, ft, y, nmin, w0.held,
							ft, nmin, fg, y, ft)
					}
				}
				t.Logf("g0 op %2d IN {%s} OUT {%v}%s", idx, desc, got, basis)
			}
			for _, id := range ids {
				compareWorlds(t, g, idx, id, wm, hm, nw)
			}
		}

		if g < 5 {
			t.Logf("group %d params n=%d R=%d Y=%d Nmin=%d: %d ops replayed, all cells matched",
				g, n, R, y, nmin, len(ops))
		}
	}
}

type cellPick struct{ i, k int }

func pickCell(rng *rand.Rand, nw *naiveWorld, w *naiveWO) (cellPick, bool) {
	seen := make(map[cellPick]bool)
	var cells []cellPick
	for _, p := range w.pieces {
		if p.done || p.scrapped {
			continue
		}
		c := cellPick{p.step, p.reworks}
		if !seen[c] {
			seen[c] = true
			cells = append(cells, c)
		}
	}
	if len(cells) == 0 {
		return cellPick{}, false
	}
	return cells[rng.Intn(len(cells))], true
}

func (nw *naiveWorld) open(id, route string, Q int64) {
	w := &naiveWO{
		route:   route,
		qty:     Q,
		pieces:  make(map[string]*naivePiece),
		fpGood:  make(map[int]int64),
		fpTotal: make(map[int]int64),
	}
	for range Q {
		w.pieces[fmt.Sprintf("%s#%d", id, w.nextID)] = &naivePiece{step: 1}
		w.nextID++
	}
	nw.wo[id] = w
	nw.log = append(nw.log, fmt.Sprintf("OPEN %s Q=%d", id, Q))
}

func (nw *naiveWorld) cell(w *naiveWO, i, k int) int64 {
	var c int64
	for _, p := range w.pieces {
		if !p.done && !p.scrapped && p.step == i && p.reworks == k {
			c++
		}
	}
	return c
}

type opKind int

const (
	opReport opKind = iota
	opSplit
	opClose
	opResume
)

type op struct {
	kind                opKind
	wo, newWo           string
	i, k                int
	good, scrap, rework int64
	qty                 int64
	operator            string
}

// apply 逐件执行并返回朴素模型的拒绝错误。
func (nw *naiveWorld) apply(o op) error {
	w := nw.wo[o.wo]
	switch o.kind {
	case opReport:
		if o.wo == "" || o.i < 1 || o.i > len(nw.back) || o.k < 0 || o.k > nw.R ||
			o.good < 0 || o.scrap < 0 || o.rework < 0 ||
			o.good+o.scrap+o.rework < 1 {
			return routing.ErrInvalid
		}
		if w == nil {
			return routing.ErrNotFound
		}
		if w.closed || w.held {
			return routing.ErrState
		}
		total := o.good + o.scrap + o.rework
		if total > nw.cell(w, o.i, o.k) {
			return routing.ErrOverflow
		}
		if o.rework > 0 && o.k == nw.R {
			return routing.ErrReworkCap
		}
		var chosen []*naivePiece
		for _, p := range w.pieces {
			if !p.done && !p.scrapped && p.step == o.i && p.reworks == o.k {
				chosen = append(chosen, p)
				if int64(len(chosen)) == total {
					break
				}
			}
		}
		idx := 0
		for range o.good {
			p := chosen[idx]
			idx++
			if o.i == len(nw.back) {
				p.done = true
				w.done++
			} else {
				p.step = o.i + 1
			}
		}
		for range o.scrap {
			p := chosen[idx]
			idx++
			p.scrapped = true
			w.scrap++
		}
		for range o.rework {
			p := chosen[idx]
			idx++
			p.step = nw.back[o.i-1]
			p.reworks++
		}
		if o.k == 0 && nw.insp[o.i-1] {
			w.fpGood[o.i] += o.good
			w.fpTotal[o.i] += total
			if w.fpTotal[o.i] >= nw.nmin &&
				w.fpGood[o.i]*100 < int64(nw.y)*w.fpTotal[o.i] {
				w.held = true
			}
		}
		return nil
	case opSplit:
		if o.wo == "" || o.newWo == "" || o.i < 1 || o.i > len(nw.back) ||
			o.k < 0 || o.k > nw.R || o.qty < 1 {
			return routing.ErrInvalid
		}
		if w == nil {
			return routing.ErrNotFound
		}
		if _, dup := nw.wo[o.newWo]; dup || w.closed || w.held {
			return routing.ErrState
		}
		if o.qty > nw.cell(w, o.i, o.k) {
			return routing.ErrOverflow
		}
		nw2 := &naiveWO{
			route:   w.route,
			qty:     o.qty,
			pieces:  make(map[string]*naivePiece),
			fpGood:  make(map[int]int64),
			fpTotal: make(map[int]int64),
		}
		moved := int64(0)
		for pid, p := range w.pieces {
			if moved == o.qty {
				break
			}
			if !p.done && !p.scrapped && p.step == o.i && p.reworks == o.k {
				delete(w.pieces, pid)
				nw2.pieces[pid+"@"] = p
				moved++
			}
		}
		w.qty -= o.qty
		nw.wo[o.newWo] = nw2
		return nil
	case opClose:
		if w == nil {
			return routing.ErrNotFound
		}
		if w.closed || w.held {
			return routing.ErrState
		}
		for _, p := range w.pieces {
			if !p.done && !p.scrapped {
				return routing.ErrState
			}
		}
		w.closed = true
		return nil
	case opResume:
		if o.wo == "" || o.operator == "" {
			return routing.ErrInvalid
		}
		if o.operator != "qe" {
			return routing.ErrForbidden
		}
		if w == nil {
			return routing.ErrNotFound
		}
		if w.closed || !w.held {
			return routing.ErrState
		}
		w.held = false
		w.fpGood = make(map[int]int64)
		w.fpTotal = make(map[int]int64)
		return nil
	}
	return nil
}

func sameErr(a, b error) bool {
	return errors.Is(a, b) || (a == nil && b == nil)
}

func compareWorlds(t *testing.T, g, idx int, id string, wm *wip.Manager, h *Manager, nw *naiveWorld) {
	t.Helper()
	s, err := wm.State(id)
	if err != nil {
		t.Fatalf("group %d op %d: State(%s): %v", g, idx, id, err)
	}
	w := nw.wo[id]
	if s.Q != w.qty || s.Done != w.done || s.Scrapped != w.scrap {
		t.Fatalf("group %d op %d %s totals real{Q=%d done=%d scrap=%d} naive{Q=%d done=%d scrap=%d}",
			g, idx, id, s.Q, s.Done, s.Scrapped, w.qty, w.done, w.scrap)
	}
	if s.Closed != w.closed || s.Held != w.held {
		t.Fatalf("group %d op %d %s flags real{closed=%v held=%v} naive{closed=%v held=%v}",
			g, idx, id, s.Closed, s.Held, w.closed, w.held)
	}
	// 队列格：按 naive 全量计算期望，逐格与 sparse map 比对。
	expect := make(map[[2]int]int64)
	var wipCount int64
	for _, p := range w.pieces {
		if !p.done && !p.scrapped {
			expect[[2]int{p.step, p.reworks}]++
			wipCount++
		}
	}
	for ck, v := range expect {
		if s.Queue[ck] != v {
			t.Fatalf("group %d op %d %s cell %v real=%d naive=%d",
				g, idx, id, ck, s.Queue[ck], v)
		}
	}
	for ck, v := range s.Queue {
		if expect[ck] != v {
			t.Fatalf("group %d op %d %s unexpected real cell %v=%d (naive %d)",
				g, idx, id, ck, v, expect[ck])
		}
	}
	if s.Q != s.Done+s.Scrapped+wipCount {
		t.Fatalf("group %d op %d %s conservation Q=%d vs %d+%d+%d",
			g, idx, id, s.Q, s.Done, s.Scrapped, wipCount)
	}
	// 首过统计逐检验点比对。
	for step := 1; step <= len(nw.back); step++ {
		if !nw.insp[step-1] {
			continue
		}
		gotGood, gotTotal, ok := h.FirstPass(id, step)
		if !ok {
			t.Fatalf("group %d op %d %s missing hold state", g, idx, id)
		}
		if gotGood != w.fpGood[step] || gotTotal != w.fpTotal[step] {
			t.Fatalf("group %d op %d %s fp@%d real=%d/%d naive=%d/%d",
				g, idx, id, step, gotGood, gotTotal, w.fpGood[step], w.fpTotal[step])
		}
	}
}

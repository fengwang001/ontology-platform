package wave_test

import (
	"fmt"
	"math/rand"
	"sort"
	"strings"
	"testing"

	"ontology/slot"
	"ontology/wave"
)

// ---------- 朴素参考模型：严格按题面逐步重算，不依赖被测实现 ----------

type nLoc struct {
	id  string
	loc int // 1=Bulk 2=Pick
	sku string
	on  int64
	res map[string]int64 // order -> 未拣量
	lk  bool
}

type nOrder struct {
	id    string
	pr    int
	lines []wave.Line
	st    wave.Status
	short int64
}

type naive struct {
	pal map[string]int64
	lcs map[string]*nLoc
	ods map[string]*nOrder
}

const (
	nOK = iota
	nInvalid
	nNotFound
	nBadState
	nQty
	nConflict
)

func newNaive() *naive {
	return &naive{pal: map[string]int64{}, lcs: map[string]*nLoc{}, ods: map[string]*nOrder{}}
}

func (n *naive) avail(l *nLoc) int64 {
	if l.lk {
		return 0
	}
	var r int64
	for _, q := range l.res {
		r += q
	}
	return l.on - r
}

func (n *naive) skuLocs(sku string, kind int) []*nLoc {
	var out []*nLoc
	for _, l := range n.lcs {
		if l.sku == sku && l.loc == kind {
			out = append(out, l)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].id < out[j].id })
	return out
}

// allocOne 三步分配；即时写入 l.res（即预占账），返回 loc->qty 与缺口。
func (n *naive) allocOne(orderID, sku string, q int64) (map[string]int64, int64) {
	takes := map[string]int64{}
	p := n.pal[sku]
	// (1) 整托
	k := q / p
	var got int64
	for _, l := range n.skuLocs(sku, 1) {
		if got == k {
			break
		}
		caps := n.avail(l) / p
		if caps <= 0 {
			continue
		}
		if caps > k-got {
			caps = k - got
		}
		l.res[orderID] += caps * p
		takes[l.id] += caps * p
		got += caps
	}
	rem := q - got*p
	// (2) Pick
	for _, l := range n.skuLocs(sku, 2) {
		if rem == 0 {
			break
		}
		a := n.avail(l)
		if a <= 0 {
			continue
		}
		g := a
		if g > rem {
			g = rem
		}
		l.res[orderID] += g
		takes[l.id] += g
		rem -= g
	}
	// (3) 回补：此刻可用升序、并列编号升序、0 跳过
	if rem > 0 {
		var cands []*nLoc
		for _, l := range n.skuLocs(sku, 1) {
			if n.avail(l) > 0 {
				cands = append(cands, l)
			}
		}
		sort.Slice(cands, func(i, j int) bool {
			ai, aj := n.avail(cands[i]), n.avail(cands[j])
			if ai != aj {
				return ai < aj
			}
			return cands[i].id < cands[j].id
		})
		for _, l := range cands {
			if rem == 0 {
				break
			}
			a := n.avail(l)
			g := a
			if g > rem {
				g = rem
			}
			l.res[orderID] += g
			takes[l.id] += g
			rem -= g
		}
	}
	return takes, rem
}

func (n *naive) rollback(orderID string, takes map[string]int64) {
	for lid, q := range takes {
		l := n.lcs[lid]
		l.res[orderID] -= q
		if l.res[orderID] == 0 {
			delete(l.res, orderID)
		}
	}
}

func (n *naive) release(ids []string) map[string]int {
	result := map[string]int{}
	sorted := append([]string(nil), ids...)
	sort.Slice(sorted, func(i, j int) bool {
		a, b := n.ods[sorted[i]], n.ods[sorted[j]]
		if a.pr != b.pr {
			return a.pr > b.pr
		}
		return a.id < b.id
	})
	for _, id := range sorted {
		o := n.ods[id]
		var lineTakes []map[string]int64
		failed := false
		for _, ln := range o.lines {
			tk, miss := n.allocOne(id, ln.SKU, ln.Qty)
			lineTakes = append(lineTakes, tk)
			if miss > 0 {
				failed = true
				break
			}
		}
		if failed {
			for _, tk := range lineTakes {
				n.rollback(id, tk)
			}
			o.st = wave.StatusBackorder
			result[id] = int(wave.StatusBackorder)
			continue
		}
		o.st = wave.StatusAllocated
		result[id] = int(wave.StatusAllocated)
	}
	return result
}

func (n *naive) orderTotal(id string) int64 {
	var t int64
	for _, l := range n.lcs {
		t += l.res[id]
	}
	return t
}

func (n *naive) finalize(o *nOrder) {
	if n.orderTotal(o.id) == 0 {
		if o.short == 0 {
			o.st = wave.StatusDone
		} else {
			o.st = wave.StatusShort
		}
	}
}

func (n *naive) pick(id, lid string, q int64) int {
	o, ok := n.ods[id]
	if !ok {
		return nNotFound
	}
	l, ok := n.lcs[lid]
	if !ok {
		return nNotFound
	}
	rq, ok := l.res[id]
	if !ok {
		return nNotFound
	}
	if q != rq {
		return nQty
	}
	l.on -= q
	delete(l.res, id)
	n.finalize(o)
	return nOK
}

func (n *naive) shortPick(id, lid string, found int64) int {
	o, ok := n.ods[id]
	if !ok {
		return nNotFound
	}
	l, ok := n.lcs[lid]
	if !ok {
		return nNotFound
	}
	rq, ok := l.res[id]
	if !ok {
		return nNotFound
	}
	if found >= rq {
		return nQty
	}
	sku := l.sku
	unpicked := rq
	l.on -= found

	type def struct {
		o   *nOrder
		qty int64
	}
	queue := []def{{o, unpicked - found}}
	var affected []def
	for oid, q := range l.res {
		if oid != id {
			affected = append(affected, def{n.ods[oid], q})
		}
	}
	sort.Slice(affected, func(i, j int) bool {
		if affected[i].o.pr != affected[j].o.pr {
			return affected[i].o.pr > affected[j].o.pr
		}
		return affected[i].o.id < affected[j].o.id
	})
	l.res = map[string]int64{}
	l.lk = true
	queue = append(queue, affected...)

	touched := map[string]*nOrder{}
	for _, d := range queue {
		tk, miss := n.allocOne(d.o.id, sku, d.qty)
		_ = tk
		if miss > 0 {
			d.o.short += miss
		}
		touched[d.o.id] = d.o
	}
	for _, oo := range touched {
		n.finalize(oo)
	}
	return nOK
}

func (n *naive) unlock(lid string, counted int64) int {
	l, ok := n.lcs[lid]
	if !ok {
		return nNotFound
	}
	if !l.lk {
		return nBadState
	}
	l.lk = false
	l.on = counted
	l.res = map[string]int64{}
	return nOK
}

func (n *naive) cancel(id string) int {
	o, ok := n.ods[id]
	if !ok {
		return nNotFound
	}
	if o.st != wave.StatusAllocated {
		return nBadState
	}
	for _, l := range n.lcs {
		if q, ok := l.res[id]; ok {
			l.on -= 0
			_ = q
			delete(l.res, id)
		}
	}
	o.st = wave.StatusCancelled
	return nOK
}

func (n *naive) allocs(id string) map[string]int64 {
	m := map[string]int64{}
	for _, l := range n.lcs {
		if q, ok := l.res[id]; ok && q > 0 {
			m[l.id] = q
		}
	}
	return m
}

// ---------- 随机操作序列对拍 ----------

type op struct {
	kind  int
	a, b  string
	k     slot.Kind
	q     int64
	p     int
	ids   []string
	lines []wave.Line
}

const (
	opSetPallet = iota
	opPutStock
	opAddOrder
	opRelease
	opPick
	opShortPick
	opUnlock
	opCancel
)

func TestNaiveModelRandom(t *testing.T) {
	const sequences = 1500
	rng := rand.New(rand.NewSource(20261004))
	for seq := 0; seq < sequences; seq++ {
		n := runNaiveSequence(t, rng, seq)
		_ = n
	}
}

func runNaiveSequence(t *testing.T, rng *rand.Rand, seq int) *naive {
	t.Helper()
	n := newNaive()
	c := wave.New()
	var sb strings.Builder
	fmt.Fprintf(&sb, "=== sequence %d ===\n", seq)

	skus := []string{"s1", "s2", "s3"}
	pal := []int64{3, 5, 10}
	locs := []string{"B1", "B2", "B3", "B4", "K1", "K2", "K3", "K4"}
	locKind := map[string]slot.Kind{}
	locSKU := map[string]string{}
	isBulk := func(id string) bool { return id[0] == 'B' }

	// 初始：SetPallet + 随机库存
	for i, s := range skus {
		code := func() int {
			if err := c.SetPallet(s, pal[i]); err != nil {
				return errCode(err)
			}
			n.pal[s] = pal[i]
			return nOK
		}()
		fmt.Fprintf(&sb, "SetPallet(%s,%d) -> %d\n", s, pal[i], code)
	}
	for _, lid := range locs {
		if rng.Intn(3) == 0 {
			continue
		}
		sku := skus[rng.Intn(len(skus))]
		var k slot.Kind
		if isBulk(lid) {
			k = slot.Bulk
		} else {
			k = slot.Pick
		}
		q := int64(rng.Intn(12) + 1)
		err := c.PutStock(lid, k, sku, q)
		if err != nil {
			t.Fatalf("seq %d initial PutStock(%s,%s,%d): %v", seq, lid, sku, q, err)
		}
		n.lcs[lid] = &nLoc{id: lid, sku: sku, on: q, res: map[string]int64{},
			loc: map[bool]int{true: 1, false: 2}[isBulk(lid)]}
		locKind[lid] = k
		locSKU[lid] = sku
		fmt.Fprintf(&sb, "PutStock(%s,%s,%d) -> ok\n", lid, sku, q)
	}

	type oInfo struct {
		lines []wave.Line
	}
	orderInfo := map[string]*oInfo{}
	allocated := []string{}
	opCount := 12 + rng.Intn(20)
	for step := 0; step < opCount; step++ {
		kind := rng.Intn(8)
		switch kind {
		case opPutStock:
			lid := locs[rng.Intn(len(locs))]
			sku := skus[rng.Intn(len(skus))]
			var k slot.Kind
			if isBulk(lid) {
				k = slot.Bulk
			} else {
				k = slot.Pick
			}
			q := int64(rng.Intn(8) + 1)
			e1 := errCode(c.PutStock(lid, k, sku, q))
			e2 := nPutStock(n, lid, isBulk(lid), sku, q)
			fmt.Fprintf(&sb, "PutStock(%s,%s,%d) -> impl=%d naive=%d\n", lid, sku, q, e1, e2)
			if e1 != e2 {
				t.Fatalf("seq %d step %d PutStock mismatch:\n%s", seq, step, sb.String())
			}
		case opAddOrder:
			id := fmt.Sprintf("O%d", len(orderInfo))
			if _, exists := orderInfo[id]; exists {
				continue
			}
			pri := rng.Intn(10)
			nl := 1 + rng.Intn(2)
			perm := rng.Perm(len(skus))[:nl]
			var lines []wave.Line
			for _, idx := range perm {
				lines = append(lines, wave.Line{SKU: skus[idx], Qty: int64(1 + rng.Intn(15))})
			}
			e1 := errCode(c.AddOrder(id, pri, lines))
			if e1 == nOK {
				n.ods[id] = &nOrder{id: id, pr: pri, lines: lines, st: wave.StatusNew}
			}
			orderInfo[id] = &oInfo{lines: lines}
			fmt.Fprintf(&sb, "AddOrder(%s,p=%d,%v) -> %d\n", id, pri, lines, e1)
		case opRelease:
			cands := releaseCandidates(n)
			if len(cands) == 0 {
				continue
			}
			rng.Shuffle(len(cands), func(i, j int) { cands[i], cands[j] = cands[j], cands[i] })
			cnt := 1 + rng.Intn(len(cands))
			ids := append([]string(nil), cands[:cnt]...)
			res, err := c.Release(ids)
			e1 := errCode(err)
			e2 := nValidateRelease(n, ids)
			fmt.Fprintf(&sb, "Release(%v) -> impl=%d naive=%d\n", ids, e1, e2)
			if e1 != e2 {
				t.Fatalf("seq %d step %d Release reject mismatch:\n%s", seq, step, sb.String())
			}
			if e1 == nOK {
				nres := n.release(ids)
				if len(res) != len(ids) {
					t.Fatalf("seq %d result count %d != %d:\n%s", seq, len(res), len(ids), sb.String())
				}
				for _, r := range res {
					if int(r.State) != nres[r.Order] {
						t.Fatalf("seq %d Release %s state impl=%d naive=%d:\n%s",
							seq, r.Order, r.State, nres[r.Order], sb.String())
					}
					got, _ := c.OrderAllocs(r.Order)
					gm := map[string]int64{}
					for _, a := range got {
						gm[a.Loc] = a.Qty
					}
					nm := n.allocs(r.Order)
					if !eqMap(gm, nm) {
						t.Fatalf("seq %d Release %s allocs impl=%v naive=%v:\n%s",
							seq, r.Order, gm, nm, sb.String())
					}
				}
				allocated = append(allocated, ids...)
			}
		case opPick, opShortPick:
			pick := pickCandidates(n)
			if len(pick) == 0 {
				continue
			}
			pi := pick[rng.Intn(len(pick))]
			rq := n.lcs[pi.loc].res[pi.id]
			if kind == opPick {
				var q int64
				switch rng.Intn(3) {
				case 0:
					q = rq
				case 1:
					q = rq - 1
					if q <= 0 {
						q = rq
					}
				default:
					q = rq + 1
				}
				e1 := errCode(c.Pick(pi.id, pi.loc, q))
				e2 := n.pick(pi.id, pi.loc, q)
				fmt.Fprintf(&sb, "Pick(%s,%s,%d of %d) -> impl=%d naive=%d\n", pi.id, pi.loc, q, rq, e1, e2)
				if e1 != e2 {
					t.Fatalf("seq %d Pick mismatch:\n%s", seq, sb.String())
				}
			} else {
				found := int64(rng.Intn(int(rq) + 2)) // 0..rq+1，可能 >= rq
				e1 := errCode(c.ShortPick(pi.id, pi.loc, found))
				e2 := n.shortPick(pi.id, pi.loc, found)
				fmt.Fprintf(&sb, "ShortPick(%s,%s,found=%d of %d) -> impl=%d naive=%d\n",
					pi.id, pi.loc, found, rq, e1, e2)
				if e1 != e2 {
					t.Fatalf("seq %d ShortPick reject mismatch:\n%s", seq, sb.String())
				}
			}
			compareWorld(t, c, n, seq, step, &sb)
		case opUnlock:
			var locked []string
			for lid, l := range n.lcs {
				if l.lk {
					locked = append(locked, lid)
				}
			}
			lid := locs[rng.Intn(len(locs))]
			if len(locked) > 0 && rng.Intn(2) == 0 {
				lid = locked[rng.Intn(len(locked))]
			}
			counted := int64(rng.Intn(10))
			e1 := errCode(c.Unlock(lid, counted))
			e2 := n.unlock(lid, counted)
			fmt.Fprintf(&sb, "Unlock(%s,%d) -> impl=%d naive=%d\n", lid, counted, e1, e2)
			if e1 != e2 {
				t.Fatalf("seq %d Unlock mismatch:\n%s", seq, sb.String())
			}
		case opCancel:
			if len(allocated) == 0 {
				continue
			}
			id := allocated[rng.Intn(len(allocated))]
			e1 := errCode(c.Cancel(id))
			e2 := n.cancel(id)
			fmt.Fprintf(&sb, "Cancel(%s) -> impl=%d naive=%d\n", id, e1, e2)
			if e1 != e2 {
				t.Fatalf("seq %d Cancel mismatch:\n%s", seq, sb.String())
			}
		}
	}
	compareWorld(t, c, n, seq, -1, &sb)
	t.Logf("sequence %d accepted (%d ops)\n%s", seq, opCount, sb.String())
	return n
}

type pickPair struct{ id, loc string }

func pickCandidates(n *naive) []pickPair {
	var out []pickPair
	for lid, l := range n.lcs {
		for oid := range l.res {
			out = append(out, pickPair{oid, lid})
		}
	}
	return out
}

func releaseCandidates(n *naive) []string {
	var out []string
	for id, o := range n.ods {
		if o.st == wave.StatusNew || o.st == wave.StatusBackorder {
			out = append(out, id)
		}
	}
	sort.Strings(out)
	return out
}

func nValidateRelease(n *naive, ids []string) int {
	seen := map[string]bool{}
	for _, id := range ids {
		if seen[id] {
			return nInvalid
		}
		seen[id] = true
		o, ok := n.ods[id]
		if !ok {
			return nNotFound
		}
		if o.st != wave.StatusNew && o.st != wave.StatusBackorder {
			return nBadState
		}
	}
	return nOK
}

func nPutStock(n *naive, lid string, bulk bool, sku string, q int64) int {
	cur, ok := n.lcs[lid]
	if !ok {
		if _, ok := n.pal[sku]; !ok {
			return nNotFound
		}
		n.lcs[lid] = &nLoc{id: lid, sku: sku, on: q, res: map[string]int64{},
			loc: map[bool]int{true: 1, false: 2}[bulk]}
		return nOK
	}
	if cur.lk {
		return nBadState
	}
	kindN := map[bool]int{true: 1, false: 2}[bulk]
	if cur.loc != kindN || cur.sku != sku {
		return nConflict
	}
	cur.on += q
	return nOK
}

func errCode(err error) int {
	switch {
	case err == nil:
		return nOK
	case errorsIs(err, wave.ErrInvalidArg):
		return nInvalid
	case errorsIs(err, wave.ErrNotFound):
		return nNotFound
	case errorsIs(err, wave.ErrBadState):
		return nBadState
	case errorsIs(err, wave.ErrQtyMismatch):
		return nQty
	case errorsIs(err, wave.ErrConflict):
		return nConflict
	default:
		return -1
	}
}

func errorsIs(err, target error) bool {
	if err == target {
		return true
	}
	type wrapper interface{ Unwrap() error }
	for {
		w, ok := err.(wrapper)
		if !ok {
			return false
		}
		err = w.Unwrap()
		if err == target {
			return true
		}
	}
}

func compareWorld(t *testing.T, c *wave.Coordinator, n *naive, seq, step int, sb *strings.Builder) {
	t.Helper()
	// 每个订单：状态、缺口、全部预占明细
	for id, no := range n.ods {
		st, short, err := c.Status(id)
		if err != nil {
			t.Fatalf("seq %d step %d Status(%s): %v\n%s", seq, step, id, err, sb.String())
		}
		if st != no.st || short != no.short {
			t.Fatalf("seq %d step %d order %s impl=(%v,%d) naive=(%v,%d)\n%s",
				seq, step, id, st, short, no.st, no.short, sb.String())
		}
		gm := map[string]int64{}
		rows, _ := c.OrderAllocs(id)
		for _, a := range rows {
			gm[a.Loc] = a.Qty
		}
		nm := n.allocs(id)
		if !eqMap(gm, nm) {
			t.Fatalf("seq %d step %d order %s allocs impl=%v naive=%v\n%s",
				seq, step, id, gm, nm, sb.String())
		}
	}
	// 每个库位：onHand、reserved、locked
	for lid, nl := range n.lcs {
		cl, ok := c.LocView(lid)
		if !ok {
			t.Fatalf("seq %d loc %s missing impl\n%s", seq, lid, sb.String())
		}
		var nres int64
		for _, q := range nl.res {
			nres += q
		}
		if cl.OnHand != nl.on || cl.Reserved != nres || cl.Locked != nl.lk {
			t.Fatalf("seq %d step %d loc %s impl=(on=%d,res=%d,lk=%v) naive=(on=%d,res=%d,lk=%v)\n%s",
				seq, step, lid, cl.OnHand, cl.Reserved, cl.Locked, nl.on, nres, nl.lk, sb.String())
		}
	}
}

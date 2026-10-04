package adjust

import (
	"errors"
	"fmt"
	"math/rand"
	"sort"
	"testing"
)

// naive 是完全独立于产品代码的逐步朴素模拟器。
type naive struct {
	day     int64
	cash    map[string]int64
	froz    map[string]int64
	q       map[string]int64
	f       map[string]int64
	closeP  map[string]int64
	orders  map[string]*norder
	actions map[string]*naction
}

type norder struct {
	sym  string
	side Side
	px   int64
	qty  int64
}

type naction struct {
	sym      string
	c, b     int64
	rec, ex  int64
	state    int // 0 announced 1 snapshotted 2 executed 3 canceled
	snap     map[string][2]int64
	pex      int64
	awards   map[string][5]int64 // shares, sfroz, cash, cfroz, frag
	orderAdj map[string][2]int64 // px, canceled
}

func newNaive() *naive {
	return &naive{
		cash:    map[string]int64{},
		froz:    map[string]int64{},
		q:       map[string]int64{},
		f:       map[string]int64{},
		closeP:  map[string]int64{},
		orders:  map[string]*norder{},
		actions: map[string]*naction{},
	}
}

func posKey(acct, sym string) string { return acct + "\x00" + sym }

func (n *naive) pexCalc(p, c, b int64) int64 {
	num := 10*p - c
	den := 10 + b
	v := (2*num + den) / (2 * den)
	if v < 1 {
		return 1
	}
	return v
}

func (n *naive) process(day int64) {
	var ids []string
	for id, a := range n.actions {
		if a.state == 0 && a.rec < day {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	for _, id := range ids {
		a := n.actions[id]
		a.snap = map[string][2]int64{}
		for k, qq := range n.q {
			if qq > 0 {
				var acct, sym string
				for i := 0; i < len(k); i++ {
					if k[i] == 0 {
						acct = k[:i]
						sym = k[i+1:]
						break
					}
				}
				if sym == a.sym {
					a.snap[acct] = [2]int64{qq, n.f[k]}
				}
			}
		}
		a.state = 1
	}
	ids = ids[:0]
	for id, a := range n.actions {
		if a.state == 1 && a.ex <= day {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	for _, id := range ids {
		n.execute(id)
	}
}

func (n *naive) execute(id string) {
	a := n.actions[id]
	p := n.closeP[a.sym]
	pex := n.pexCalc(p, a.c, a.b)
	var accts []string
	for acct := range a.snap {
		accts = append(accts, acct)
	}
	sort.Strings(accts)
	a.awards = map[string][5]int64{}
	for _, acct := range accts {
		sf := a.snap[acct]
		qq, ff := sf[0], sf[1]
		if qq <= 0 {
			continue
		}
		sh := qq * a.b / 10
		sfroz := ff * a.b / 10
		cash := qq * a.c / 10
		cfroz := ff * a.c / 10
		frag := (qq * a.b) % 10 * pex / 10
		k := posKey(acct, a.sym)
		n.q[k] += sh
		n.f[k] += sfroz
		n.cash[acct] += cash - cfroz + frag
		n.froz[acct] += cfroz
		a.awards[acct] = [5]int64{sh, sfroz, cash, cfroz, frag}
	}
	var oids []string
	for oid, o := range n.orders {
		if o.sym == a.sym {
			oids = append(oids, oid)
		}
	}
	sort.Strings(oids)
	a.orderAdj = map[string][2]int64{}
	for _, oid := range oids {
		o := n.orders[oid]
		num := o.px * pex
		var np int64
		if o.side == Buy {
			np = num / p
		} else {
			np = (num + p - 1) / p
		}
		if o.side == Buy && np < 1 {
			delete(n.orders, oid)
			a.orderAdj[oid] = [2]int64{0, 1}
			continue
		}
		if np < 1 {
			np = 1
		}
		o.px = np
		a.orderAdj[oid] = [2]int64{np, 0}
	}
	n.closeP[a.sym] = pex
	a.pex = pex
	a.state = 2
}

// ---- 操作的统一表示：引擎与模型消费同一条输入 ----

type inOp struct {
	kind           string
	s1, s2, s3     string
	i1, i2, i3, i4 int64
	side           Side
}

func (o inOp) String() string {
	switch o.kind {
	case "adv":
		return fmtOp("Advance(%d)", o.i1)
	case "dep":
		return fmtOp("Deposit(%q,%d)", o.s1, o.i1)
	case "trade":
		return fmtOp("Trade(%q,%q,%d)", o.s1, o.s2, o.i1)
	case "freeze":
		return fmtOp("Freeze(%q,%q,%d)", o.s1, o.s2, o.i1)
	case "unfreeze":
		return fmtOp("Unfreeze(%q,%q,%d)", o.s1, o.s2, o.i1)
	case "close":
		return fmtOp("SetClose(%q,%d)", o.s1, o.i1)
	case "order":
		return fmtOp("PlaceOrder(%q,%q,%q,%s,%d,%d)", o.s1, o.s2, o.s3, sideName(o.side), o.i1, o.i2)
	case "cancelord":
		return fmtOp("CancelOrder(%q)", o.s1)
	case "ann":
		return fmtOp("Announce(%q,%q,c=%d,b=%d,rec=%d,ex=%d)", o.s1, o.s2, o.i1, o.i2, o.i3, o.i4)
	case "cancelact":
		return fmtOp("CancelAction(%q)", o.s1)
	}
	return o.kind
}

func sideName(s Side) string {
	if s == Buy {
		return "BUY"
	}
	return "SELL"
}

func fmtOp(format string, args ...any) string {
	return sprintf(format, args...)
}

func sprintf(format string, args ...any) string {
	return fmt.Sprintf(format, args...)
}

func (n *naive) apply(o inOp) error {
	switch o.kind {
	case "adv":
		if o.i1 < 0 || o.i1 > 1_000_000 {
			return ErrInvalidParam
		}
		if o.i1 < n.day {
			return ErrDateRollback
		}
		if o.i1 > n.day {
			n.day = o.i1
			n.process(n.day)
		}
	case "dep":
		n.cash[o.s1] += o.i1
	case "trade":
		k := posKey(o.s1, o.s2)
		if o.i1 < 0 {
			if _, ok := n.cash[o.s1]; !ok {
				return ErrNotFound
			}
			if n.q[k]-n.f[k] < -o.i1 {
				return ErrInsufficient
			}
		} else {
			if _, ok := n.cash[o.s1]; !ok {
				n.cash[o.s1] = 0
				n.froz[o.s1] = 0
			}
		}
		n.q[k] += o.i1
	case "freeze":
		if _, ok := n.cash[o.s1]; !ok {
			return ErrNotFound
		}
		k := posKey(o.s1, o.s2)
		if n.q[k]-n.f[k] < o.i1 {
			return ErrInsufficient
		}
		n.f[k] += o.i1
	case "unfreeze":
		if _, ok := n.cash[o.s1]; !ok {
			return ErrNotFound
		}
		k := posKey(o.s1, o.s2)
		if n.f[k] < o.i1 {
			return ErrInsufficient
		}
		n.f[k] -= o.i1
	case "close":
		n.closeP[o.s1] = o.i1
	case "order":
		if _, ok := n.orders[o.s1]; ok {
			return ErrDuplicateID
		}
		n.orders[o.s1] = &norder{sym: o.s3, side: o.side, px: o.i1, qty: o.i2}
	case "cancelord":
		if _, ok := n.orders[o.s1]; !ok {
			return ErrNotFound
		}
		delete(n.orders, o.s1)
	case "ann":
		if _, ok := n.actions[o.s1]; ok {
			return ErrDuplicateID
		}
		if o.i3 > n.day {
			return ErrBadState
		}
		for _, a := range n.actions {
			if a.sym == o.s2 && a.state != 2 && a.state != 3 {
				return ErrConflict
			}
		}
		if _, ok := n.closeP[o.s2]; !ok {
			return ErrNoRefPrice
		}
		n.actions[o.s1] = &naction{
			sym: o.s2, c: o.i1, b: o.i2, rec: o.i3, ex: o.i4,
		}
	case "cancelact":
		a, ok := n.actions[o.s1]
		if !ok {
			return ErrNotFound
		}
		if a.state != 0 {
			return ErrBadState
		}
		a.state = 3
	}
	return nil
}

// ---- 随机序列生成与对拍 ----

const diffIterations = 1500

func genOps(rng *rand.Rand) []inOp {
	var ops []inOp
	day := int64(0)
	accts := []string{"A", "B", "C"}
	syms := []string{"S", "T"}
	n := 12 + rng.Intn(40)
	annCount := 0
	ordSeq := 0
	for i := 0; i < n; i++ {
		switch rng.Intn(11) {
		case 0:
			day += int64(rng.Intn(4))
			if day > 1_000_000 {
				day = 1_000_000
			}
			ops = append(ops, inOp{kind: "adv", i1: day})
		case 1:
			ops = append(ops, inOp{kind: "dep", s1: accts[rng.Intn(len(accts))], i1: int64(1 + rng.Intn(100000))})
		case 2, 3:
			delta := int64(1 + rng.Intn(50))
			if rng.Intn(2) == 0 {
				delta = -delta
			}
			ops = append(ops, inOp{kind: "trade", s1: accts[rng.Intn(len(accts))], s2: syms[rng.Intn(len(syms))], i1: delta})
		case 4:
			ops = append(ops, inOp{kind: "freeze", s1: accts[rng.Intn(len(accts))], s2: syms[rng.Intn(len(syms))], i1: int64(1 + rng.Intn(20))})
		case 5:
			ops = append(ops, inOp{kind: "unfreeze", s1: accts[rng.Intn(len(accts))], s2: syms[rng.Intn(len(syms))], i1: int64(1 + rng.Intn(20))})
		case 6:
			ops = append(ops, inOp{kind: "close", s1: syms[rng.Intn(len(syms))], i1: int64(1 + rng.Intn(2000))})
		case 7:
			ordSeq++
			side := Buy
			if rng.Intn(2) == 0 {
				side = Sell
			}
			ops = append(ops, inOp{kind: "order",
				s1: fmt.Sprintf("o%d", ordSeq), s2: accts[rng.Intn(len(accts))], s3: syms[rng.Intn(len(syms))],
				side: side, i1: int64(1 + rng.Intn(2000)), i2: int64(1 + rng.Intn(100))})
		case 8:
			if ordSeq > 0 {
				ops = append(ops, inOp{kind: "cancelord", s1: fmt.Sprintf("o%d", 1+rng.Intn(ordSeq))})
			}
		case 9:
			if annCount < 6 && day >= 0 {
				rec := day + int64(rng.Intn(4))
				ex := rec + 1 + int64(rng.Intn(3))
				c := int64(rng.Intn(1000))
				b := int64(rng.Intn(11))
				if c == 0 && b == 0 {
					b = 1
				}
				annCount++
				ops = append(ops, inOp{kind: "ann",
					s1: fmt.Sprintf("act%d", annCount), s2: syms[rng.Intn(len(syms))],
					i1: c, i2: b, i3: rec, i4: ex})
			}
		case 10:
			if annCount > 0 {
				ops = append(ops, inOp{kind: "cancelact", s1: fmt.Sprintf("act%d", 1+rng.Intn(annCount))})
			}
		}
	}
	day = 1_000_000
	ops = append(ops, inOp{kind: "adv", i1: day})
	return ops
}

func errCode(err error) string {
	switch {
	case err == nil:
		return "ok"
	case errors.Is(err, ErrInvalidParam):
		return "invalid"
	case errors.Is(err, ErrDateRollback):
		return "rollback"
	case errors.Is(err, ErrDuplicateID):
		return "dup"
	case errors.Is(err, ErrNotFound):
		return "notfound"
	case errors.Is(err, ErrBadState):
		return "state"
	case errors.Is(err, ErrConflict):
		return "conflict"
	case errors.Is(err, ErrNoRefPrice):
		return "noref"
	case errors.Is(err, ErrInsufficient):
		return "insuff"
	default:
		return "other:" + err.Error()
	}
}

func (e *Engine) applyIn(o inOp) error {
	switch o.kind {
	case "adv":
		return e.Advance(o.i1)
	case "dep":
		return e.Deposit([]byte(o.s1), o.i1)
	case "trade":
		return e.Trade([]byte(o.s1), []byte(o.s2), o.i1)
	case "freeze":
		return e.Freeze([]byte(o.s1), []byte(o.s2), o.i1)
	case "unfreeze":
		return e.Unfreeze([]byte(o.s1), []byte(o.s2), o.i1)
	case "close":
		return e.SetClose([]byte(o.s1), o.i1)
	case "order":
		return e.PlaceOrder([]byte(o.s1), []byte(o.s2), []byte(o.s3), o.side, o.i1, o.i2)
	case "cancelord":
		return e.CancelOrder([]byte(o.s1))
	case "ann":
		return e.Announce([]byte(o.s1), []byte(o.s2), o.i1, o.i2, o.i3, o.i4)
	case "cancelact":
		return e.CancelAction([]byte(o.s1))
	}
	return nil
}

func (n *naive) compare(t *testing.T, e *Engine, trace []string) {
	t.Helper()
	if n.day != e.day {
		dumpFail(t, trace, fmt.Sprintf("day model=%d engine=%d", n.day, e.day))
	}
	for _, acct := range []string{"A", "B", "C"} {
		mAvail, mFroz := n.cash[acct], n.froz[acct]
		_, mExist := n.cash[acct]
		gAvail, gFroz, err := e.book.Cash([]byte(acct))
		if (err == nil) != mExist {
			dumpFail(t, trace, fmt.Sprintf("acct %s existence model=%t engine=%v", acct, mExist, err))
		}
		if err == nil && mExist && (mAvail != gAvail || mFroz != gFroz) {
			dumpFail(t, trace, fmt.Sprintf("cash %s model=(%d,%d) engine=(%d,%d)", acct, mAvail, mFroz, gAvail, gFroz))
		}
		for _, sym := range []string{"S", "T"} {
			k := posKey(acct, sym)
			gq, gf, _ := e.book.Position([]byte(acct), []byte(sym))
			if n.q[k] != gq || n.f[k] != gf {
				dumpFail(t, trace, fmt.Sprintf("pos %s/%s model=(%d,%d) engine=(%d,%d)", acct, sym, n.q[k], n.f[k], gq, gf))
			}
		}
	}
	for _, sym := range []string{"S", "T"} {
		mp, mok := n.closeP[sym]
		gp, gok := e.book.Close([]byte(sym))
		if mok != gok || (mok && mp != gp) {
			dumpFail(t, trace, fmt.Sprintf("close %s model=(%d,%t) engine=(%d,%t)", sym, mp, mok, gp, gok))
		}
	}
	// 委托簿
	if len(n.orders) != len(e.ords) {
		dumpFail(t, trace, fmt.Sprintf("order count model=%d engine=%d", len(n.orders), len(e.ords)))
	}
	for oid, mo := range n.orders {
		go2, ok := e.ords[oid]
		if !ok {
			dumpFail(t, trace, fmt.Sprintf("order %s missing in engine", oid))
		}
		if mo.px != go2.price || mo.qty != go2.qty || mo.side != go2.side {
			dumpFail(t, trace, fmt.Sprintf("order %s model=(%d,%d,%d) engine=(%d,%d,%d)",
				oid, mo.px, mo.qty, mo.side, go2.price, go2.qty, go2.side))
		}
	}
	// 行动与结果
	if len(n.actions) != e.reg.ActionsCount() {
		dumpFail(t, trace, fmt.Sprintf("action count model=%d engine=%d", len(n.actions), e.reg.ActionsCount()))
	}
	for id, ma := range n.actions {
		ga, err := e.reg.Get([]byte(id))
		if err != nil {
			dumpFail(t, trace, fmt.Sprintf("action %s missing in engine", id))
		}
		modelState := []int{0, 1, 2, 3}[ga.State]
		_ = modelState
		if modelState != ma.state {
			dumpFail(t, trace, fmt.Sprintf("action %s state model=%d engine=%d", id, ma.state, modelState))
		}
		if ma.state == 2 {
			if ga.Result == nil {
				dumpFail(t, trace, fmt.Sprintf("action %s engine missing result", id))
			}
			if ga.Result.Pex != ma.pex {
				dumpFail(t, trace, fmt.Sprintf("action %s pex model=%d engine=%d", id, ma.pex, ga.Result.Pex))
			}
			if len(ga.Result.Awards) != len(ma.awards) {
				dumpFail(t, trace, fmt.Sprintf("action %s awards len model=%d engine=%d", id, len(ma.awards), len(ga.Result.Awards)))
			}
			for _, aw := range ga.Result.Awards {
				mw, ok := ma.awards[string(aw.Acct)]
				if !ok {
					dumpFail(t, trace, fmt.Sprintf("action %s unexpected award %s", id, aw.Acct))
				}
				gw := [5]int64{aw.Shares, aw.SharesFroz, aw.Cash, aw.CashFroz, aw.FragCash}
				if gw != mw {
					dumpFail(t, trace, fmt.Sprintf("action %s award %s model=%v engine=%v", id, aw.Acct, mw, gw))
				}
			}
			if len(ga.Result.Orders) != len(ma.orderAdj) {
				dumpFail(t, trace, fmt.Sprintf("action %s orderAdj len model=%d engine=%d", id, len(ma.orderAdj), len(ga.Result.Orders)))
			}
			for _, oa := range ga.Result.Orders {
				mv, ok := ma.orderAdj[string(oa.OID)]
				if !ok {
					dumpFail(t, trace, fmt.Sprintf("action %s unexpected orderAdj %s", id, oa.OID))
				}
				canceled := int64(0)
				if oa.Canceled {
					canceled = 1
				}
				if mv[0] != oa.NewPrice || mv[1] != canceled {
					dumpFail(t, trace, fmt.Sprintf("action %s orderAdj %s model=%v engine=(%d,%d)", id, oa.OID, mv, oa.NewPrice, canceled))
				}
			}
		}
	}
}

func dumpFail(t *testing.T, trace []string, reason string) {
	t.Helper()
	for i, line := range trace {
		t.Logf("  %3d: %s", i, line)
	}
	t.Fatalf("判定依据: %s", reason)
}

func TestRandomDifferential(t *testing.T) {
	if testing.Short() {
		t.Skip()
	}
	for iter := 0; iter < diffIterations; iter++ {
		seed := int64(1000 + iter)
		rng := rand.New(rand.NewSource(seed))
		ops := genOps(rng)
		e := New()
		n := newNaive()
		trace := make([]string, 0, len(ops))
		for _, o := range ops {
			gErr := e.applyIn(o)
			mErr := n.apply(o)
			gc, mc := errCode(gErr), errCode(mErr)
			trace = append(trace, fmt.Sprintf("%-44s -> engine=%-9s model=%-9s", o.String(), gc, mc))
			if gc != mc {
				t.Logf("=== seed=%d 输入/输出对照（首个不一致即判定失败）===", seed)
				dumpFail(t, trace, fmt.Sprintf("拒绝码不一致: %s", o.String()))
			}
			n.compare(t, e, trace)
		}
		if iter < 3 || iter == diffIterations-1 {
			t.Logf("=== seed=%d 序列 %d 条，逐条输入/输出/判定一致 ===", seed, len(ops))
			for i, line := range trace {
				t.Logf("  %3d: %s", i, line)
			}
		}
	}
}

// TestReplayDeterminism 同一随机序列重放两次，逐条输出与终态必须完全一致。
func TestReplayDeterminism(t *testing.T) {
	ops := genOps(rand.New(rand.NewSource(42)))
	run := func() []string {
		e := New()
		n := newNaive()
		out := make([]string, 0, len(ops))
		for _, o := range ops {
			gc := errCode(e.applyIn(o))
			mc := errCode(n.apply(o))
			if gc != mc {
				t.Fatalf("internal mismatch: %s", o)
			}
			out = append(out, o.String()+"=>"+gc)
		}
		for id := range n.actions {
			ga, err := e.reg.Get([]byte(id))
			if err != nil || ga.State == 2 && ga.Result != nil {
				out = append(out, fmt.Sprintf("%s:pex=%d:awards=%d", id, ga.Result.Pex, len(ga.Result.Awards)))
			}
		}
		return out
	}
	r1 := run()
	r2 := run()
	if len(r1) != len(r2) {
		t.Fatalf("replay length differs %d vs %d", len(r1), len(r2))
	}
	for i := range r1 {
		if r1[i] != r2[i] {
			t.Fatalf("replay differs at %d:\n%s\n%s", i, r1[i], r2[i])
		}
	}
}

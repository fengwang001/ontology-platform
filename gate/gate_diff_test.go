package gate

import (
	"fmt"
	"math/rand"
	"sort"
	"testing"
)

// naive 为朴素模拟：每次需要判定时，从全部委托与持仓重新汇总。
type naive struct {
	now       int64
	groups    map[string]string
	lims      map[string][3]int64
	hedges    map[string][2]int64 // acct|sym -> [long,short]
	ords      map[string]*norder
	dayFilled map[string]int64 // acct|sym -> 已成交开仓（ResetDay 清零）
	oidSeq    int
	log       []string
}

type norder struct {
	acct, sym string
	side      Side
	off       Offset
	qty       int64
	filled    int64
	canceled  bool
}

func newNaive() *naive {
	return &naive{
		groups: map[string]string{}, lims: map[string][3]int64{},
		hedges: map[string][2]int64{}, ords: map[string]*norder{},
		dayFilled: map[string]int64{},
	}
}

func (n *naive) key(a, s string) string { return a + "|" + s }

// recalc 从全部存活委托与成交记录重算单账户单合约状态。
func (n *naive) recalc(a, s string) (pos [2]int64, open [2]int64, pclose [2]int64, dayFilled int64) {
	dayFilled = n.dayFilled[n.key(a, s)]
	for _, o := range n.ords {
		if o.acct != a || o.sym != s {
			continue
		}
		i := 0
		if o.side == Short {
			i = 1
		}
		// 已撤委托保留已成交部分、未成交部分归零；未撤委托未成交=全部残余。
		rem := o.qty - o.filled
		if o.canceled {
			rem = 0
		}
		if o.off == Open {
			pos[i] += o.filled
			open[i] += rem
		} else {
			pos[i] -= o.filled
			pclose[i] += rem
		}
	}
	return
}

func (n *naive) groupExp(grp, s string) int64 {
	var total int64
	for a, g := range n.groups {
		if g != grp {
			continue
		}
		pos, op, _, _ := n.recalc(a, s)
		h := n.hedges[n.key(a, s)]
		for i := 0; i < 2; i++ {
			e := pos[i] + op[i]
			if e-h[i] > 0 {
				total += e - h[i]
			}
		}
	}
	return total
}

func (n *naive) register(now int64, a, g string) error {
	if err := baseCheck(now, a, g); err != nil {
		return err
	}
	if now < n.now {
		return ErrClock
	}
	if _, ok := n.groups[a]; ok {
		return ErrDuplicate
	}
	n.groups[a] = g
	n.now = now
	return nil
}

func baseCheck(now int64, ids ...string) error {
	if !validNow(now) {
		return ErrInvalid
	}
	for _, id := range ids {
		if id == "" {
			return ErrInvalid
		}
	}
	return nil
}

func (n *naive) setLimit(now int64, s string, la, lg, d int64) error {
	if !validNow(now) || s == "" || !validCap(la) || !validCap(lg) || !validCap(d) {
		return ErrInvalid
	}
	if now < n.now {
		return ErrClock
	}
	n.lims[s] = [3]int64{la, lg, d}
	n.now = now
	return nil
}

func (n *naive) setHedge(now int64, a, s string, side Side, hh int64) error {
	if !validNow(now) || a == "" || s == "" || !validSide(side) || !validCap(hh) {
		return ErrInvalid
	}
	if now < n.now {
		return ErrClock
	}
	if _, ok := n.groups[a]; !ok {
		return ErrNotFound
	}
	if _, ok := n.lims[s]; !ok {
		return ErrNotFound
	}
	h := n.hedges[n.key(a, s)]
	h[sideIdxOf(side)] = hh
	n.hedges[n.key(a, s)] = h
	n.now = now
	return nil
}

func sideIdxOf(s Side) int {
	if s == Short {
		return 1
	}
	return 0
}

func (n *naive) order(now int64, o, a, s string, side Side, off Offset, qty int64) error {
	if !validNow(now) || o == "" || a == "" || s == "" || !validSide(side) || !validOffset(off) || !validQty(qty) {
		return ErrInvalid
	}
	if now < n.now {
		return ErrClock
	}
	grp, ok := n.groups[a]
	if !ok {
		return ErrNotFound
	}
	lim, ok := n.lims[s]
	if !ok {
		return ErrNotFound
	}
	if old, dup := n.ords[o]; dup {
		if old.canceled || old.filled == old.qty {
			return ErrState
		}
		return ErrDuplicateOID
	}
	pos, op, pcl, dayFilled := n.recalc(a, s)
	i := sideIdxOf(side)
	if off == Close {
		if qty > pos[i]-pcl[i] {
			return ErrCloseQty
		}
	} else {
		e := pos[i] + op[i]
		h := n.hedges[n.key(a, s)][i]
		if e+qty > lim[0]+h {
			return ErrAcctLimit
		}
		oldC := int64(0)
		if c := pos[i] + op[i] - h; c > 0 {
			oldC = c
		}
		newC := e + qty - h
		if newC < 0 {
			newC = 0
		}
		if n.groupExp(grp, s)-oldC+newC > lim[1] {
			return ErrGroupLimit
		}
		dayOpen := dayFilled + op[0] + op[1]
		if dayOpen+qty > lim[2] {
			return ErrDayLimit
		}
	}
	n.ords[o] = &norder{acct: a, sym: s, side: side, off: off, qty: qty}
	n.now = now
	return nil
}

func (n *naive) fill(now int64, o string, qty int64) error {
	if !validNow(now) || o == "" || !validQty(qty) {
		return ErrInvalid
	}
	if now < n.now {
		return ErrClock
	}
	od, ok := n.ords[o]
	if !ok {
		return ErrNotFound
	}
	if od.canceled || od.filled == od.qty {
		return ErrState
	}
	if qty > od.qty-od.filled {
		return ErrState
	}
	od.filled += qty
	if od.off == Open {
		n.dayFilled[n.key(od.acct, od.sym)] += qty
	}
	n.now = now
	return nil
}

func (n *naive) cancel(now int64, o string) error {
	if !validNow(now) || o == "" {
		return ErrInvalid
	}
	if now < n.now {
		return ErrClock
	}
	od, ok := n.ords[o]
	if !ok {
		return ErrNotFound
	}
	if od.canceled || od.filled == od.qty {
		return ErrState
	}
	od.canceled = true
	n.now = now
	return nil
}

func (n *naive) resetDay(now int64) error {
	if !validNow(now) {
		return ErrInvalid
	}
	if now < n.now {
		return ErrClock
	}
	n.dayFilled = map[string]int64{}
	n.now = now
	return nil
}

type opKind int

const (
	kRegister opKind = iota
	kSetLimit
	kSetHedge
	kOrder
	kFill
	kCancel
	kResetDay
)

type op struct {
	kind          opKind
	now           int64
	id1, id2, id3 string
	side          Side
	off           Offset
	qty           int64
	la, lg, d, hh int64
}

func (o op) String() string {
	switch o.kind {
	case kRegister:
		return fmt.Sprintf("Register(now=%d acct=%q group=%q)", o.now, o.id1, o.id2)
	case kSetLimit:
		return fmt.Sprintf("SetLimit(now=%d sym=%q La=%d Lg=%d D=%d)", o.now, o.id1, o.la, o.lg, o.d)
	case kSetHedge:
		return fmt.Sprintf("SetHedge(now=%d acct=%q sym=%q side=%v H=%d)", o.now, o.id1, o.id2, o.side, o.hh)
	case kOrder:
		return fmt.Sprintf("Order(now=%d oid=%q acct=%q sym=%q side=%v off=%v qty=%d)",
			o.now, o.id1, o.id2, o.id3, o.side, o.off, o.qty)
	case kFill:
		return fmt.Sprintf("Fill(now=%d oid=%q qty=%d)", o.now, o.id1, o.qty)
	case kCancel:
		return fmt.Sprintf("Cancel(now=%d oid=%q)", o.now, o.id1)
	default:
		return fmt.Sprintf("ResetDay(now=%d)", o.now)
	}
}

func runOp(g *Gateway, nv *naive, o op) (error, error) {
	switch o.kind {
	case kRegister:
		return g.Register(o.now, []byte(o.id1), []byte(o.id2)), nv.register(o.now, o.id1, o.id2)
	case kSetLimit:
		g.SetLimit(o.now, []byte(o.id1), o.la, o.lg, o.d)
		return nil, nv.setLimit(o.now, o.id1, o.la, o.lg, o.d)
	case kSetHedge:
		return g.SetHedge(o.now, []byte(o.id1), []byte(o.id2), o.side, o.hh),
			nv.setHedge(o.now, o.id1, o.id2, o.side, o.hh)
	case kOrder:
		return g.Order(o.now, []byte(o.id1), []byte(o.id2), []byte(o.id3), o.side, o.off, o.qty),
			nv.order(o.now, o.id1, o.id2, o.id3, o.side, o.off, o.qty)
	case kFill:
		return g.Fill(o.now, []byte(o.id1), o.qty), nv.fill(o.now, o.id1, o.qty)
	case kCancel:
		return g.Cancel(o.now, []byte(o.id1)), nv.cancel(o.now, o.id1)
	default:
		g.ResetDay(o.now)
		return nil, nv.resetDay(o.now)
	}
}

func errName(err error) string {
	if err == nil {
		return "OK"
	}
	return err.Error()
}

func sameErr(a, b error) bool {
	return (a == nil) == (b == nil) && (a == nil || a.Error() == b.Error())
}

// snapshot 为朴素模型对所有 (账户,合约,边) 与组敞口的全量重算结果。
type snapshot struct {
	recs map[string][2]recSnap // acct|sym -> 两边
	gexp map[string]int64      // group|sym
	day  map[string]int64
}

type recSnap struct {
	pos, open, pclose, h, contrib int64
}

func takeNaiveSnap(nv *naive) snapshot {
	s := snapshot{recs: map[string][2]recSnap{}, gexp: map[string]int64{}, day: map[string]int64{}}
	keys := map[string]bool{}
	for k := range nv.hedges {
		keys[k] = true
	}
	for _, od := range nv.ords {
		keys[nv.key(od.acct, od.sym)] = true
	}
	for k := range nv.dayFilled {
		keys[k] = true
	}
	ks := make([]string, 0, len(keys))
	for k := range keys {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	for _, k := range ks {
		a, sym, _ := splitKey(k)
		pos, op, pcl, _ := nv.recalc(a, sym)
		var recs [2]recSnap
		for i := 0; i < 2; i++ {
			h := nv.hedges[k][i]
			e := pos[i] + op[i]
			c := e - h
			if c < 0 {
				c = 0
			}
			recs[i] = recSnap{pos[i], op[i], pcl[i], h, c}
		}
		s.recs[k] = recs
		s.day[k] = nv.dayFilled[k] + op[0] + op[1]
	}
	grps := map[string]bool{}
	for _, grp := range nv.groups {
		grps[grp] = true
	}
	syms := map[string]bool{}
	for sym := range nv.lims {
		syms[sym] = true
	}
	for grp := range grps {
		for sym := range syms {
			s.gexp[grp+"|"+sym] = nv.groupExp(grp, sym)
		}
	}
	return s
}

func splitKey(k string) (string, string, bool) {
	for i := 0; i < len(k); i++ {
		if k[i] == '|' {
			return k[:i], k[i+1:], true
		}
	}
	return "", "", false
}

func takeGateSnap(g *Gateway) snapshot {
	s := snapshot{recs: map[string][2]recSnap{}, gexp: map[string]int64{}, day: map[string]int64{}}
	g.mu.RLock()
	defer g.mu.RUnlock()
	for _, e := range g.book.AllRecs() {
		r := e.Rec
		s.recs[string(e.Acct)+"|"+string(e.Sym)] = [2]recSnap{
			{r.Pos[0], r.Open[0], r.Close[0], r.Hedge[0], r.Contrib[0]},
			{r.Pos[1], r.Open[1], r.Close[1], r.Hedge[1], r.Contrib[1]},
		}
		s.day[string(e.Acct)+"|"+string(e.Sym)] = r.DayFilled + r.Open[0] + r.Open[1]
	}
	for _, e := range g.book.AllGroupExps() {
		s.gexp[string(e.Group)+"|"+string(e.Sym)] = e.Exp
	}
	return s
}

func snapEqual(a, b snapshot) (string, bool) {
	for k, av := range a.recs {
		bv, ok := b.recs[k]
		if !ok || av[0] != bv[0] || av[1] != bv[1] {
			return fmt.Sprintf("rec %s: naive=%+v gate=%+v", k, av, bv), false
		}
	}
	for k := range b.recs {
		if _, ok := a.recs[k]; !ok {
			return fmt.Sprintf("rec %s missing in naive", k), false
		}
	}
	ag, bg := a.gexp, b.gexp
	for k := range ag {
		if ag[k] != bg[k] {
			return fmt.Sprintf("gexp %q: naive=%d gate=%d", k, ag[k], bg[k]), false
		}
	}
	for k := range bg {
		if _, ok := ag[k]; !ok && bg[k] != 0 {
			return fmt.Sprintf("gexp %q: naive=0 gate=%d", k, bg[k]), false
		}
	}
	for k, av := range a.day {
		if bv, ok := b.day[k]; !ok || av != bv {
			return fmt.Sprintf("day %s: naive=%d gate=%d", k, av, b.day[k]), false
		}
	}
	return "", true
}

func (n *naive) basis(a, s string, side Side, off Offset, qty int64) string {
	lim, ok := n.lims[s]
	if !ok {
		return "no limit"
	}
	grp, hasAcct := n.groups[a]
	if !hasAcct {
		return "acct not registered"
	}
	pos, op, pcl, df := n.recalc(a, s)
	i := sideIdxOf(side)
	e := pos[i] + op[i]
	h := n.hedges[n.key(a, s)][i]
	dayOpen := df + op[0] + op[1]
	if off == Close {
		return fmt.Sprintf("close: pos=%d pendingClose=%d closeable=%d qty=%d",
			pos[i], pcl[i], pos[i]-pcl[i], qty)
	}
	return fmt.Sprintf("open: e=%d qty=%d La+H=%d | g=%d (acctCap=%v,groupCap=%v,dayCap=%v)",
		e, qty, lim[0]+h, n.groupExp(grp, s),
		e+qty <= lim[0]+h,
		n.groupExp(grp, s)-maxz(e-h)+maxz(e+qty-h) <= lim[1],
		dayOpen+qty <= lim[2])
}

func maxz(x int64) int64 {
	if x > 0 {
		return x
	}
	return 0
}

func genOps(rng *rand.Rand) []op {
	var ops []op
	clock := int64(1)
	accounts := []string{"A0", "A1", "A2", "A3"}
	syms := []string{"S0", "S1"}
	// 固定骨架：全部登记，两个合约设置随机限额。
	ops = append(ops, op{kind: kRegister, now: clock, id1: accounts[0], id2: "G"})
	clock++
	for i := 1; i < len(accounts); i++ {
		ops = append(ops, op{kind: kRegister, now: clock, id1: accounts[i], id2: "G"})
		clock++
	}
	for _, s := range syms {
		ops = append(ops, op{kind: kSetLimit, now: clock, id1: s,
			la: rng.Int63n(120), lg: rng.Int63n(200), d: rng.Int63n(300)})
		clock++
	}
	var oids []string
	oidSeq := 0
	for step := 0; step < 60; step++ {
		if rng.Intn(20) == 0 {
			clock += rng.Int63n(2) // 偶发同刻或跳刻
		}
		a := accounts[rng.Intn(len(accounts))]
		s := syms[rng.Intn(len(syms))]
		r := rng.Intn(100)
		switch {
		case r < 46: // Order
			side := Long
			if rng.Intn(2) == 1 {
				side = Short
			}
			off := Open
			if rng.Intn(3) == 0 {
				off = Close
			}
			oid := "o" + itoa(oidSeq)
			oidSeq++
			// 10% 复用历史 oid 触发重复/状态不符。
			if len(oids) > 0 && rng.Intn(10) == 0 {
				oid = oids[rng.Intn(len(oids))]
			} else {
				oids = append(oids, oid)
			}
			ops = append(ops, op{kind: kOrder, now: clock, id1: oid, id2: a, id3: s,
				side: side, off: off, qty: 1 + rng.Int63n(80)})
		case r < 66 && len(oids) > 0: // Fill
			ops = append(ops, op{kind: kFill, now: clock, id1: oids[rng.Intn(len(oids))],
				qty: 1 + rng.Int63n(90)})
		case r < 82 && len(oids) > 0: // Cancel
			ops = append(ops, op{kind: kCancel, now: clock, id1: oids[rng.Intn(len(oids))]})
		case r < 90: // SetHedge
			side := Long
			if rng.Intn(2) == 1 {
				side = Short
			}
			ops = append(ops, op{kind: kSetHedge, now: clock, id1: a, id2: s,
				side: side, hh: rng.Int63n(100)})
		case r < 96: // SetLimit（含调低）
			ops = append(ops, op{kind: kSetLimit, now: clock, id1: s,
				la: rng.Int63n(120), lg: rng.Int63n(200), d: rng.Int63n(300)})
		default: // ResetDay
			ops = append(ops, op{kind: kResetDay, now: clock})
		}
		// 少量畸形输入，验证非法参数判定一致。
		if rng.Intn(25) == 0 {
			ops[len(ops)-1].now = -1
		}
		clock++
	}
	return ops
}

func executeSeq(ops []op) (*Gateway, *naive, []string) {
	g := New()
	nv := newNaive()
	logs := make([]string, 0, len(ops))
	for _, o := range ops {
		basis := ""
		if o.kind == kOrder {
			basis = " | basis: " + nv.basis(o.id2, o.id3, o.side, o.off, o.qty)
		}
		ge, ne := runOp(g, nv, o)
		logs = append(logs, fmt.Sprintf("%s => gate=%s naive=%s%s",
			o, errName(ge), errName(ne), basis))
		if !sameErr(ge, ne) {
			return g, nv, logs
		}
	}
	return g, nv, logs
}

func TestRandomDifferential(t *testing.T) {
	const groups = 1500
	for seed := int64(0); seed < groups; seed++ {
		rng := rand.New(rand.NewSource(seed))
		ops := genOps(rng)
		g, nv, logs := executeSeq(ops)
		// 重放确定性：同序列跑第二个网关，结果须完全一致。
		_, _, logs2 := executeSeq(ops)
		for i := range logs {
			if logs[i] != logs2[i] {
				t.Fatalf("seed=%d replay differs at op %d:\n%s\n%s", seed, i, logs[i], logs2[i])
			}
		}
		sn := takeNaiveSnap(nv)
		sg := takeGateSnap(g)
		if msg, ok := snapEqual(sn, sg); !ok {
			t.Logf("seed=%d divergence: %s", seed, msg)
			for i, l := range logs {
				t.Logf("op %d: %s", i, l)
			}
			t.Fatalf("seed=%d snapshot mismatch: %s", seed, msg)
		}
		for k, recs := range sg.recs {
			for i, rc := range recs {
				if rc.pos < 0 {
					t.Fatalf("seed=%d negative pos %s side=%d: %+v", seed, k, i, rc)
				}
			}
		}
		if seed == 0 {
			for i, l := range logs {
				t.Logf("seed=0 op %d: %s", i, l)
			}
		}
	}
}

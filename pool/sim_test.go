package pool_test

import (
	"errors"
	"fmt"
	"math/rand"
	"sort"
	"testing"

	"ontology/loan"
	"ontology/pool"
	"ontology/recall"
)

// 独立的“逐步朴素模拟”参考模型：全部选择都按登记序/合约序/
// (dl,id) 序用线性扫描完成，不借用 pool 的内部结构。

type nContract struct {
	id               int64
	lender, borrower string
	qty              int64
}

type nRecall struct {
	id               int64
	sym              string
	lender, borrower string
	qty, dl          int64
}

type nBuy struct{ id, qty, price, pen, amount, t int64 }

type naive struct {
	n, pen, now, nextID, nextReg int64
	price                        map[string]int64
	reg                          map[string]map[string]int64
	free                         map[string]map[string]int64
	totalLend, returned          map[string]int64
	normal                       map[string]map[int64]*nContract
	recalls                      []*nRecall
	debt                         map[string]map[string]int64
	buys                         []nBuy
}

func newNaive(n, pen int64) *naive {
	return &naive{
		n: n, pen: pen,
		price:     map[string]int64{},
		reg:       map[string]map[string]int64{},
		free:      map[string]map[string]int64{},
		totalLend: map[string]int64{},
		returned:  map[string]int64{},
		normal:    map[string]map[int64]*nContract{},
		debt:      map[string]map[string]int64{},
	}
}

func (m *naive) ensure(sym string) {
	if m.reg[sym] == nil {
		m.reg[sym] = map[string]int64{}
		m.free[sym] = map[string]int64{}
		m.normal[sym] = map[int64]*nContract{}
		m.debt[sym] = map[string]int64{}
	}
}

const (
	kNil = iota
	kInvalid
	kClock
	kNotFound
	kOver
	kNoSupply
)

type nResult struct {
	errKind            byte
	ids, qtys          []int64
	lenders            []string
	rcIDs, rcQty, rcDL []int64
	toR, toN           int64
	reg                bool
	regSeq             int64
	buys               []nBuy
}

// "_" 在测试里表示空字节串。
func badIn(now int64, who, sym string, v int64) bool {
	return now < 0 || now > 1_000_000_000_000 || who == "_" || sym == "_" ||
		sym == "" || who == "" || v < 1 || v > 1_000_000_000
}

func (m *naive) sortRecalls() {
	sort.SliceStable(m.recalls, func(i, j int) bool {
		if m.recalls[i].dl != m.recalls[j].dl {
			return m.recalls[i].dl < m.recalls[j].dl
		}
		return m.recalls[i].id < m.recalls[j].id
	})
}

func (m *naive) settle(now int64) []nBuy {
	m.now = now
	m.sortRecalls()
	var done []nBuy
	keep := m.recalls[:0]
	for _, rc := range m.recalls {
		if rc.dl > now {
			keep = append(keep, rc)
			continue
		}
		base := rc.qty * m.price[rc.sym]
		pen := (base*m.pen + 9999) / 10000
		by := nBuy{rc.id, rc.qty, m.price[rc.sym], pen, base + pen, rc.dl}
		done = append(done, by)
		m.buys = append(m.buys, by)
		m.returned[rc.sym] += rc.qty
		m.debt[rc.sym][rc.borrower] -= rc.qty
	}
	m.recalls = keep
	return done
}

func (m *naive) setPrice(now int64, sym string, price int64) nResult {
	if badIn(now, "x", sym, price) {
		return nResult{errKind: kInvalid}
	}
	if now < m.now {
		return nResult{errKind: kClock}
	}
	buys := m.settle(now)
	m.ensure(sym)
	m.price[sym] = price
	return nResult{buys: buys}
}

func (m *naive) newID() int64 { m.nextID++; return m.nextID }

func (m *naive) lend(now int64, lender, sym string, qty int64) nResult {
	if badIn(now, lender, sym, qty) {
		return nResult{errKind: kInvalid}
	}
	if now < m.now {
		return nResult{errKind: kClock}
	}
	buys := m.settle(now)
	m.ensure(sym)
	if _, ok := m.price[sym]; !ok {
		return nResult{errKind: kNotFound, buys: buys}
	}
	res := nResult{buys: buys}
	if s, ok := m.reg[sym][lender]; ok {
		res.regSeq = s
	} else {
		m.nextReg++
		m.reg[sym][lender] = m.nextReg
		res.reg, res.regSeq = true, m.nextReg
	}
	m.totalLend[sym] += qty
	m.free[sym][lender] += qty
	return res
}

func (m *naive) lendersByReg(sym, exclude string) []string {
	type pair struct {
		seq int64
		k   string
	}
	var ps []pair
	for k, f := range m.free[sym] {
		if f > 0 && k != exclude {
			ps = append(ps, pair{m.reg[sym][k], k})
		}
	}
	sort.Slice(ps, func(i, j int) bool { return ps[i].seq < ps[j].seq })
	out := make([]string, len(ps))
	for i, p := range ps {
		out[i] = p.k
	}
	return out
}

func (m *naive) totalFree(sym string) int64 {
	var s int64
	for _, f := range m.free[sym] {
		s += f
	}
	return s
}

func (m *naive) borrow(now int64, borrower, sym string, qty int64) nResult {
	if badIn(now, borrower, sym, qty) {
		return nResult{errKind: kInvalid}
	}
	if now < m.now {
		return nResult{errKind: kClock}
	}
	buys := m.settle(now)
	m.ensure(sym)
	if len(m.reg[sym]) == 0 {
		return nResult{errKind: kNotFound, buys: buys}
	}
	if m.totalFree(sym) < qty {
		return nResult{errKind: kNoSupply, buys: buys}
	}
	res := nResult{buys: buys}
	need := qty
	for _, lender := range m.lendersByReg(sym, "") {
		if need <= 0 {
			break
		}
		take := m.free[sym][lender]
		if take > need {
			take = need
		}
		id := m.newID()
		m.normal[sym][id] = &nContract{id, lender, borrower, take}
		m.free[sym][lender] -= take
		m.debt[sym][borrower] += take
		res.ids = append(res.ids, id)
		res.qtys = append(res.qtys, take)
		res.lenders = append(res.lenders, lender)
		need -= take
	}
	return res
}

type snapshot struct {
	price     int64
	free      string
	normal    string
	recalls   string
	debt      string
	totalLend int64
	returned  int64
	now       int64
	buys      string
}

func kvString(m map[string]int64) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	s := ""
	for _, k := range keys {
		s += fmt.Sprintf("%s:%d,", k, m[k])
	}
	return s
}

func buysNaive(bs []nBuy) string {
	s := ""
	for _, x := range bs {
		s += fmt.Sprintf("%d:%d:%d:%d:%d:%d,", x.id, x.qty, x.price, x.pen, x.amount, x.t)
	}
	return s
}

func buysReal(bs []recall.Buy) string {
	s := ""
	for _, x := range bs {
		s += fmt.Sprintf("%d:%d:%d:%d:%d:%d,", x.ID, x.Qty, x.Price, x.Pen, x.Amount, x.Time)
	}
	return s
}

func (m *naive) snap(sym string) snapshot {
	ids := make([]int64, 0, len(m.normal[sym]))
	for id := range m.normal[sym] {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	ns := ""
	for _, id := range ids {
		c := m.normal[sym][id]
		ns += fmt.Sprintf("%d:%s:%s:%d,", id, c.lender, c.borrower, c.qty)
	}
	m.sortRecalls()
	rs := ""
	for _, rc := range m.recalls {
		if rc.sym == sym {
			rs += fmt.Sprintf("%d:%s:%s:%d:%d,", rc.id, rc.lender, rc.borrower, rc.qty, rc.dl)
		}
	}
	var fs, ls int64
	for _, f := range m.free[sym] {
		fs += f
	}
	for _, c := range m.normal[sym] {
		ls += c.qty
	}
	for _, rc := range m.recalls {
		if rc.sym == sym {
			ls += rc.qty
		}
	}
	if m.totalLend[sym] != fs+ls+m.returned[sym] {
		panic("naive invariant broken")
	}
	freeCopy := map[string]int64{}
	for k, v := range m.free[sym] {
		if v != 0 {
			freeCopy[k] = v
		}
	}
	debtCopy := map[string]int64{}
	for k, v := range m.debt[sym] {
		if v != 0 {
			debtCopy[k] = v
		}
	}
	return snapshot{
		price:     m.price[sym],
		free:      kvString(freeCopy),
		normal:    ns,
		recalls:   rs,
		debt:      kvString(debtCopy),
		totalLend: m.totalLend[sym],
		returned:  m.returned[sym],
		now:       m.now,
		buys:      buysNaive(m.buys),
	}
}

func realSnap(p *pool.Pool, sym string, names map[string]bool) snapshot {
	freeMap := map[string]int64{}
	debtMap := map[string]int64{}
	for nm := range names {
		if v := p.Free(b(nm), b(sym)); v != 0 {
			freeMap[nm] = v
		}
		if v := p.Debt(b(nm), b(sym)); v != 0 {
			debtMap[nm] = v
		}
	}
	normal := ""
	for _, c := range p.NormalContracts(b(sym)) {
		normal += fmt.Sprintf("%d:%s:%s:%d,", c.ID, string(c.Lender), string(c.Borrower), c.Qty)
	}
	rs := ""
	for _, rc := range p.RecallContracts() {
		if string(rc.Sym) == sym {
			rs += fmt.Sprintf("%d:%s:%s:%d:%d,", rc.ID, string(rc.Lender), string(rc.Borrower), rc.Qty, rc.DL)
		}
	}
	price, _ := p.Price(b(sym))
	tl, fs, ls, rt := p.Invariant(b(sym))
	if tl != fs+ls+rt {
		panic("real invariant broken")
	}
	return snapshot{
		price:     price,
		free:      kvString(freeMap),
		normal:    normal,
		recalls:   rs,
		debt:      kvString(debtMap),
		totalLend: tl,
		returned:  rt,
		now:       p.Now(),
		buys:      buysReal(p.Buys()),
	}
}

// 操作（who 与 sym 取 "_" 表示空字节串，用于制造非法输入）。
type op struct {
	kind     int // 0 price 1 lend 2 borrow 3 withdraw 4 return
	now      int64
	who, sym string
	v        int64
}

func errKind(err error) byte {
	switch {
	case err == nil:
		return kNil
	case errors.Is(err, pool.ErrInvalid):
		return kInvalid
	case errors.Is(err, pool.ErrClock):
		return kClock
	case errors.Is(err, pool.ErrNotFound):
		return kNotFound
	case errors.Is(err, pool.ErrOverQty):
		return kOver
	case errors.Is(err, pool.ErrNoSupply):
		return kNoSupply
	}
	return 255
}

func TestAgainstNaiveRandom1500(t *testing.T) {
	if !testing.Verbose() {
		t.Log("使用 -v 可查看每步输入/输出/判定日志；本用例始终记录摘要")
	}
	const seq = 1500
	const ops = 40
	var totalOps int
	for s := 0; s < seq; s++ {
		rng := rand.New(rand.NewSource(int64(s) + 1))
		N := int64(1 + rng.Intn(12))
		pen := int64(rng.Intn(1200))
		real := mustNew(t, N, pen)
		na := newNaive(N, pen)
		now := int64(0)
		names := map[string]bool{}
		syms := []string{"S"}

		// 初始设价（也会随机重设以验证旧价结算）。
		seedPrice := int64(1 + rng.Intn(50))
		real.SetPrice(0, b("S"), seedPrice)
		na.setPrice(0, "S", seedPrice)
		if rng.Intn(3) == 0 {
			syms = append(syms, "T")
			tp := int64(1 + rng.Intn(50))
			real.SetPrice(0, b("T"), tp)
			na.setPrice(0, "T", tp)
		}

		for step := 0; step < ops; step++ {
			// 时钟：常等或前进，偶发回退制造拒绝。
			now += int64(rng.Intn(4))
			opNow := now
			if rng.Intn(15) == 0 {
				opNow = now - int64(1+rng.Intn(3))
			}
			sym := syms[rng.Intn(len(syms))]
			who := pickName(rng, names)
			v := int64(1 + rng.Intn(30))
			// 偶发非法入参。
			if rng.Intn(20) == 0 {
				who = "_"
			}
			if rng.Intn(20) == 0 {
				v = int64(1_000_000_001)
			}
			kind := rng.Intn(5)
			var nr nResult
			var ek byte
			var detail string
			realWho := who
			if who == "_" {
				realWho = ""
			}
			switch kind {
			case 0:
				price := v
				if v > 1_000_000_000 {
					price = v
				}
				bs, err := real.SetPrice(opNow, b(sym), price)
				ek = errKind(err)
				nr = na.setPrice(opNow, sym, price)
				detail = fmt.Sprintf("buys=%s", buysReal(bs))
			case 1:
				lr, err := real.Lend(opNow, b(realWho), b(sym), v)
				ek = errKind(err)
				nr = na.lend(opNow, who, sym, v)
				if err == nil {
					names[who] = true
				}
				detail = fmt.Sprintf("reg=%v/%v contracts=[] buys=%s", lr.Registered, lr.RegSeq, buysReal(lr.Buys))
			case 2:
				br, err := real.Borrow(opNow, b(realWho), b(sym), v)
				ek = errKind(err)
				nr = na.borrow(opNow, who, sym, v)
				if err == nil {
					names[who] = true
				}
				detail = fmt.Sprintf("contracts=%v buys=%s",
					fmtContracts(br.Contracts), buysReal(br.Buys))
				if err == nil && !matchBorrow(br.Contracts, nr) {
					t.Fatalf("Borrow 合约明细分歧: %s vs %+v", fmtContracts(br.Contracts), nr)
				}
			case 3:
				wr, err := real.Withdraw(opNow, b(realWho), b(sym), v)
				ek = errKind(err)
				nr = na.withdraw(opNow, who, sym, v)
				detail = fmt.Sprintf("rep=%s rec=%s buys=%s",
					fmtContracts(wr.Replacements), fmtRecalls(wr.Recalls), buysReal(wr.Buys))
				if err == nil && !matchWithdraw(wr, nr) {
					t.Fatalf("Withdraw 明细分歧 real=%s/%s naive=%+v",
						fmtContracts(wr.Replacements), fmtRecalls(wr.Recalls), nr)
				}
			case 4:
				rr, err := real.Return(opNow, b(realWho), b(sym), v)
				ek = errKind(err)
				nr = na.ret(opNow, who, sym, v)
				detail = fmt.Sprintf("toR=%d toN=%d buys=%s", rr.ToRecalled, rr.ToNormal, buysReal(rr.Buys))
				if err == nil && (rr.ToRecalled != nr.toR || rr.ToNormal != nr.toN) {
					t.Fatalf("Return 明细分歧 real=%d/%d naive=%d/%d",
						rr.ToRecalled, rr.ToNormal, nr.toR, nr.toN)
				}
			}

			totalOps++
			pass := ek == nr.errKind
			logLine := fmt.Sprintf("seq=%d step=%d op={k=%d now=%d who=%q sym=%q v=%d} realErr=%d naiveErr=%d %s",
				s, step, kind, opNow, who, sym, v, ek, nr.errKind, detail)
			t.Log(logLine + " 判定:" + map[bool]string{true: "错误类一致", false: "错误类不一致"}[pass])
			if !pass {
				t.Fatalf("错误类分歧: %s", logLine)
			}

			for _, sy := range syms {
				want := na.snap(sy)
				got := realSnap(real, sy, names)
				if want != got {
					t.Fatalf("状态分歧 seq=%d step=%d sym=%s\nnaive=%+v\nreal =%+v\n%s",
						s, step, sy, want, got, logLine)
				}
			}
		}
	}
	t.Logf("完成 %d 序列共 %d 次操作，朴素模型与实现逐步一致", seq, totalOps)
}

func mustNew(t *testing.T, n, pen int64) *pool.Pool {
	t.Helper()
	p, err := pool.NewPool(n, pen)
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	return p
}

func pickName(rng *rand.Rand, names map[string]bool) string {
	if len(names) == 0 || rng.Intn(3) == 0 {
		return fmt.Sprintf("P%d", rng.Intn(4))
	}
	i := rng.Intn(len(names))
	for k := range names {
		if i == 0 {
			return k
		}
		i--
	}
	return "P0"
}

func qtyList(cs []loan.Contract) []int64 {
	out := make([]int64, len(cs))
	for i, c := range cs {
		out[i] = c.Qty
	}
	return out
}

func lenderList(cs []loan.Contract) []string {
	out := make([]string, len(cs))
	for i, c := range cs {
		out[i] = string(c.Lender)
	}
	return out
}

func fmtContracts(cs []loan.Contract) string {
	out := ""
	for _, c := range cs {
		out += fmt.Sprintf("[%d:%s<-:%s q=%d]", c.ID, string(c.Lender), string(c.Borrower), c.Qty)
	}
	return out
}

func fmtRecalls(cs []recall.CContract) string {
	out := ""
	for _, c := range cs {
		out += fmt.Sprintf("[%d:%s<-:%s q=%d dl=%d]", c.ID, string(c.Lender), string(c.Borrower), c.Qty, c.DL)
	}
	return out
}

func matchBorrow(cs []loan.Contract, nr nResult) bool {
	if len(cs) != len(nr.ids) {
		return false
	}
	for i, c := range cs {
		if c.ID != nr.ids[i] || c.Qty != nr.qtys[i] || string(c.Lender) != nr.lenders[i] {
			return false
		}
	}
	return true
}

func matchWithdraw(wr pool.WithdrawReceipt, nr nResult) bool {
	if len(wr.Replacements) != len(nr.ids) || len(wr.Recalls) != len(nr.rcIDs) {
		return false
	}
	for i, c := range wr.Replacements {
		if c.ID != nr.ids[i] || c.Qty != nr.qtys[i] || string(c.Lender) != nr.lenders[i] {
			return false
		}
	}
	for i, rc := range wr.Recalls {
		if rc.ID != nr.rcIDs[i] || rc.Qty != nr.rcQty[i] || rc.DL != nr.rcDL[i] {
			return false
		}
	}
	return true
}

func (m *naive) withdraw(now int64, lender, sym string, qty int64) nResult {
	if badIn(now, lender, sym, qty) {
		return nResult{errKind: kInvalid}
	}
	if now < m.now {
		return nResult{errKind: kClock}
	}
	buys := m.settle(now)
	m.ensure(sym)
	if _, ok := m.reg[sym][lender]; !ok {
		return nResult{errKind: kNotFound, buys: buys}
	}
	var held int64
	for _, c := range m.normal[sym] {
		if c.lender == lender {
			held += c.qty
		}
	}
	if qty > m.free[sym][lender]+held {
		return nResult{errKind: kOver, buys: buys}
	}
	res := nResult{buys: buys}
	need := qty
	if f := m.free[sym][lender]; f > 0 {
		take := f
		if take > need {
			take = need
		}
		m.free[sym][lender] -= take
		m.returned[sym] += take
		need -= take
	}
	var ids []int64
	for id, c := range m.normal[sym] {
		if c.lender == lender {
			ids = append(ids, id)
		}
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] > ids[j] })
	for _, id := range ids {
		if need <= 0 {
			break
		}
		c := m.normal[sym][id]
		if c == nil {
			continue
		}
		remain := c.qty
		for _, other := range m.lendersByReg(sym, lender) {
			if need <= 0 || remain <= 0 {
				break
			}
			take := m.free[sym][other]
			if take > remain {
				take = remain
			}
			if take > need {
				take = need
			}
			m.free[sym][other] -= take
			nid := m.newID()
			m.normal[sym][nid] = &nContract{nid, other, c.borrower, take}
			res.ids = append(res.ids, nid)
			res.qtys = append(res.qtys, take)
			res.lenders = append(res.lenders, other)
			remain -= take
			need -= take
			m.returned[sym] += take
		}
		if need > 0 && remain > 0 {
			take := remain
			if take > need {
				take = need
			}
			rid := m.newID()
			m.recalls = append(m.recalls, &nRecall{rid, sym, lender, c.borrower, take, now + m.n})
			res.rcIDs = append(res.rcIDs, rid)
			res.rcQty = append(res.rcQty, take)
			res.rcDL = append(res.rcDL, now+m.n)
			remain -= take
			need -= take
		}
		if remain <= 0 {
			delete(m.normal[sym], id)
		} else {
			c.qty = remain
		}
	}
	return res
}

func (m *naive) ret(now int64, borrower, sym string, qty int64) nResult {
	if badIn(now, borrower, sym, qty) {
		return nResult{errKind: kInvalid}
	}
	if now < m.now {
		return nResult{errKind: kClock}
	}
	buys := m.settle(now)
	m.ensure(sym)
	if m.debt[sym][borrower] <= 0 {
		return nResult{errKind: kNotFound, buys: buys}
	}
	if qty > m.debt[sym][borrower] {
		return nResult{errKind: kOver, buys: buys}
	}
	res := nResult{buys: buys}
	need := qty
	m.sortRecalls()
	for _, rc := range m.recalls {
		if need <= 0 {
			break
		}
		if rc.sym != sym || rc.borrower != borrower {
			continue
		}
		take := rc.qty
		if take > need {
			take = need
		}
		rc.qty -= take
		m.returned[sym] += take
		m.debt[sym][borrower] -= take
		res.toR += take
		need -= take
	}
	var live []*nRecall
	for _, rc := range m.recalls {
		if rc.qty > 0 {
			live = append(live, rc)
		}
	}
	m.recalls = live
	var nids []int64
	for id, c := range m.normal[sym] {
		if c.borrower == borrower {
			nids = append(nids, id)
		}
	}
	sort.Slice(nids, func(i, j int) bool { return nids[i] < nids[j] })
	for _, id := range nids {
		if need <= 0 {
			break
		}
		c := m.normal[sym][id]
		take := c.qty
		if take > need {
			take = need
		}
		m.free[sym][c.lender] += take
		m.debt[sym][borrower] -= take
		res.toN += take
		need -= take
		c.qty -= take
		if c.qty <= 0 {
			delete(m.normal[sym], id)
		}
	}
	return res
}

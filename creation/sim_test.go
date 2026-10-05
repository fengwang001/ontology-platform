package creation

import (
	"errors"
	"fmt"
	"math/big"
	"math/rand/v2"
	"sort"
	"testing"

	"ontology/basket"
)

// 本文件按题面规则逐步写一个朴素模拟（model），与 Processor 对照 1500 组
// 随机操作序列：每步比较错误类别与全量状态，并校验守恒不变式。

type mAcct struct {
	cash   int64
	holds  map[string]int64
	shares int64
	locked int64
	debt   int64
}

type mGap struct {
	sym string
	d   int64
	pre int64
}

type mRec struct {
	acct string
	gaps []mGap
}

type model struct {
	items    []basket.Item // 按 sym 字节序
	e        int64
	rmax     int64
	q        int64
	maxNow   int64
	hasNow   bool
	prices   map[string]int64
	accts    map[string]*mAcct
	fundInv  map[string]int64
	fundCash int64
	ids      map[string]bool
	dayUnits int64
	pending  []mRec
}

func newModel(items []basket.Item, e, rmax, q int64) *model {
	sorted := make([]basket.Item, len(items))
	copy(sorted, items)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Sym < sorted[j].Sym })
	return &model{
		items:   sorted,
		e:       e,
		rmax:    rmax,
		q:       q,
		prices:  make(map[string]int64),
		accts:   make(map[string]*mAcct),
		fundInv: make(map[string]int64),
		ids:     make(map[string]bool),
	}
}

func (m *model) checkNow(now int64) error {
	if now < 0 || now > 1_000_000_000_000 {
		return basket.ErrParam
	}
	if m.hasNow && now < m.maxNow {
		return ErrClock
	}
	return nil
}

func (m *model) advance(now int64) {
	m.maxNow = now
	m.hasNow = true
}

func (m *model) acct(name string) *mAcct {
	a := m.accts[name]
	if a == nil {
		a = &mAcct{holds: make(map[string]int64)}
		m.accts[name] = a
	}
	return a
}

func (m *model) credit(now int64, acct, sym string, n int64) (error, string) {
	if acct == "" || sym == "" || n < 1 || n > 1_000_000_000_000 {
		return basket.ErrParam, "参数非法"
	}
	if err := m.checkNow(now); err != nil {
		return err, "时钟回退"
	}
	m.acct(acct).holds[sym] += n
	m.advance(now)
	return nil, fmt.Sprintf("入账 %s %s += %d", acct, sym, n)
}

func (m *model) creditCash(now int64, acct string, amt int64) (error, string) {
	if acct == "" || amt < 1 || amt > 1_000_000_000_000 {
		return basket.ErrParam, "参数非法"
	}
	if err := m.checkNow(now); err != nil {
		return err, "时钟回退"
	}
	m.acct(acct).cash += amt
	m.advance(now)
	return nil, fmt.Sprintf("入账现金 %s += %d", acct, amt)
}

func (m *model) setPrice(now int64, sym string, price int64) (error, string) {
	if sym == "" || price < 1 || price > 1_000_000 {
		return basket.ErrParam, "参数非法"
	}
	if err := m.checkNow(now); err != nil {
		return err, "时钟回退"
	}
	m.prices[sym] = price
	m.advance(now)
	return nil, fmt.Sprintf("现价 %s = %d", sym, price)
}

// create 按题面规则逐步模拟申购。
func (m *model) create(now int64, id, acct string, n int64) (error, string) {
	if id == "" || acct == "" || n < 1 || n > 1_000_000 {
		return basket.ErrParam, "参数非法"
	}
	if err := m.checkNow(now); err != nil {
		return err, "时钟回退"
	}
	a := m.accts[acct]
	if a == nil {
		return ErrNotExist, "账户未建立"
	}
	for _, it := range m.items {
		if it.Flag == basket.FlagM {
			continue
		}
		if _, ok := m.prices[it.Sym]; !ok {
			return ErrNotExist, "无现价 " + it.Sym
		}
	}
	if m.ids[id] {
		return ErrNotExist, "id 重复 " + id
	}
	if m.dayUnits+n > m.q {
		return ErrQuota, fmt.Sprintf("当日额度 %d+%d>%d", m.dayUnits, n, m.q)
	}
	for _, it := range m.items {
		if it.Flag == basket.FlagN && a.holds[it.Sym] < it.Qty*n {
			return &SymbolError{Kind: ErrComponent, Sym: it.Sym},
				fmt.Sprintf("成分券不足 %s 需%d持%d", it.Sym, it.Qty*n, a.holds[it.Sym])
		}
	}
	x := new(big.Int)
	y := new(big.Int)
	net := new(big.Int)
	rec := mRec{acct: acct}
	for _, it := range m.items {
		switch it.Flag {
		case basket.FlagN:
			y.Add(y, big.NewInt(it.Qty*n*m.prices[it.Sym]))
		case basket.FlagA:
			need := it.Qty * n
			give := a.holds[it.Sym]
			if give > need {
				give = need
			}
			d := need - give
			y.Add(y, big.NewInt(need*m.prices[it.Sym]))
			if d > 0 {
				x.Add(x, big.NewInt(d*m.prices[it.Sym]))
				// 预收 ceil(d*p*(10000+prem)/10000)
				num := new(big.Int).Mul(big.NewInt(d), big.NewInt(m.prices[it.Sym]))
				num.Mul(num, big.NewInt(10000+it.Prem))
				pre := num.Add(num, big.NewInt(9999)).Div(num, big.NewInt(10000)).Int64()
				net.Add(net, big.NewInt(pre))
				rec.gaps = append(rec.gaps, mGap{sym: it.Sym, d: d, pre: pre})
			}
		case basket.FlagM:
			fv := big.NewInt(it.Fixed * n)
			x.Add(x, fv)
			y.Add(y, fv)
			net.Add(net, fv)
		}
	}
	lhs := new(big.Int).Mul(x, big.NewInt(100))
	rhs := new(big.Int).Mul(y, big.NewInt(m.rmax))
	if lhs.Cmp(rhs) > 0 {
		return ErrRatio, fmt.Sprintf("替代比例超限 X*100=%s > Rmax*Y=%s", lhs, rhs)
	}
	net.Add(net, big.NewInt(m.e*n))
	if net.Sign() > 0 && net.Cmp(big.NewInt(a.cash)) > 0 {
		return ErrFund, fmt.Sprintf("资金不足 净额%s>现金%d", net, a.cash)
	}
	for _, it := range m.items {
		switch it.Flag {
		case basket.FlagN:
			a.holds[it.Sym] -= it.Qty * n
			m.fundInv[it.Sym] += it.Qty * n
		case basket.FlagA:
			need := it.Qty * n
			give := a.holds[it.Sym]
			if give > need {
				give = need
			}
			a.holds[it.Sym] -= give
			m.fundInv[it.Sym] += give
		}
	}
	n64 := net.Int64()
	a.cash -= n64
	m.fundCash += n64
	a.shares += n
	a.locked += n
	m.dayUnits += n
	m.ids[id] = true
	m.pending = append(m.pending, rec)
	m.advance(now)
	return nil, fmt.Sprintf("申购接受 X=%s Y=%s 净额=%d", x, y, n64)
}

// redeem 按题面规则逐步模拟赎回。
func (m *model) redeem(now int64, acct string, n int64) (error, string) {
	if acct == "" || n < 1 || n > 1_000_000 {
		return basket.ErrParam, "参数非法"
	}
	if err := m.checkNow(now); err != nil {
		return err, "时钟回退"
	}
	a := m.accts[acct]
	if a == nil {
		return ErrNotExist, "账户未建立"
	}
	for _, it := range m.items {
		if it.Flag == basket.FlagA {
			if _, ok := m.prices[it.Sym]; !ok {
				return ErrNotExist, "无现价 " + it.Sym
			}
		}
	}
	if a.shares-a.locked < n {
		return ErrShares, fmt.Sprintf("份额不足 可赎%d<%d", a.shares-a.locked, n)
	}
	for _, it := range m.items {
		if it.Flag == basket.FlagN && m.fundInv[it.Sym] < it.Qty*n {
			return &SymbolError{Kind: ErrInventory, Sym: it.Sym},
				fmt.Sprintf("库存不足 %s 需%d存%d", it.Sym, it.Qty*n, m.fundInv[it.Sym])
		}
	}
	net := new(big.Int)
	for _, it := range m.items {
		switch it.Flag {
		case basket.FlagA:
			need := it.Qty * n
			give := m.fundInv[it.Sym]
			if give > need {
				give = need
			}
			if d := need - give; d > 0 {
				// 偿付 floor(d*p*(10000-prem)/10000)
				num := new(big.Int).Mul(big.NewInt(d), big.NewInt(m.prices[it.Sym]))
				num.Mul(num, big.NewInt(10000-it.Prem))
				net.Add(net, num.Div(num, big.NewInt(10000)))
			}
		case basket.FlagM:
			net.Add(net, big.NewInt(it.Fixed*n))
		}
	}
	net.Add(net, big.NewInt(m.e*n))
	if net.Sign() < 0 && new(big.Int).Neg(net).Cmp(big.NewInt(a.cash)) > 0 {
		return ErrFund, fmt.Sprintf("资金不足 净收入%s 现金%d", net, a.cash)
	}
	for _, it := range m.items {
		switch it.Flag {
		case basket.FlagN:
			m.fundInv[it.Sym] -= it.Qty * n
			a.holds[it.Sym] += it.Qty * n
		case basket.FlagA:
			need := it.Qty * n
			give := m.fundInv[it.Sym]
			if give > need {
				give = need
			}
			m.fundInv[it.Sym] -= give
			a.holds[it.Sym] += give
		}
	}
	n64 := net.Int64()
	a.cash += n64
	m.fundCash -= n64
	a.shares -= n
	m.advance(now)
	return nil, fmt.Sprintf("赎回接受 净收入=%d", n64)
}

// eod 按题面规则逐步模拟日终退补与清零。
func (m *model) eod(now int64) (error, string) {
	if err := m.checkNow(now); err != nil {
		return err, "时钟回退"
	}
	var refund, collect, debt int64
	for _, rec := range m.pending {
		a := m.accts[rec.acct]
		for _, g := range rec.gaps {
			cost := g.d * m.prices[g.sym]
			diff := g.pre - cost
			switch {
			case diff > 0:
				a.cash += diff
				m.fundCash -= diff
				refund += diff
			case diff < 0:
				take := a.cash
				if take > -diff {
					take = -diff
				}
				a.cash -= take
				m.fundCash += take
				a.debt += -diff - take
				collect += take
				debt += -diff - take
			}
		}
		a.locked = 0
	}
	m.pending = nil
	m.dayUnits = 0
	m.advance(now)
	return nil, fmt.Sprintf("日终 退=%d 补收=%d 欠款=%d", refund, collect, debt)
}

var sentinels = []error{
	basket.ErrParam, ErrClock, ErrNotExist, ErrQuota,
	ErrComponent, ErrRatio, ErrFund, ErrShares, ErrInventory,
}

// sameErr 判定两个错误是否属于同一类别（含 SymbolError 的 sym）。
func sameErr(got, want error) bool {
	if (got == nil) != (want == nil) {
		return false
	}
	for _, s := range sentinels {
		if errors.Is(got, s) != errors.Is(want, s) {
			return false
		}
	}
	var gs, ws *SymbolError
	if errors.As(got, &gs) != errors.As(want, &ws) {
		return false
	}
	return gs == nil || ws == nil || gs.Sym == ws.Sym
}

// compareState 比较 Processor 与朴素模拟的全量状态。
func compareState(t *testing.T, p *Processor, m *model) {
	t.Helper()
	if p.fundCash != m.fundCash {
		t.Fatalf("fundCash: proc=%d model=%d", p.fundCash, m.fundCash)
	}
	if p.dayUnits != m.dayUnits || p.maxNow != m.maxNow || p.hasNow != m.hasNow {
		t.Fatalf("时钟/额度: proc=(%d,%d,%v) model=(%d,%d,%v)",
			p.dayUnits, p.maxNow, p.hasNow, m.dayUnits, m.maxNow, m.hasNow)
	}
	if len(p.ids) != len(m.ids) || len(p.pending) != len(m.pending) {
		t.Fatalf("ids/pending 长度不一致: %d/%d vs %d/%d",
			len(p.ids), len(m.ids), len(p.pending), len(m.pending))
	}
	syms := make(map[string]bool)
	for s := range p.fundInv {
		syms[s] = true
	}
	for s := range m.fundInv {
		syms[s] = true
	}
	for s := range syms {
		if p.fundInv[s] != m.fundInv[s] {
			t.Fatalf("fundInv[%s]: proc=%d model=%d", s, p.fundInv[s], m.fundInv[s])
		}
	}
	names := make(map[string]bool)
	for n := range p.accts {
		names[n] = true
	}
	for n := range m.accts {
		names[n] = true
	}
	for n := range names {
		pa, ma := p.accts[n], m.accts[n]
		if (pa == nil) != (ma == nil) {
			t.Fatalf("账户 %s 存在性不一致", n)
		}
		if pa == nil {
			continue
		}
		if pa.cash != ma.cash || pa.shares != ma.shares || pa.locked != ma.locked || pa.debt != ma.debt {
			t.Fatalf("账户 %s: proc=(cash %d,shares %d,locked %d,debt %d) model=(%d,%d,%d,%d)",
				n, pa.cash, pa.shares, pa.locked, pa.debt, ma.cash, ma.shares, ma.locked, ma.debt)
		}
		hs := make(map[string]bool)
		for s := range pa.holds {
			hs[s] = true
		}
		for s := range ma.holds {
			hs[s] = true
		}
		for s := range hs {
			if pa.holds[s] != ma.holds[s] {
				t.Fatalf("账户 %s 持券 %s: proc=%d model=%d", n, s, pa.holds[s], ma.holds[s])
			}
		}
	}
}

// checkInvariants 校验守恒不变式。
func checkInvariants(t *testing.T, p *Processor, credited map[string]int64, creditedCash, outShares int64) {
	t.Helper()
	var totalShares, totalCash int64
	holdsSum := make(map[string]int64)
	for name, a := range p.accts {
		if a.cash < 0 {
			t.Fatalf("账户 %s 现金为负 %d", name, a.cash)
		}
		if a.shares < 0 {
			t.Fatalf("账户 %s 份额为负 %d", name, a.shares)
		}
		totalShares += a.shares
		totalCash += a.cash
		for s, v := range a.holds {
			if v < 0 {
				t.Fatalf("账户 %s 持券 %s 为负 %d", name, s, v)
			}
			holdsSum[s] += v
		}
	}
	if totalShares != outShares {
		t.Fatalf("份额总数 %d != 累计申购-赎回 %d", totalShares, outShares)
	}
	if totalCash+p.fundCash != creditedCash {
		t.Fatalf("现金不守恒: 账户%d+基金%d != 入账%d", totalCash, p.fundCash, creditedCash)
	}
	syms := make(map[string]bool)
	for s := range credited {
		syms[s] = true
	}
	for s := range p.fundInv {
		syms[s] = true
	}
	for s := range syms {
		if got := holdsSum[s] + p.fundInv[s]; got != credited[s] {
			t.Fatalf("标的 %s 不守恒: 账户+基金 %d != Credit %d", s, got, credited[s])
		}
	}
}

// TestRandomReplay 1500 组随机操作序列与朴素模拟对照。
func TestRandomReplay(t *testing.T) {
	r := rand.New(rand.NewPCG(20261005, 1408))
	for seq := 0; seq < 1500; seq++ {
		runSeq(t, r, seq)
	}
}

func runSeq(t *testing.T, r *rand.Rand, seq int) {
	t.Helper()
	// 随机清单。
	syms := []string{"a", "b", "c", "d", "e"}
	r.Shuffle(len(syms), func(i, j int) { syms[i], syms[j] = syms[j], syms[i] })
	k := 1 + r.IntN(4)
	items := make([]basket.Item, 0, k)
	basketSyms := make([]string, 0, k)
	for _, s := range syms[:k] {
		it := basket.Item{Sym: s, Qty: int64(1 + r.IntN(10))}
		switch r.IntN(3) {
		case 0:
			it.Flag = basket.FlagN
		case 1:
			it.Flag = basket.FlagA
			it.Prem = int64(r.IntN(2001))
		case 2:
			it.Flag = basket.FlagM
			it.Fixed = int64(1 + r.IntN(2000))
		}
		items = append(items, it)
		basketSyms = append(basketSyms, s)
	}
	e := int64(r.IntN(41) - 20)
	rmax := int64(r.IntN(101))
	q := int64(1 + r.IntN(10))
	p, err := New(items, e, rmax, q)
	if err != nil {
		t.Fatalf("seq=%d New: %v", seq, err)
	}
	m := newModel(items, e, rmax, q)
	t.Logf("seq=%d 清单=%+v E=%d Rmax=%d Q=%d", seq, items, e, rmax, q)

	credited := make(map[string]int64)
	var creditedCash, createdUnits, redeemedUnits int64
	var now int64
	var ids []string
	idSeq := 0
	accts := []string{"u0", "u1", "u2"}
	allSyms := append(append([]string{}, basketSyms...), "x", "y")

	step := func(op string, gotErr, wantErr error, note string) {
		t.Helper()
		t.Logf("seq=%d %s => got=%v want=%v | %s", seq, op, gotErr, wantErr, note)
		if !sameErr(gotErr, wantErr) {
			t.Fatalf("seq=%d %s: got=%v want=%v", seq, op, gotErr, wantErr)
		}
		compareState(t, p, m)
		checkInvariants(t, p, credited, creditedCash, createdUnits-redeemedUnits)
	}
	nextNow := func() int64 {
		now += int64(r.IntN(3))
		if r.IntN(20) == 0 && now > 0 {
			return now - 1 // 偶发时钟回退
		}
		return now
	}
	pick := func(ss []string) string { return ss[r.IntN(len(ss))] }

	// 初始流动性：设价、入账。
	for _, s := range basketSyms {
		nw := nextNow()
		pr := int64(1 + r.IntN(50))
		gErr := p.SetPrice(nw, s, pr)
		wErr, note := m.setPrice(nw, s, pr)
		step(fmt.Sprintf("SetPrice(%d,%s,%d)", nw, s, pr), gErr, wErr, note)
	}
	for _, a := range accts {
		nw := nextNow()
		amt := int64(1 + r.IntN(5000))
		gErr := p.CreditCash(nw, a, amt)
		wErr, note := m.creditCash(nw, a, amt)
		if gErr == nil {
			creditedCash += amt
		}
		step(fmt.Sprintf("CreditCash(%d,%s,%d)", nw, a, amt), gErr, wErr, note)
		for _, s := range basketSyms {
			if r.IntN(10) < 7 {
				nw := nextNow()
				n := int64(1 + r.IntN(30))
				gErr := p.Credit(nw, a, s, n)
				wErr, note := m.credit(nw, a, s, n)
				if gErr == nil {
					credited[s] += n
				}
				step(fmt.Sprintf("Credit(%d,%s,%s,%d)", nw, a, s, n), gErr, wErr, note)
			}
		}
	}

	// 随机操作序列。
	ops := 15 + r.IntN(20)
	for i := 0; i < ops; i++ {
		nw := nextNow()
		switch r.IntN(100) {
		case 0, 1, 2, 3: // 非法参数
			n := int64(0)
			if r.IntN(2) == 0 {
				n = -1
			}
			a := pick(accts)
			gErr := p.Create(nw, "bad", a, n)
			wErr, note := m.create(nw, "bad", a, n)
			step(fmt.Sprintf("Create(%d,bad,%s,%d) 非法参数", nw, a, n), gErr, wErr, note)
		case 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19: // 申购
			a := pick(accts)
			n := int64(1 + r.IntN(5))
			id := fmt.Sprintf("s%di%d", seq, idSeq)
			idSeq++
			if r.IntN(10) == 0 && len(ids) > 0 {
				id = ids[r.IntN(len(ids))] // 重复 id
			}
			gErr := p.Create(nw, id, a, n)
			wErr, note := m.create(nw, id, a, n)
			if gErr == nil {
				ids = append(ids, id)
				createdUnits += n
			}
			step(fmt.Sprintf("Create(%d,%s,%s,%d)", nw, id, a, n), gErr, wErr, note)
		case 20, 21, 22, 23, 24, 25, 26, 27, 28, 29: // 赎回
			a := pick(accts)
			n := int64(1 + r.IntN(3))
			gErr := p.Redeem(nw, a, n)
			wErr, note := m.redeem(nw, a, n)
			if gErr == nil {
				redeemedUnits += n
			}
			step(fmt.Sprintf("Redeem(%d,%s,%d)", nw, a, n), gErr, wErr, note)
		case 30, 31, 32, 33, 34, 35, 36, 37, 38, 39: // 日终
			gErr := p.EndOfDay(nw)
			wErr, note := m.eod(nw)
			step(fmt.Sprintf("EndOfDay(%d)", nw), gErr, wErr, note)
		case 40, 41, 42, 43, 44, 45, 46, 47, 48, 49, 50, 51, 52, 53, 54: // 设价
			s := pick(basketSyms)
			pr := int64(1 + r.IntN(50))
			gErr := p.SetPrice(nw, s, pr)
			wErr, note := m.setPrice(nw, s, pr)
			step(fmt.Sprintf("SetPrice(%d,%s,%d)", nw, s, pr), gErr, wErr, note)
		case 55, 56, 57, 58, 59, 60, 61, 62, 63, 64, 65, 66, 67, 68, 69: // 入账券
			a := pick(accts)
			s := pick(allSyms)
			n := int64(1 + r.IntN(50))
			gErr := p.Credit(nw, a, s, n)
			wErr, note := m.credit(nw, a, s, n)
			if gErr == nil {
				credited[s] += n
			}
			step(fmt.Sprintf("Credit(%d,%s,%s,%d)", nw, a, s, n), gErr, wErr, note)
		default: // 入账现金
			a := pick(accts)
			amt := int64(1 + r.IntN(5000))
			gErr := p.CreditCash(nw, a, amt)
			wErr, note := m.creditCash(nw, a, amt)
			if gErr == nil {
				creditedCash += amt
			}
			step(fmt.Sprintf("CreditCash(%d,%s,%d)", nw, a, amt), gErr, wErr, note)
		}
	}
}

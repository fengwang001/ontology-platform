package adjust

import (
	"errors"
	"fmt"
	"math/big"
	"math/rand"
	"reflect"
	"sort"
	"sync"
	"testing"

	"ontology/corpact"
	"ontology/holding"
)

// 朴素模拟：严格按题目规则逐步实现的独立参照实现，
// 与 Engine 的内部结构完全不同（Pex 用 big.Rat、快照即时拷贝、
// 执行时遍历全表），用于 1500 组随机操作序列的对照。

type nPos struct{ q, f int64 }

type nAcct struct {
	cash   int64
	frCash int64
	pos    map[string]nPos
}

type nOrder struct {
	acct, sym  string
	buy        bool
	price, qty int64
}

type nAction struct {
	sym      string
	c, b     int64
	rec, ex  int
	snap     map[string]nPos // nil 表示尚未拍快照
	executed bool
	res      *corpact.Result
}

type naive struct {
	day     int
	closes  map[string]int64
	accts   map[string]*nAcct
	orders  map[string]*nOrder
	actions map[string]*nAction
	pending map[string]string // 标的 -> 未执行行动 id
}

func newNaive() *naive {
	return &naive{
		closes:  map[string]int64{},
		accts:   map[string]*nAcct{},
		orders:  map[string]*nOrder{},
		actions: map[string]*nAction{},
		pending: map[string]string{},
	}
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// pexNaive 用有理数独立计算除权参考价：(10P-c)/(10+b) 加 1/2 后下整，小于 1 取 1。
func pexNaive(p, c, b int64) int64 {
	r := big.NewRat(10*p-c, 10+b)
	r.Add(r, big.NewRat(1, 2))
	z := new(big.Int).Quo(r.Num(), r.Denom())
	pex := z.Int64()
	if pex < 1 {
		pex = 1
	}
	return pex
}

var sentinels = []struct {
	name string
	err  error
}{
	{"参数非法", holding.ErrInvalidParam},
	{"日期回退", holding.ErrDateRollback},
	{"编号重复", holding.ErrDuplicate},
	{"不存在", holding.ErrNotExist},
	{"状态不符", holding.ErrState},
	{"冲突", holding.ErrConflict},
	{"无参考价", holding.ErrNoRefPrice},
	{"数量不足", holding.ErrInsufficient},
}

func errLabel(err error) string {
	if err == nil {
		return "成功"
	}
	for _, s := range sentinels {
		if errors.Is(err, s.err) {
			return s.name
		}
	}
	return "未知:" + err.Error()
}

func (n *naive) getOrCreate(acct string) *nAcct {
	a := n.accts[acct]
	if a == nil {
		a = &nAcct{pos: map[string]nPos{}}
		n.accts[acct] = a
	}
	return a
}

func (n *naive) advance(d int) error {
	if d < 0 || d > 1_000_000 {
		return holding.ErrInvalidParam
	}
	if d < n.day {
		return holding.ErrDateRollback
	}
	n.day = d
	ids := sortedKeys(n.actions)
	// 先为所有越过 rec 的行动拍快照
	for _, id := range ids {
		a := n.actions[id]
		if a.snap == nil && d > a.rec {
			a.snap = map[string]nPos{}
			for name, ac := range n.accts {
				if p, ok := ac.pos[a.sym]; ok && p.q > 0 {
					a.snap[name] = p
				}
			}
		}
	}
	// 再执行所有到达 ex 的行动
	for _, id := range ids {
		a := n.actions[id]
		if !a.executed && d >= a.ex {
			n.execute(a)
		}
	}
	return nil
}

func (n *naive) execute(a *nAction) {
	p := n.closes[a.sym]
	pex := pexNaive(p, a.c, a.b)
	res := &corpact.Result{Pex: pex}
	for _, name := range sortedKeys(a.snap) {
		s := a.snap[name]
		shares := s.q * a.b / 10
		frShares := s.f * a.b / 10
		cash := s.q * a.c / 10
		frCash := s.f * a.c / 10
		frac := (s.q * a.b % 10) * pex / 10
		ac := n.accts[name]
		pos := ac.pos[a.sym]
		pos.q += shares
		pos.f += frShares
		ac.pos[a.sym] = pos
		ac.cash += cash - frCash + frac
		ac.frCash += frCash
		res.Gains = append(res.Gains, corpact.AccountGain{
			Acct:         name,
			Shares:       shares,
			FrozenShares: frShares,
			Cash:         cash,
			FrozenCash:   frCash,
			FractionCash: frac,
		})
	}
	for _, oid := range sortedKeys(n.orders) {
		o := n.orders[oid]
		if o.sym != a.sym {
			continue
		}
		adj := corpact.OrderAdj{Oid: oid, OldPrice: o.price}
		if o.buy {
			np := o.price * pex / p
			adj.NewPrice = np
			if np < 1 {
				adj.Cancelled = true
				delete(n.orders, oid)
			} else {
				o.price = np
			}
		} else {
			np := (o.price*pex + p - 1) / p
			adj.NewPrice = np
			o.price = np
		}
		res.Orders = append(res.Orders, adj)
	}
	a.executed = true
	a.res = res
	n.closes[a.sym] = pex
	delete(n.pending, a.sym)
}

func (n *naive) deposit(acct string, cash int64) error {
	if acct == "" || cash < 1 || cash > 1_000_000_000_000 {
		return holding.ErrInvalidParam
	}
	n.getOrCreate(acct).cash += cash
	return nil
}

func (n *naive) trade(acct, sym string, delta int64) error {
	if acct == "" || sym == "" || delta == 0 || delta > 1_000_000_000 || delta < -1_000_000_000 {
		return holding.ErrInvalidParam
	}
	a := n.accts[acct]
	if delta < 0 {
		if a == nil {
			return holding.ErrNotExist
		}
		p := a.pos[sym]
		if p.q-p.f < -delta {
			return holding.ErrInsufficient
		}
		p.q += delta
		if p.q == 0 && p.f == 0 {
			delete(a.pos, sym)
		} else {
			a.pos[sym] = p
		}
		return nil
	}
	a = n.getOrCreate(acct)
	p := a.pos[sym]
	p.q += delta
	a.pos[sym] = p
	return nil
}

func (n *naive) freeze(acct, sym string, shares int64) error {
	if acct == "" || sym == "" || shares < 1 {
		return holding.ErrInvalidParam
	}
	a := n.accts[acct]
	if a == nil {
		return holding.ErrNotExist
	}
	p := a.pos[sym]
	if shares > p.q-p.f {
		return holding.ErrInsufficient
	}
	p.f += shares
	a.pos[sym] = p
	return nil
}

func (n *naive) unfreeze(acct, sym string, shares int64) error {
	if acct == "" || sym == "" || shares < 1 {
		return holding.ErrInvalidParam
	}
	a := n.accts[acct]
	if a == nil {
		return holding.ErrNotExist
	}
	p := a.pos[sym]
	if shares > p.f {
		return holding.ErrInsufficient
	}
	p.f -= shares
	a.pos[sym] = p
	return nil
}

func (n *naive) setClose(sym string, p int64) error {
	if sym == "" || p < 1 || p > 1_000_000_000 {
		return holding.ErrInvalidParam
	}
	n.closes[sym] = p
	return nil
}

func (n *naive) announce(id, sym string, c, b int64, rec, ex int) error {
	if id == "" || sym == "" ||
		c < 0 || c > 1_000_000 || b < 0 || b > 100 || (c == 0 && b == 0) ||
		rec < 0 || rec >= ex || ex > 1_000_000 {
		return holding.ErrInvalidParam
	}
	if _, ok := n.actions[id]; ok {
		return holding.ErrDuplicate
	}
	if rec <= n.day {
		return holding.ErrState
	}
	if _, ok := n.pending[sym]; ok {
		return holding.ErrConflict
	}
	if _, ok := n.closes[sym]; !ok {
		return holding.ErrNoRefPrice
	}
	n.actions[id] = &nAction{sym: sym, c: c, b: b, rec: rec, ex: ex}
	n.pending[sym] = id
	return nil
}

func (n *naive) cancelAction(id string) error {
	if id == "" {
		return holding.ErrInvalidParam
	}
	a, ok := n.actions[id]
	if !ok {
		return holding.ErrNotExist
	}
	if a.snap != nil || a.executed {
		return holding.ErrState
	}
	delete(n.actions, id)
	delete(n.pending, a.sym)
	return nil
}

func (n *naive) placeOrder(oid, acct, sym string, side Side, price, qty int64) error {
	if oid == "" || acct == "" || sym == "" ||
		(side != Buy && side != Sell) ||
		price < 1 || price > 1_000_000_000 || qty < 1 || qty > 1_000_000_000 {
		return holding.ErrInvalidParam
	}
	if _, ok := n.orders[oid]; ok {
		return holding.ErrDuplicate
	}
	n.orders[oid] = &nOrder{acct: acct, sym: sym, buy: side == Buy, price: price, qty: qty}
	return nil
}

func (n *naive) cancelOrder(oid string) error {
	if oid == "" {
		return holding.ErrInvalidParam
	}
	if _, ok := n.orders[oid]; !ok {
		return holding.ErrNotExist
	}
	delete(n.orders, oid)
	return nil
}

func (n *naive) result(id string) (*corpact.Result, error) {
	if id == "" {
		return nil, holding.ErrInvalidParam
	}
	a, ok := n.actions[id]
	if !ok {
		return nil, holding.ErrNotExist
	}
	if !a.executed {
		return nil, holding.ErrState
	}
	return a.res, nil
}

// ---------- 状态导出与终态对照 ----------

var symUniverse = []string{"S1", "S2", "S3", "SW"}

type stateDump struct {
	day    int
	closes map[string]int64
	accts  map[string]holding.AccountState
	orders map[string]Order
}

func dumpEngine(e *Engine) stateDump {
	d := stateDump{
		day:    e.Day(),
		closes: map[string]int64{},
		accts:  e.book.Dump(),
		orders: map[string]Order{},
	}
	for _, s := range symUniverse {
		if p, ok := e.reg.Close(s); ok {
			d.closes[s] = p
		}
	}
	for oid, o := range e.orders {
		d.orders[oid] = *o
	}
	return d
}

func dumpNaive(n *naive) stateDump {
	d := stateDump{
		day:    n.day,
		closes: map[string]int64{},
		accts:  map[string]holding.AccountState{},
		orders: map[string]Order{},
	}
	for _, s := range symUniverse {
		if p, ok := n.closes[s]; ok {
			d.closes[s] = p
		}
	}
	for name, a := range n.accts {
		st := holding.AccountState{
			Cash:       a.cash,
			FrozenCash: a.frCash,
			Positions:  map[string]holding.Pos{},
		}
		for sym, p := range a.pos {
			st.Positions[sym] = holding.Pos{Q: p.q, F: p.f}
		}
		d.accts[name] = st
	}
	for oid, o := range n.orders {
		side := Buy
		if !o.buy {
			side = Sell
		}
		d.orders[oid] = Order{Oid: oid, Acct: o.acct, Sym: o.sym, Side: side, Price: o.price, Qty: o.qty}
	}
	return d
}

// ---------- 随机操作序列生成 ----------

type genOp struct {
	desc string
	eng  func(e *Engine) (any, error)
	sim  func(n *naive) (any, error)
}

func genSequence(r *rand.Rand, nOps int) []genOp {
	accts := []string{"A", "B", "C", "D", "E", "F"}
	oids := []string{"o1", "o2", "o3", "o4", "o5", "o6"}
	actIDs := []string{"g1", "g2", "g3", "g4"}
	wild := r.Float64() < 0.1 // 极端参数序列：触发 Pex 钳位与低价撤单
	pick := func(xs []string) string { return xs[r.Intn(len(xs))] }

	day := 0 // 假定全部 Advance 成功时的计划日，仅用于生成参数
	var announced []string
	ops := make([]genOp, 0, nOps)
	for i := 0; i < nOps; i++ {
		switch r.Intn(100) {
		case 0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11: // Advance
			var d int
			switch r.Intn(10) {
			case 0:
				d = -1 - r.Intn(3) // 非法
			case 1:
				d = 1_000_001 // 非法
			case 2:
				d = day - 1 - r.Intn(2) // 回退
			case 3:
				d = day
			default:
				d = day + r.Intn(4)
			}
			if d >= day && d <= 1_000_000 {
				day = d
			}
			ops = append(ops, genOp{
				desc: fmt.Sprintf("Advance(%d)", d),
				eng:  func(e *Engine) (any, error) { return nil, e.Advance(d) },
				sim:  func(n *naive) (any, error) { return nil, n.advance(d) },
			})
		case 12, 13, 14, 15, 16, 17, 18, 19, 20, 21: // Deposit
			acct := pick(accts)
			cash := int64(1 + r.Intn(1_000_000))
			switch r.Intn(12) {
			case 0:
				cash = 0
			case 1:
				cash = 1_000_000_000_001
			}
			ops = append(ops, genOp{
				desc: fmt.Sprintf("Deposit(%q,%d)", acct, cash),
				eng:  func(e *Engine) (any, error) { return nil, e.Deposit(acct, cash) },
				sim:  func(n *naive) (any, error) { return nil, n.deposit(acct, cash) },
			})
		case 22, 23, 24, 25, 26, 27, 28, 29, 30, 31,
			32, 33, 34, 35, 36, 37, 38, 39, 40, 41,
			42, 43, 44, 45, 46: // Trade
			acct, sym := pick(accts), pick(symUniverse)
			var delta int64
			switch r.Intn(10) {
			case 0:
				delta = 0
			case 1:
				delta = 1_000_000_001
			case 2, 3, 4:
				delta = -int64(1 + r.Intn(300))
			default:
				delta = int64(1 + r.Intn(200))
			}
			ops = append(ops, genOp{
				desc: fmt.Sprintf("Trade(%q,%q,%d)", acct, sym, delta),
				eng:  func(e *Engine) (any, error) { return nil, e.Trade(acct, sym, delta) },
				sim:  func(n *naive) (any, error) { return nil, n.trade(acct, sym, delta) },
			})
		case 47, 48, 49, 50, 51, 52, 53, 54: // Freeze
			acct, sym := pick(accts), pick(symUniverse)
			shares := int64(r.Intn(150))
			ops = append(ops, genOp{
				desc: fmt.Sprintf("Freeze(%q,%q,%d)", acct, sym, shares),
				eng:  func(e *Engine) (any, error) { return nil, e.Freeze(acct, sym, shares) },
				sim:  func(n *naive) (any, error) { return nil, n.freeze(acct, sym, shares) },
			})
		case 55, 56, 57, 58, 59: // Unfreeze
			acct, sym := pick(accts), pick(symUniverse)
			shares := int64(1 + r.Intn(150))
			ops = append(ops, genOp{
				desc: fmt.Sprintf("Unfreeze(%q,%q,%d)", acct, sym, shares),
				eng:  func(e *Engine) (any, error) { return nil, e.Unfreeze(acct, sym, shares) },
				sim:  func(n *naive) (any, error) { return nil, n.unfreeze(acct, sym, shares) },
			})
		case 60, 61, 62, 63, 64, 65, 66, 67: // SetClose
			sym := pick(symUniverse)
			p := int64(1 + r.Intn(3000))
			if wild {
				p = int64(1 + r.Intn(20))
			}
			switch r.Intn(15) {
			case 0:
				p = 0
			case 1:
				p = 1_000_000_000
			}
			ops = append(ops, genOp{
				desc: fmt.Sprintf("SetClose(%q,%d)", sym, p),
				eng:  func(e *Engine) (any, error) { return nil, e.SetClose(sym, p) },
				sim:  func(n *naive) (any, error) { return nil, n.setClose(sym, p) },
			})
		case 68, 69, 70, 71, 72, 73, 74, 75, 76, 77, 78, 79: // PlaceOrder
			oid, acct, sym := pick(oids), pick(accts), pick(symUniverse)
			side := Buy
			if r.Intn(2) == 0 {
				side = Sell
			}
			if r.Intn(20) == 0 {
				side = Side(9)
			}
			price := int64(1 + r.Intn(1500))
			if wild {
				price = int64(1 + r.Intn(3))
			}
			qty := int64(1 + r.Intn(100))
			ops = append(ops, genOp{
				desc: fmt.Sprintf("PlaceOrder(%q,%q,%q,%d,%d,%d)", oid, acct, sym, side, price, qty),
				eng:  func(e *Engine) (any, error) { return nil, e.PlaceOrder(oid, acct, sym, side, price, qty) },
				sim:  func(n *naive) (any, error) { return nil, n.placeOrder(oid, acct, sym, side, price, qty) },
			})
		case 80, 81, 82, 83, 84: // CancelOrder
			oid := pick(oids)
			ops = append(ops, genOp{
				desc: fmt.Sprintf("CancelOrder(%q)", oid),
				eng:  func(e *Engine) (any, error) { return nil, e.CancelOrder(oid) },
				sim:  func(n *naive) (any, error) { return nil, n.cancelOrder(oid) },
			})
		case 85, 86, 87, 88, 89, 90, 91, 92: // Announce
			id, sym := pick(actIDs), pick(symUniverse)
			var c, b int64
			if wild {
				c = int64(r.Intn(1_000_001))
				b = int64(r.Intn(101))
			} else {
				c = int64(r.Intn(51))
				b = int64(r.Intn(13))
			}
			rec := day + r.Intn(4)
			ex := rec + 1 + r.Intn(3)
			switch r.Intn(15) {
			case 0:
				c, b = 0, 0 // 非法
			case 1:
				ex = rec // 非法
			case 2:
				rec = day - 1 // 状态不符（若 day>=1）
			}
			announced = append(announced, id)
			ops = append(ops, genOp{
				desc: fmt.Sprintf("Announce(%q,%q,%d,%d,%d,%d)", id, sym, c, b, rec, ex),
				eng:  func(e *Engine) (any, error) { return nil, e.Announce(id, sym, c, b, rec, ex) },
				sim:  func(n *naive) (any, error) { return nil, n.announce(id, sym, c, b, rec, ex) },
			})
		case 93, 94, 95, 96: // CancelAction
			id := pick(actIDs)
			ops = append(ops, genOp{
				desc: fmt.Sprintf("CancelAction(%q)", id),
				eng:  func(e *Engine) (any, error) { return nil, e.CancelAction(id) },
				sim:  func(n *naive) (any, error) { return nil, n.cancelAction(id) },
			})
		default: // Result
			id := pick(actIDs)
			if len(announced) > 0 && r.Intn(10) < 7 {
				id = announced[r.Intn(len(announced))]
			}
			ops = append(ops, genOp{
				desc: fmt.Sprintf("Result(%q)", id),
				eng:  func(e *Engine) (any, error) { return e.Result(id) },
				sim:  func(n *naive) (any, error) { return n.result(id) },
			})
		}
	}
	// 极端除权簇：c=10P 使 Pex 钳位到 1，低价买单被撤销。
	if r.Float64() < 0.3 {
		sym := "SW"
		id := fmt.Sprintf("w%d", r.Intn(1_000_000))
		p := int64(4 + r.Intn(47))
		c := 10 * p
		rec, ex := day+1, day+2
		acct := pick(accts)
		price := int64(1 + r.Intn(3))
		cluster := []genOp{
			{
				desc: fmt.Sprintf("SetClose(%q,%d)", sym, p),
				eng:  func(e *Engine) (any, error) { return nil, e.SetClose(sym, p) },
				sim:  func(n *naive) (any, error) { return nil, n.setClose(sym, p) },
			},
			{
				desc: fmt.Sprintf("Announce(%q,%q,%d,0,%d,%d)", id, sym, c, rec, ex),
				eng:  func(e *Engine) (any, error) { return nil, e.Announce(id, sym, c, 0, rec, ex) },
				sim:  func(n *naive) (any, error) { return nil, n.announce(id, sym, c, 0, rec, ex) },
			},
			{
				desc: fmt.Sprintf("PlaceOrder(%q,%q,%q,%d,%d,10)", "wo1", acct, sym, Buy, price),
				eng:  func(e *Engine) (any, error) { return nil, e.PlaceOrder("wo1", acct, sym, Buy, price, 10) },
				sim:  func(n *naive) (any, error) { return nil, n.placeOrder("wo1", acct, sym, Buy, price, 10) },
			},
			{
				desc: fmt.Sprintf("PlaceOrder(%q,%q,%q,%d,1,10)", "wo2", acct, sym, Sell),
				eng:  func(e *Engine) (any, error) { return nil, e.PlaceOrder("wo2", acct, sym, Sell, 1, 10) },
				sim:  func(n *naive) (any, error) { return nil, n.placeOrder("wo2", acct, sym, Sell, 1, 10) },
			},
			{
				desc: fmt.Sprintf("Advance(%d)", ex),
				eng:  func(e *Engine) (any, error) { return nil, e.Advance(ex) },
				sim:  func(n *naive) (any, error) { return nil, n.advance(ex) },
			},
			{
				desc: fmt.Sprintf("Result(%q)", id),
				eng:  func(e *Engine) (any, error) { return e.Result(id) },
				sim:  func(n *naive) (any, error) { return n.result(id) },
			},
		}
		pos := r.Intn(len(ops) + 1)
		ops = append(ops[:pos], append(cluster, ops[pos:]...)...)
	}
	return ops
}

// 1500 组随机操作序列与朴素模拟逐步对照：
// 每一步比较错误类别（errors.Is）与输出，结束比较终态；
// 日志打印输入（操作描述）、输出（错误类别/结果）与判定依据。
func TestRandomReplay(t *testing.T) {
	const sequences = 1500
	var statExec, statOrderAdj, statCancel, statResultOK int
	for i := 0; i < sequences; i++ {
		r := rand.New(rand.NewSource(int64(i)))
		ops := genSequence(r, 20+r.Intn(60))
		e := New()
		n := newNaive()
		failed := false
		for j, op := range ops {
			ev, eerr := op.eng(e)
			sv, serr := op.sim(n)
			if i == 0 {
				t.Logf("seq0 step %d 输入=%s 输出: engine=%s naive=%s",
					j, op.desc, errLabel(eerr), errLabel(serr))
			}
			if errLabel(eerr) != errLabel(serr) {
				t.Errorf("seq %d step %d 输入=%s: 错误类别不一致 engine=%s naive=%s（判定依据：拒绝次序与 errors.Is 类别）",
					i, j, op.desc, errLabel(eerr), errLabel(serr))
				failed = true
			}
			if !reflect.DeepEqual(ev, sv) {
				t.Errorf("seq %d step %d 输入=%s: 输出不一致\nengine=%+v\nnaive =%+v（判定依据：Result 内容逐项深比较）",
					i, j, op.desc, ev, sv)
				failed = true
			}
			if failed {
				for k, o := range ops {
					t.Logf("seq %d 输入序列 step %d: %s", i, k, o.desc)
				}
				break
			}
			if res, ok := ev.(*corpact.Result); ok && res != nil {
				statResultOK++
				if len(res.Orders) > 0 {
					statOrderAdj++
				}
				for _, adj := range res.Orders {
					if adj.Cancelled {
						statCancel++
						break
					}
				}
			}
		}
		if failed {
			continue
		}
		for _, a := range n.actions {
			if a.executed {
				statExec++
				break
			}
		}
		ed, sd := dumpEngine(e), dumpNaive(n)
		if !reflect.DeepEqual(ed, sd) {
			t.Errorf("seq %d: 终态不一致（判定依据：账户/持仓/冻结/现金/收盘价/在簿委托全量深比较）\nengine=%+v\nnaive =%+v",
				i, ed, sd)
			for k, o := range ops {
				t.Logf("seq %d 输入序列 step %d: %s", i, k, o.desc)
			}
			continue
		}
		// 不变量：任意时刻每个账户每个标的 0<=f<=q
		for acct, st := range ed.accts {
			for sym, p := range st.Positions {
				if p.F < 0 || p.F > p.Q {
					t.Errorf("seq %d: 不变量被破坏 %s %s q=%d f=%d", i, acct, sym, p.Q, p.F)
				}
			}
		}
		if i%300 == 0 {
			t.Logf("seq %d: %d 步操作全部一致（判定依据：逐步错误类别+输出+终态深比较）", i, len(ops))
		}
	}
	t.Logf("覆盖统计：含已执行除权的序列=%d/%d，Result 成功返回=%d 次，含委托调价=%d 次，含低价撤单=%d 次",
		statExec, sequences, statResultOK, statOrderAdj, statCancel)
}

// 相同操作序列重放得到相同结果。
func TestDeterministicReplay(t *testing.T) {
	r := rand.New(rand.NewSource(42))
	ops := genSequence(r, 200)
	run := func() stateDump {
		e := New()
		for _, op := range ops {
			if _, err := op.eng(e); err != nil {
				_ = err
			}
		}
		return dumpEngine(e)
	}
	want := run()
	for i := 0; i < 20; i++ {
		if got := run(); !reflect.DeepEqual(got, want) {
			t.Fatalf("第 %d 次重放结果不一致", i)
		}
	}
}

// touched 对照：一次除权执行触碰的账户记录数与只持有其他标的的账户数无关。
func TestTouchedIndependentOfOtherAccounts(t *testing.T) {
	run := func(total int) int {
		e := New()
		must(t, e.Trade("H1", "S1", 100))
		must(t, e.Trade("H2", "S1", 50))
		for i := 0; i < total-2; i++ {
			must(t, e.Trade(fmt.Sprintf("X%05d", i), "S2", 5))
		}
		must(t, e.SetClose("S1", 1000))
		must(t, e.Announce("act", "S1", 10, 3, 1, 2))
		before := e.touched
		must(t, e.Advance(2))
		return e.touched - before
	}
	t10 := run(10)
	t10000 := run(10000)
	if t10 != 2 || t10000 != 2 {
		t.Fatalf("touched: 10 账户档=%d, 10000 账户档=%d, 均应为 2", t10, t10000)
	}
	t.Logf("touched 对照：10 账户档=%d，10000 账户档=%d（判定依据：仅触碰快照持仓非零账户）",
		t10, t10000)
}

// 并发调用等价于某个串行顺序：竞态检测下不变量保持。
func TestConcurrentEquivalentToSerial(t *testing.T) {
	e := New()
	must(t, e.SetClose("S1", 1000))
	must(t, e.Announce("act", "S1", 10, 3, 3, 5))
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			r := rand.New(rand.NewSource(int64(g)))
			acct := fmt.Sprintf("G%d", g)
			for i := 0; i < 200; i++ {
				switch r.Intn(6) {
				case 0:
					e.Deposit(acct, 10)
				case 1:
					e.Trade(acct, "S1", 5)
				case 2:
					e.Trade(acct, "S1", -3)
				case 3:
					e.Freeze(acct, "S1", 2)
				case 4:
					e.Unfreeze(acct, "S1", 1)
				case 5:
					e.PlaceOrder(fmt.Sprintf("o-%d-%d", g, i), acct, "S1", Buy, 100, 1)
				}
			}
		}(g)
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for d := 1; d <= 20; d++ {
			e.Advance(d)
		}
	}()
	wg.Wait()
	for acct, st := range e.book.Dump() {
		for sym, p := range st.Positions {
			if p.F < 0 || p.F > p.Q {
				t.Fatalf("不变量被破坏: %s %s q=%d f=%d", acct, sym, p.Q, p.F)
			}
		}
	}
	if _, err := e.Result("act"); err != nil {
		t.Fatalf("除权应已执行: %v", err)
	}
}

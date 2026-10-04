// Package exposure 按边维护持仓、在途委托与套保额度，并增量汇总账户/组敞口。
package exposure

import "ontology/limit"

// Rec 为单个账户在单个合约上的双边状态（0=多头, 1=空头）。
type Rec struct {
	Pos       [2]int64 // 持仓
	Open      [2]int64 // 在途开仓未成交量
	Close     [2]int64 // 在途平仓未成交量
	Hedge     [2]int64 // 套保额度
	DayFilled int64    // 当日已成交开仓量（多空合计）
	Contrib   [2]int64 // 对所属组的贡献 max(0,e-H)
}

// Reason 为开仓预检失败原因。
type Reason int

const (
	OK Reason = iota
	AcctLimit
	GroupLimit
	DayLimit
)

// key 为 (账户, 合约) 组合键。
type key struct {
	acct string
	sym  string
}

// Book 为敞口账本（非并发安全，由 gate 加锁）。
type Book struct {
	recs    map[key]*Rec
	gexp    map[gkey]int64 // (组, 合约) -> 组敞口
	touched int
}

type gkey struct {
	group string
	sym   string
}

// NewBook 创建空账本。
func NewBook() *Book {
	return &Book{recs: map[key]*Rec{}, gexp: map[gkey]int64{}}
}

// Touched 返回上次变更操作触碰的账户记录数。
func (b *Book) Touched() int { return b.touched }

// GroupExp 返回某组某合约的组敞口。
func (b *Book) GroupExp(group, sym []byte) int64 {
	return b.gexp[gkey{string(group), string(sym)}]
}

// AcctExp 返回账户某合约某边敞口 e=持仓+在途开仓。
func (b *Book) AcctExp(acct, sym []byte, side limit.Side) int64 {
	r := b.recs[key{string(acct), string(sym)}]
	if r == nil {
		return 0
	}
	i := sideIdx(side)
	return r.Pos[i] + r.Open[i]
}

// DayOpen 返回账户某合约日内开仓量 o=已成交开仓+在途开仓（多空合计）。
func (b *Book) DayOpen(acct, sym []byte) int64 {
	r := b.recs[key{string(acct), string(sym)}]
	if r == nil {
		return 0
	}
	return r.DayFilled + r.Open[0] + r.Open[1]
}

// Closeable 返回账户某合约某边可平量=持仓-在途平仓。
func (b *Book) Closeable(acct, sym []byte, side limit.Side) int64 {
	r := b.recs[key{string(acct), string(sym)}]
	if r == nil {
		return 0
	}
	i := sideIdx(side)
	return r.Pos[i] - r.Close[i]
}

// Rec 返回账户合约状态快照（不存在时返回零值）。
func (b *Book) Rec(acct, sym []byte) Rec {
	if r := b.recs[key{string(acct), string(sym)}]; r != nil {
		return *r
	}
	return Rec{}
}

// CheckOpen 预检开仓 qty 是否同时满足账户、组、日内三项限额（取等通过）。
// 只读，不触碰任何账户记录。
func (b *Book) CheckOpen(acct, group, sym []byte, side limit.Side, qty, acctLim, groupLim, dayOpen int64) Reason {
	i := sideIdx(side)
	k := key{string(acct), string(sym)}
	r := b.recs[k]
	var e, h, dayFilled int64
	if r != nil {
		e = r.Pos[i] + r.Open[i]
		h = r.Hedge[i]
		dayFilled = r.DayFilled + r.Open[0] + r.Open[1]
	}
	if e+qty > acctLim+h {
		return AcctLimit
	}
	oldContrib := int64(0)
	if r != nil {
		oldContrib = r.Contrib[i]
	}
	newContrib := max64(0, e+qty-h)
	g := b.gexp[gkey{string(group), string(sym)}]
	if g-oldContrib+newContrib > groupLim {
		return GroupLimit
	}
	if dayFilled+qty > dayOpen {
		return DayLimit
	}
	return OK
}

// AcceptOpen 接受开仓委托：增加该边在途开仓与日内在途部分，增量更新组贡献。
func (b *Book) AcceptOpen(acct, group, sym []byte, side limit.Side, qty int64) {
	r := b.touch(acct, group, sym)
	i := sideIdx(side)
	r.Open[i] += qty
	b.refreshContrib(r, group, sym, i)
}

// AcceptClose 接受平仓委托：增加该边在途平仓（占用可平量，不减敞口）。
func (b *Book) AcceptClose(acct, group, sym []byte, side limit.Side, qty int64) {
	r := b.touch(acct, group, sym)
	r.Close[sideIdx(side)] += qty
}

// FillOpen 开仓成交 fillQty：在途开仓转持仓，并计入当日已成交开仓（日内总量不变）。
func (b *Book) FillOpen(acct, group, sym []byte, side limit.Side, fillQty int64) {
	r := b.touch(acct, group, sym)
	i := sideIdx(side)
	r.Open[i] -= fillQty
	r.Pos[i] += fillQty
	r.DayFilled += fillQty
	// e=持仓+在途开仓 不变，组贡献不变。
}

// FillClose 平仓成交 fillQty：同减该边持仓与在途平仓，敞口下降并增量更新组贡献。
func (b *Book) FillClose(acct, group, sym []byte, side limit.Side, fillQty int64) {
	r := b.touch(acct, group, sym)
	i := sideIdx(side)
	r.Close[i] -= fillQty
	r.Pos[i] -= fillQty
	b.refreshContrib(r, group, sym, i)
}

// CancelOpen 撤销开仓委托残余量：释放在途开仓，日内在途部分同步释放。
func (b *Book) CancelOpen(acct, group, sym []byte, side limit.Side, qty int64) {
	r := b.touch(acct, group, sym)
	i := sideIdx(side)
	r.Open[i] -= qty
	b.refreshContrib(r, group, sym, i)
}

// CancelClose 撤销平仓委托残余量：释放在途平仓。
func (b *Book) CancelClose(acct, group, sym []byte, side limit.Side, qty int64) {
	r := b.touch(acct, group, sym)
	r.Close[sideIdx(side)] -= qty
}

// SetHedge 设置账户合约某边套保额度（始终接受），并重算该账户该边组贡献。
func (b *Book) SetHedge(acct, group, sym []byte, side limit.Side, hedge int64) {
	r := b.touch(acct, group, sym)
	i := sideIdx(side)
	r.Hedge[i] = hedge
	b.refreshContrib(r, group, sym, i)
}

// ResetDay 清零所有账户的当日已成交开仓部分，在途保留。
// 不设 touched 语义（需遍历全部记录，属于日切而非逐笔路径）。
func (b *Book) ResetDay() {
	for _, r := range b.recs {
		r.DayFilled = 0
	}
}

// touch 获取（必要时创建）唯一一条账户合约记录，并把本操作触碰计数置为 1。
func (b *Book) touch(acct, group, sym []byte) *Rec {
	b.touched = 1
	k := key{string(acct), string(sym)}
	r := b.recs[k]
	if r == nil {
		r = &Rec{}
		b.recs[k] = r
	}
	return r
}

// refreshContrib 重算单账户单合约单边的组贡献，并增量调整组敞口。
func (b *Book) refreshContrib(r *Rec, group, sym []byte, i int) {
	gk := gkey{string(group), string(sym)}
	b.gexp[gk] -= r.Contrib[i]
	e := r.Pos[i] + r.Open[i]
	r.Contrib[i] = max64(0, e-r.Hedge[i])
	b.gexp[gk] += r.Contrib[i]
}

func sideIdx(side limit.Side) int {
	if side == limit.Short {
		return 1
	}
	return 0
}

func max64(a, c int64) int64 {
	if a > c {
		return a
	}
	return c
}

// RecEntry 为单账户单合约记录的快照条目。
type RecEntry struct {
	Acct []byte
	Sym  []byte
	Rec  Rec
}

// GroupExpEntry 为 (组, 合约) 组敞口快照条目。
type GroupExpEntry struct {
	Group []byte
	Sym   []byte
	Exp   int64
}

// AllRecs 返回全部账户合约记录快照（测试/全量对账用，非热路径）。
func (b *Book) AllRecs() []RecEntry {
	out := make([]RecEntry, 0, len(b.recs))
	for k, r := range b.recs {
		out = append(out, RecEntry{Acct: []byte(k.acct), Sym: []byte(k.sym), Rec: *r})
	}
	return out
}

// AllGroupExps 返回全部 (组, 合约) 组敞口快照。
func (b *Book) AllGroupExps() []GroupExpEntry {
	out := make([]GroupExpEntry, 0, len(b.gexp))
	for k, v := range b.gexp {
		out = append(out, GroupExpEntry{Group: []byte(k.group), Sym: []byte(k.sym), Exp: v})
	}
	return out
}

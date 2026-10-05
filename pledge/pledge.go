// Package pledge 维护质押库：账户的可用/在库持仓、现金，以及标准券额度
// （Cap）与占用（Use）的增量维护。Cap 恒等于按定义从在库明细重算的值，
// 但所有改动只触碰与本次操作直接相关的在库债券记录或账户。
package pledge

import (
	"sort"

	"ontology/haircut"
)

// Position 为账户在某一债券上的持仓。
type Position struct {
	Avail   int64 // 可用
	Pledged int64 // 在库（已质押）
}

// Account 为账户状态。Cap 为标准券额度，Use 为未了结回购的占用。
type Account struct {
	Cash int64
	Cap  int64
	Use  int64
	Pos  map[string]*Position
}

// Deficit 为欠库账户及其缺口 Use-Cap。
type Deficit struct {
	Acct string
	Gap  int64
}

// Library 为质押库。holders 为 bond→在库持有该券的账户集合索引，
// 使折算率调整只需触碰真正受影响的账户。
type Library struct {
	accts   map[string]*Account
	holders map[string]map[string]struct{}
	touched int64 // 额度检查/维护触碰的在库债券记录或账户数（测试用）
}

func NewLibrary() *Library {
	return &Library{
		accts:   make(map[string]*Account),
		holders: make(map[string]map[string]struct{}),
	}
}

func (l *Library) Get(acct string) (*Account, bool) {
	a, ok := l.accts[acct]
	return a, ok
}

func (l *Library) ensure(acct string) *Account {
	a, ok := l.accts[acct]
	if !ok {
		a = &Account{Pos: make(map[string]*Position)}
		l.accts[acct] = a
	}
	return a
}

// Credit 增加可用持仓，账户不存在则建立。
func (l *Library) Credit(acct, bond string, n int64) {
	a := l.ensure(acct)
	p := a.Pos[bond]
	if p == nil {
		p = &Position{}
		a.Pos[bond] = p
	}
	p.Avail += n
}

// CreditCash 增加现金，账户不存在则建立。
func (l *Library) CreditCash(acct string, amt int64) {
	l.ensure(acct).Cash += amt
}

// HasCapacity 报告账户再增加 extra 占用后是否仍满足 Use<=Cap。
// 只读账户的增量维护值，不触碰任何在库债券记录。
func (l *Library) HasCapacity(acct string, extra int64) bool {
	a := l.accts[acct]
	return a.Use+extra <= a.Cap
}

func (l *Library) AddUse(acct string, u int64) {
	l.accts[acct].Use += u
}

func (l *Library) ReleaseUse(acct string, u int64) {
	l.accts[acct].Use -= u
}

// PledgeIn 把 n 张从可用转入在库，可用不足报 ErrInsufficientAvail。
// 欠库账户不受限制。额度按单券贡献差值增量调整。
func (l *Library) PledgeIn(acct, bond string, n, rate int64) error {
	a := l.accts[acct]
	p := a.Pos[bond]
	if p == nil || p.Avail < n {
		return haircut.ErrInsufficientAvail
	}
	a.Cap += haircut.Contrib(p.Pledged+n, rate) - haircut.Contrib(p.Pledged, rate)
	p.Avail -= n
	p.Pledged += n
	l.track(bond, acct)
	return nil
}

// PledgeOut 把 n 张从在库转回可用。在库不足报 ErrInsufficientStock；
// 出库后额度须不小于占用，否则报 ErrInsufficientCap。额度检查只触碰
// 本券一条在库记录。
func (l *Library) PledgeOut(acct, bond string, n, rate int64) error {
	a := l.accts[acct]
	p := a.Pos[bond]
	l.touched++
	var pledged int64
	if p != nil {
		pledged = p.Pledged
	}
	if pledged < n {
		return haircut.ErrInsufficientStock
	}
	newCap := a.Cap - haircut.Contrib(pledged, rate) + haircut.Contrib(pledged-n, rate)
	if newCap < a.Use {
		return haircut.ErrInsufficientCap
	}
	p.Pledged -= n
	p.Avail += n
	a.Cap = newCap
	l.untrack(bond, acct, p)
	return nil
}

// Retrate 在折算率由 oldRate 调整为 newRate 后，只重算在库持有该券的账户。
func (l *Library) Retrate(bond string, oldRate, newRate int64) {
	for acct := range l.holders[bond] {
		l.touched++
		a := l.accts[acct]
		n := a.Pos[bond].Pledged
		a.Cap += haircut.Contrib(n, newRate) - haircut.Contrib(n, oldRate)
	}
}

// Deficits 按账户字节序列出全部欠库账户及缺口 Use-Cap。
func (l *Library) Deficits() []Deficit {
	var out []Deficit
	for name, a := range l.accts {
		if a.Cap < a.Use {
			out = append(out, Deficit{Acct: name, Gap: a.Use - a.Cap})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Acct < out[j].Acct })
	return out
}

// PledgedBonds 按债券代码字节序列出账户在库大于零的债券（违约处置顺序）。
func (l *Library) PledgedBonds(acct string) []string {
	a := l.accts[acct]
	var out []string
	for bond, p := range a.Pos {
		if p.Pledged > 0 {
			out = append(out, bond)
		}
	}
	sort.Strings(out)
	return out
}

// RemovePledged 违约处置卖出在库债券：只减少在库与额度，不回到可用。
func (l *Library) RemovePledged(acct, bond string, n, rate int64) {
	a := l.accts[acct]
	p := a.Pos[bond]
	a.Cap += haircut.Contrib(p.Pledged-n, rate) - haircut.Contrib(p.Pledged, rate)
	p.Pledged -= n
	l.untrack(bond, acct, p)
}

func (l *Library) track(bond, acct string) {
	s := l.holders[bond]
	if s == nil {
		s = make(map[string]struct{})
		l.holders[bond] = s
	}
	s[acct] = struct{}{}
}

func (l *Library) untrack(bond, acct string, p *Position) {
	if p.Pledged == 0 {
		delete(l.holders[bond], acct)
	}
}

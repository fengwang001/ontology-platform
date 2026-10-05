// Package holding 维护账户账本：每个账户每个标的的总持仓 q、冻结 f
// （可用为 q-f，恒有 0<=f<=q），以及可用现金与冻结现金。
// 账户在首次 Deposit 或买入 Trade 时建立。
package holding

import (
	"errors"
	"sync"
)

// 哨兵错误，供 errors.Is 区分拒绝类别。
var (
	ErrInvalidParam = errors.New("holding: invalid parameter")
	ErrDateRollback = errors.New("holding: date rollback")
	ErrDuplicate    = errors.New("holding: duplicate id")
	ErrNotExist     = errors.New("holding: not exist")
	ErrState        = errors.New("holding: bad state")
	ErrConflict     = errors.New("holding: conflict")
	ErrNoRefPrice   = errors.New("holding: no reference price")
	ErrInsufficient = errors.New("holding: insufficient")
)

const (
	// MaxDeposit 为单次入金上限（含）。
	MaxDeposit = int64(1_000_000_000_000)
	// MaxDelta 为单次成交数量的绝对值上限（含）。
	MaxDelta = int64(1_000_000_000)
)

// Pos 为某标的的持仓：Q 总持仓，F 冻结，可用为 Q-F。
type Pos struct {
	Q int64
	F int64
}

// AccountState 为账户状态快照（深拷贝用）。
type AccountState struct {
	Cash       int64
	FrozenCash int64
	Positions  map[string]Pos
}

type account struct {
	cash     int64
	frCash   int64
	position map[string]*Pos
}

// Book 为账户账本，所有方法可并发调用。
type Book struct {
	mu       sync.Mutex
	accounts map[string]*account
}

// NewBook 创建空账本。
func NewBook() *Book {
	return &Book{accounts: make(map[string]*account)}
}

func (b *Book) getOrCreate(acct string) *account {
	a, ok := b.accounts[acct]
	if !ok {
		a = &account{position: make(map[string]*Pos)}
		b.accounts[acct] = a
	}
	return a
}

// Deposit 增加账户可用现金，cash 取值 1..1e12；账户不存在时建立。
func (b *Book) Deposit(acct string, cash int64) error {
	if acct == "" || cash < 1 || cash > MaxDeposit {
		return ErrInvalidParam
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.getOrCreate(acct).cash += cash
	return nil
}

// Trade 记一笔成交，delta 非零且 |delta|<=1e9；正为买入（账户不存在时建立），
// 负为卖出（账户须存在且可用股数足够）。不触碰现金。
func (b *Book) Trade(acct, sym string, delta int64) error {
	if acct == "" || sym == "" || delta == 0 || delta > MaxDelta || delta < -MaxDelta {
		return ErrInvalidParam
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	a, ok := b.accounts[acct]
	if delta < 0 {
		if !ok {
			return ErrNotExist
		}
		p := a.position[sym]
		var avail int64
		if p != nil {
			avail = p.Q - p.F
		}
		if avail < -delta {
			return ErrInsufficient
		}
		p.Q += delta
		if p.Q == 0 && p.F == 0 {
			delete(a.position, sym)
		}
		return nil
	}
	if !ok {
		a = b.getOrCreate(acct)
	}
	p := a.position[sym]
	if p == nil {
		p = &Pos{}
		a.position[sym] = p
	}
	p.Q += delta
	return nil
}

// Freeze 把 n 股从可用移入冻结，n>=1；不得超过可用股数。
func (b *Book) Freeze(acct, sym string, n int64) error {
	if acct == "" || sym == "" || n < 1 {
		return ErrInvalidParam
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	a, ok := b.accounts[acct]
	if !ok {
		return ErrNotExist
	}
	p := a.position[sym]
	var avail int64
	if p != nil {
		avail = p.Q - p.F
	}
	if n > avail {
		return ErrInsufficient
	}
	p.F += n
	return nil
}

// Unfreeze 把 n 股从冻结移回可用，n>=1；不得超过冻结股数。
func (b *Book) Unfreeze(acct, sym string, n int64) error {
	if acct == "" || sym == "" || n < 1 {
		return ErrInvalidParam
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	a, ok := b.accounts[acct]
	if !ok {
		return ErrNotExist
	}
	p := a.position[sym]
	var frozen int64
	if p != nil {
		frozen = p.F
	}
	if n > frozen {
		return ErrInsufficient
	}
	p.F -= n
	return nil
}

// SnapshotSymbol 返回该标的当前 q>0 的账户持仓快照（仅这些账户会被除权触碰）。
func (b *Book) SnapshotSymbol(sym string) map[string]Pos {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make(map[string]Pos)
	for name, a := range b.accounts {
		if p, ok := a.position[sym]; ok && p.Q > 0 {
			out[name] = Pos{Q: p.Q, F: p.F}
		}
	}
	return out
}

// Credit 为除权入账：总持仓 +=addQ，冻结 +=addF，
// 可用现金 +=addCash，冻结现金 +=addFrozenCash。
func (b *Book) Credit(acct, sym string, addQ, addF, addCash, addFrozenCash int64) {
	b.mu.Lock()
	defer b.mu.Unlock()
	a := b.getOrCreate(acct)
	p := a.position[sym]
	if p == nil {
		p = &Pos{}
		a.position[sym] = p
	}
	p.Q += addQ
	p.F += addF
	a.cash += addCash
	a.frCash += addFrozenCash
}

// Position 返回账户某标的的持仓；账户或标的不存在时 ok=false。
func (b *Book) Position(acct, sym string) (q, f int64, ok bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	a, exists := b.accounts[acct]
	if !exists {
		return 0, 0, false
	}
	p, exists := a.position[sym]
	if !exists {
		return 0, 0, false
	}
	return p.Q, p.F, true
}

// Cash 返回账户可用与冻结现金；账户不存在时 ok=false。
func (b *Book) Cash(acct string) (avail, frozen int64, ok bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	a, exists := b.accounts[acct]
	if !exists {
		return 0, 0, false
	}
	return a.cash, a.frCash, true
}

// Dump 返回全部账户状态的深拷贝（测试与对账用）。
func (b *Book) Dump() map[string]AccountState {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make(map[string]AccountState, len(b.accounts))
	for name, a := range b.accounts {
		st := AccountState{
			Cash:       a.cash,
			FrozenCash: a.frCash,
			Positions:  make(map[string]Pos, len(a.position)),
		}
		for sym, p := range a.position {
			st.Positions[sym] = Pos{Q: p.Q, F: p.F}
		}
		out[name] = st
	}
	return out
}

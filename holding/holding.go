// Package holding 管理账户的现金（可用/冻结）、每股持仓（总/冻结）与收盘价。
package holding

import "errors"

var (
	ErrInvalidParam = errors.New("holding: invalid parameter")
	ErrNoAccount    = errors.New("holding: account not found")
	ErrInsufficient = errors.New("holding: insufficient holding")
)

// Pos 是某账户在某标的上的持仓快照。
type Pos struct {
	Q int64
	F int64
}

type accountInfo struct {
	cashAvail int64
	cashFroz  int64
	pos       map[string]Pos
}

// Book 自身不加锁，由上层 adjust.Engine 统一加锁。
type Book struct {
	accts   map[string]*accountInfo
	closes  map[string]int64
	touched int
}

func NewBook() *Book {
	return &Book{
		accts:  map[string]*accountInfo{},
		closes: map[string]int64{},
	}
}

func (b *Book) HasAcct(account []byte) bool {
	_, ok := b.accts[string(account)]
	return ok
}

// EnsureAcct 在首次 Trade 时建立账户。
func (b *Book) EnsureAcct(account []byte) {
	if _, ok := b.accts[string(account)]; !ok {
		b.accts[string(account)] = &accountInfo{pos: map[string]Pos{}}
	}
}

// Deposit 增加可用现金；账户在首次 Deposit 时建立。
func (b *Book) Deposit(account []byte, cash int64) {
	a := b.accts[string(account)]
	if a == nil {
		a = &accountInfo{pos: map[string]Pos{}}
		b.accts[string(account)] = a
	}
	a.cashAvail += cash
}

// Trade 处理成交后的持仓变动，delta>0 买入，delta<0 卖出（要求可用股数足够）。
func (b *Book) Trade(acctName, sym []byte, delta int64) error {
	a := b.accts[string(acctName)]
	if a == nil {
		return ErrNoAccount
	}
	s := string(sym)
	p := a.pos[s]
	if delta < 0 {
		avail := p.Q - p.F
		if -delta > avail {
			return ErrInsufficient
		}
	}
	p.Q += delta
	a.pos[s] = p
	return nil
}

// Freeze 将可用股数转入冻结。
func (b *Book) Freeze(acctName, sym []byte, n int64) error {
	a := b.accts[string(acctName)]
	if a == nil {
		return ErrNoAccount
	}
	p := a.pos[string(sym)]
	if n > p.Q-p.F {
		return ErrInsufficient
	}
	p.F += n
	a.pos[string(sym)] = p
	return nil
}

// Unfreeze 将冻结股数转回可用。
func (b *Book) Unfreeze(acctName, sym []byte, n int64) error {
	a := b.accts[string(acctName)]
	if a == nil {
		return ErrNoAccount
	}
	p := a.pos[string(sym)]
	if n > p.F {
		return ErrInsufficient
	}
	p.F -= n
	a.pos[string(sym)] = p
	return nil
}

func (b *Book) SetClose(sym []byte, price int64) {
	b.closes[string(sym)] = price
}

func (b *Book) Close(sym []byte) (int64, bool) {
	p, ok := b.closes[string(sym)]
	return p, ok
}

func (b *Book) HasClose(sym []byte) bool {
	_, ok := b.closes[string(sym)]
	return ok
}

// Cash 返回可用与冻结现金；账户不存在返回 ErrNoAccount。
func (b *Book) Cash(acctName []byte) (avail, frozen int64, err error) {
	a := b.accts[string(acctName)]
	if a == nil {
		return 0, 0, ErrNoAccount
	}
	return a.cashAvail, a.cashFroz, nil
}

// Position 返回总持仓与冻结；账户不存在返回 ErrNoAccount，无持仓返回零值。
func (b *Book) Position(acctName, sym []byte) (q, f int64, err error) {
	a := b.accts[string(acctName)]
	if a == nil {
		return 0, 0, ErrNoAccount
	}
	p := a.pos[string(sym)]
	return p.Q, p.F, nil
}

// SnapshotNamed 返回该标的上 q>0 的全部账户名（字节序）及其持仓。
func (b *Book) SnapshotNamed(sym []byte) ([]string, []Pos) {
	s := string(sym)
	var names []string
	for name, a := range b.accts {
		if p := a.pos[s]; p.Q > 0 {
			names = append(names, name)
		}
	}
	sortStrings(names)
	out := make([]Pos, 0, len(names))
	for _, name := range names {
		out = append(out, b.accts[name].pos[s])
	}
	return names, out
}

// Award 是单个账户的除权所得。
type Award struct {
	Shares     int64 // 送股总数 n
	SharesFroz int64 // 其中冻结 nf
	Cash       int64 // 现金总额 m
	CashFroz   int64 // 其中冻结现金 mf
	FragCash   int64 // 碎股折现（计入可用现金）
}

// Apply 对快照中 q>0 的账户（accts 须已按字节序排列）应用除权分配，
// 每触碰一个账户记录，touched 自增一次。
func (b *Book) Apply(sym []byte, accts []string, snap []Pos, c, bb, pex int64) []Award {
	s := string(sym)
	awards := make([]Award, 0, len(accts))
	for i, name := range accts {
		p := snap[i]
		a := b.accts[name]
		if a == nil {
			continue
		}
		b.touched++
		q, f := p.Q, p.F
		n := q * bb / 10
		nf := f * bb / 10
		m := q * c / 10
		mf := f * c / 10
		frag := (q * bb) % 10 * pex / 10
		pos := a.pos[s]
		pos.Q += n
		pos.F += nf
		a.pos[s] = pos
		a.cashAvail += m - mf + frag
		a.cashFroz += mf
		awards = append(awards, Award{
			Shares:     n,
			SharesFroz: nf,
			Cash:       m,
			CashFroz:   mf,
			FragCash:   frag,
		})
	}
	return awards
}

// Touched 返回自上次 ResetTouched 以来 Apply 触碰的账户记录数。
func (b *Book) Touched() int { return b.touched }

func (b *Book) ResetTouched() { b.touched = 0 }

func sortStrings(x []string) {
	// 字节序排序，避免引入 sort 包的接口样板。
	for i := 1; i < len(x); i++ {
		for j := i; j > 0 && x[j-1] > x[j]; j-- {
			x[j-1], x[j] = x[j], x[j-1]
		}
	}
}

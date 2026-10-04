// Package balance 维护账户余额与在途预留（reservation）。
// 所有金额均为 int64；单位用量对应的金额由调用方按单价算出。
package balance

import "sync"

// Reservation 是一笔在途预留：units 个单位、按 rate 单价占用，到期时刻 expire（毫秒）。
// now < expire 时预留有效；now == expire 即到期。released 表示已被结算提前释放。
type Reservation struct {
	Acct     string
	Units    int64
	Rate     int64
	Expire   int64
	released bool
}

// Alive 是到期的纯函数：恰等即到期；已释放亦不再占用。
func (r *Reservation) Alive(now int64) bool {
	return r != nil && !r.released && now < r.Expire
}

// Ledger 是线程安全的账户账本。
type Ledger struct {
	mu sync.RWMutex

	// accts 持有每个账户的钱账与全部预留记录（到期记录保留至结算 Forget）。
	accts map[string]*account
}

type account struct {
	balance  int64
	totalIn  int64
	charged  int64
	reserved []*Reservation
}

// NewLedger 创建空账本。
func NewLedger() *Ledger {
	return &Ledger{accts: make(map[string]*account)}
}

func (l *Ledger) get(acct string) *account {
	a := l.accts[acct]
	if a == nil {
		a = &account{}
		l.accts[acct] = a
	}
	return a
}

// Ensure 建立账户（余额 0 起），已存在则无操作。
func (l *Ledger) Ensure(acct string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.get(acct)
}

// Exists 返回账户是否已建立。
func (l *Ledger) Exists(acct string) bool {
	l.mu.RLock()
	defer l.mu.RUnlock()
	_, ok := l.accts[acct]
	return ok
}

// TopUp 给余额加款并累计加款总额。
func (l *Ledger) TopUp(acct string, amount int64) {
	l.mu.Lock()
	defer l.mu.Unlock()
	a := l.get(acct)
	a.balance += amount
	a.totalIn += amount
}

// Charge 从余额扣 amount；余额不足时不扣并返回 false（调用方应改扣更小金额）。
func (l *Ledger) Charge(acct string, amount int64) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	a := l.accts[acct]
	if a == nil || a.balance < amount {
		return false
	}
	a.balance -= amount
	a.charged += amount
	return true
}

// Hold 登记一笔在途预留（调用方须已确认金额可承担）。
func (l *Ledger) Hold(acct string, units, rate, expire int64) *Reservation {
	l.mu.Lock()
	defer l.mu.Unlock()
	a := l.get(acct)
	res := &Reservation{Acct: acct, Units: units, Rate: rate, Expire: expire}
	a.reserved = append(a.reserved, res)
	return res
}

// Release 释放一笔尚未到期的预留；已到期或已释放则无操作。返回实际释放金额。
func (l *Ledger) Release(res *Reservation, now int64) int64 {
	if res == nil {
		return 0
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if !res.Alive(now) {
		return 0
	}
	res.released = true
	return res.Units * res.Rate
}

// Forget 彻底移除一笔预留记录（结算完成后调用，到期与否皆可）。
func (l *Ledger) Forget(res *Reservation) {
	if res == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	a := l.accts[res.Acct]
	if a == nil {
		return
	}
	for i, r := range a.reserved {
		if r == res {
			a.reserved = append(a.reserved[:i], a.reserved[i+1:]...)
			return
		}
	}
}

// Balance 返回账户余额。
func (l *Ledger) Balance(acct string) int64 {
	l.mu.RLock()
	defer l.mu.RUnlock()
	if a := l.accts[acct]; a != nil {
		return a.balance
	}
	return 0
}

// Held 返回账户在 now 时刻未到期预留占用的金额之和。
func (l *Ledger) Held(acct string, now int64) int64 {
	l.mu.RLock()
	defer l.mu.RUnlock()
	a := l.accts[acct]
	if a == nil {
		return 0
	}
	var sum int64
	for _, r := range a.reserved {
		if r.Alive(now) {
			sum += r.Units * r.Rate
		}
	}
	return sum
}

// Free 返回可用额：余额 − 未到期预留之和。
func (l *Ledger) Free(acct string, now int64) int64 {
	l.mu.RLock()
	defer l.mu.RUnlock()
	a := l.accts[acct]
	if a == nil {
		return 0
	}
	var held int64
	for _, r := range a.reserved {
		if r.Alive(now) {
			held += r.Units * r.Rate
		}
	}
	return a.balance - held
}

// TotalIn 返回累计加款；TotalCharged 返回累计扣费（守恒式校验用）。
func (l *Ledger) TotalIn(acct string) int64 {
	l.mu.RLock()
	defer l.mu.RUnlock()
	if a := l.accts[acct]; a != nil {
		return a.totalIn
	}
	return 0
}

func (l *Ledger) TotalCharged(acct string) int64 {
	l.mu.RLock()
	defer l.mu.RUnlock()
	if a := l.accts[acct]; a != nil {
		return a.charged
	}
	return 0
}

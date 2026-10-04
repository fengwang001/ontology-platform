package balance

import (
	"container/heap"
	"errors"
	"sort"
	"sync"
)

var (
	ErrInvalidArgument = errors.New("invalid argument")
	ErrClockBack       = errors.New("clock moved backwards")
	ErrNoGrant         = errors.New("usage reported without grant")
)

type Key struct {
	Session   string
	RateGroup int
}

type Reservation struct {
	Units    int64
	Price    int64
	ExpireAt int64
	expired  bool
	heapIdx  int
}

type Ledger struct {
	mu       sync.Mutex
	accounts map[string]*account
}

type account struct {
	balance      int64
	reserved     int64
	chargedTotal int64
	maxNow       int64
	grants       map[Key]*Reservation
	heap         []*Reservation
}

func New() *Ledger {
	return &Ledger{accounts: make(map[string]*account)}
}

func (l *Ledger) TopUp(acct string, amount int64, now int64) error {
	if acct == "" || amount < 1 || amount > 1_000_000_000_000 || now < 0 || now > 1_000_000_000_000 {
		return ErrInvalidArgument
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	a := l.ensure(acct)
	if now < a.maxNow {
		return ErrClockBack
	}
	a.maxNow = now
	a.balance += amount
	return nil
}

func (l *Ledger) Ensure(acct string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.ensure(acct)
}

func (l *Ledger) HasGrant(acct string, key Key, now int64) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	a := l.accounts[acct]
	if a == nil {
		return false
	}
	a.expire(now)
	_, ok := a.grants[key]
	return ok
}

func (l *Ledger) Exists(acct string, key Key) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	a := l.accounts[acct]
	if a == nil {
		return false
	}
	_, ok := a.grants[key]
	return ok
}

func (l *Ledger) Free(acct string, now int64) (int64, int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	a := l.accounts[acct]
	if a == nil {
		return 0, 0
	}
	expired := a.expire(now)
	return a.balance - a.reserved, expired
}

func (l *Ledger) Settle(acct string, key Key, used int64, now int64) (charged int64, chargedAmount int64, existed bool, touched int) {
	if used < 0 || used > 1_000_000_000 || now < 0 || now > 1_000_000_000_000 {
		return 0, 0, false, 0
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	a := l.accounts[acct]
	if a == nil {
		return 0, 0, false, 0
	}
	touched += a.expire(now)
	charged, chargedAmount, existed, recordTouched := l.settleLocked(a, key, used)
	return charged, chargedAmount, existed, touched + recordTouched
}

func (l *Ledger) Grant(acct string, key Key, want int64, smax int64, lmin int64, price int64, now int64, validity int64) (units int64, affordable int64, expireAt int64, touched int, err error) {
	if want < 1 || want > 1_000_000_000 || smax < 1 || smax > 1_000_000 || lmin < 1 || lmin > smax ||
		price < 1 || price > 1_000_000 || now < 0 || now > 1_000_000_000_000 || validity < 1 || validity > 1_000_000_000 {
		return 0, 0, 0, 0, ErrInvalidArgument
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	a := l.accounts[acct]
	if a == nil {
		return 0, 0, 0, 0, ErrInvalidArgument
	}
	touched += a.expire(now)
	free := a.balance - a.reserved
	affordable = free / price
	base := min(want, min(smax, affordable))
	units = base
	if change := affordable - base; change > 0 && change < lmin {
		units = affordable
	}
	if units == 0 {
		return 0, affordable, 0, touched + 1, nil
	}
	record := &Reservation{Units: units, Price: price, ExpireAt: now + validity, heapIdx: -1}
	a.grants[key] = record
	heap.Push(a, record)
	a.reserved += units * price
	return units, affordable, record.ExpireAt, touched + 1, nil
}

func (l *Ledger) SettleAll(acct string, session string, usedByGroup map[int]int64, now int64) (chargedAmount int64, touched int, err error) {
	if now < 0 || now > 1_000_000_000_000 {
		return 0, 0, ErrInvalidArgument
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	a := l.accounts[acct]
	if a == nil {
		return 0, 0, ErrInvalidArgument
	}
	for rg, used := range usedByGroup {
		if rg < 1 || rg > 1000 || used < 0 || used > 1_000_000_000 {
			return 0, 0, ErrInvalidArgument
		}
		if a.grants[Key{Session: session, RateGroup: rg}] == nil {
			return 0, 0, ErrNoGrant
		}
	}
	groups := make(map[int]int64)
	for key := range a.grants {
		if key.Session == session {
			groups[key.RateGroup] = usedByGroup[key.RateGroup]
		}
	}
	ordered := make([]int, 0, len(groups))
	for rg := range groups {
		ordered = append(ordered, rg)
	}
	sort.Ints(ordered)
	touched += a.expire(now)
	for _, rg := range ordered {
		_, amount, _, recordTouched := l.settleLocked(a, Key{Session: session, RateGroup: rg}, groups[rg])
		chargedAmount += amount
		touched += recordTouched
	}
	return chargedAmount, touched, nil
}

func (l *Ledger) ChargedTotal(acct string) int64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	a := l.accounts[acct]
	if a == nil {
		return 0
	}
	return a.chargedTotal
}

func (l *Ledger) Balance(acct string) int64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	a := l.accounts[acct]
	if a == nil {
		return 0
	}
	return a.balance
}

func (l *Ledger) Reserved(acct string) int64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	a := l.accounts[acct]
	if a == nil {
		return 0
	}
	return a.reserved
}

func (l *Ledger) ensure(acct string) *account {
	a := l.accounts[acct]
	if a == nil {
		a = &account{grants: make(map[Key]*Reservation)}
		l.accounts[acct] = a
	}
	return a
}

func (l *Ledger) settleLocked(a *account, key Key, used int64) (int64, int64, bool, int) {
	record := a.grants[key]
	if record == nil {
		return 0, 0, false, 0
	}
	if !record.expired {
		a.removeFromHeap(record)
	}
	free := a.balance - a.reserved
	charged := min(used, free/record.Price)
	amount := charged * record.Price
	a.balance -= amount
	a.chargedTotal += amount
	delete(a.grants, key)
	return charged, amount, true, 1
}

func (a *account) expire(now int64) int {
	count := 0
	for len(a.heap) > 0 && a.heap[0].ExpireAt <= now {
		record := heap.Pop(a).(*Reservation)
		a.release(record)
		count++
	}
	return count
}

func (a *account) release(record *Reservation) {
	a.reserved -= record.Units * record.Price
	a.markExpired(record)
}

func (a *account) markExpired(record *Reservation) {
	record.expired = true
}

func (a *account) removeFromHeap(record *Reservation) {
	if record.heapIdx >= 0 {
		heap.Remove(a, record.heapIdx)
	}
	a.reserved -= record.Units * record.Price
	a.markExpired(record)
}

func (a *account) Len() int { return len(a.heap) }

func (a *account) Less(i int, j int) bool {
	if a.heap[i].ExpireAt != a.heap[j].ExpireAt {
		return a.heap[i].ExpireAt < a.heap[j].ExpireAt
	}
	if a.heap[i].Price != a.heap[j].Price {
		return a.heap[i].Price < a.heap[j].Price
	}
	return a.heap[i].Units < a.heap[j].Units
}

func (a *account) Swap(i int, j int) {
	a.heap[i], a.heap[j] = a.heap[j], a.heap[i]
	a.heap[i].heapIdx = i
	a.heap[j].heapIdx = j
}

func (a *account) Push(value any) {
	record := value.(*Reservation)
	record.heapIdx = len(a.heap)
	a.heap = append(a.heap, record)
}

func (a *account) Pop() any {
	last := len(a.heap) - 1
	record := a.heap[last]
	record.heapIdx = -1
	a.heap = a.heap[:last]
	return record
}

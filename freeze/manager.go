// Package freeze implements judicial freeze and queue (轮候) freeze management
// for bank accounts.
package freeze

import (
	"errors"
	"sync"
)

const (
	// MaxTime is the inclusive upper bound for every operation/query timestamp.
	MaxTime int64 = 1_000_000_000_000
	// MaxAmount bounds every order nominal amount and every per-call amount x.
	MaxAmount int64 = 1_000_000_000_000
	// MaxBalance bounds an account balance after any deposit.
	MaxBalance int64 = 1_000_000_000_000_000
)

// Reason classifies every rejection reported by the manager.
type Reason string

const (
	ReasonInvalidParam   Reason = "invalid parameter"
	ReasonTimeRegression Reason = "time regression"
	ReasonAccountMissing Reason = "account not found"
	ReasonIDDuplicate    Reason = "freeze order id duplicate"
	ReasonOrderMissing   Reason = "freeze order not found"
	ReasonUnfreezeOver   Reason = "unfreeze exceeds order nominal amount"
	ReasonSeizeOver      Reason = "seize exceeds effective freeze amount"
	ReasonSeizeQOver     Reason = "seize-queue exceeds total effective freeze amount"
	ReasonAvailableShort Reason = "available balance insufficient"
)

// Error is the typed rejection returned by every manager call.
type Error struct {
	Reason Reason
	Detail string
}

func (e *Error) Error() string {
	if e.Detail == "" {
		return string(e.Reason)
	}
	return string(e.Reason) + ": " + e.Detail
}

// IsError reports whether err carries the given rejection reason.
func IsError(err error, reason Reason) bool {
	var fe *Error
	if errors.As(err, &fe) {
		return fe.Reason == reason
	}
	return false
}

// Order is a freeze order as stored in the registration queue.
type Order struct {
	ID     []byte
	Amount int64
	Expire int64
}

// EffectiveOrder is a queue order together with its effective freeze amount.
type EffectiveOrder struct {
	ID        []byte
	Amount    int64
	Effective int64
	Expire    int64
}

// Snapshot is the effective state of an account at the time it is observed.
type Snapshot struct {
	Balance   int64
	Available int64
	Orders    []EffectiveOrder
}

// Manager is a concurrency-safe judicial freeze manager.
//
// A single mutex makes every accepted call (SeizeQueue included) one atomic
// step, so concurrent histories are equivalent to some serial order.
type Manager struct {
	mu       sync.Mutex
	m        int64
	accounts map[string]*account

	// Invariant: sum(account balances) + seizedTotal + debitedTotal == depositedTotal.
	depositedTotal int64
	debitedTotal   int64
	seizedTotal    int64
}

type account struct {
	balance int64
	queue   []Order
}

func bad(reason Reason, detail string) error {
	return &Error{Reason: reason, Detail: detail}
}

func validTime(t int64) bool {
	return 0 <= t && t <= MaxTime
}

func validAmount(x int64) bool {
	return 1 <= x && x <= MaxAmount
}

func nonEmpty(b []byte) bool {
	return len(b) > 0
}

// sweep removes every order whose expiry is non-zero and not after t.
// An order with exp == t is already expired at t.
func sweep(queue []Order, t int64) []Order {
	kept := queue[:0]
	for _, o := range queue {
		if o.Expire != 0 && o.Expire <= t {
			continue
		}
		kept = append(kept, o)
	}
	return kept
}

// effective computes from the queue head:
//
//	e_i = min(a_i, max(0, B - sum(e_j for j<i)))
//
// Each entry only depends on B and the orders ahead of it; orders behind
// never influence it.
func effective(queue []Order, balance int64) []EffectiveOrder {
	out := make([]EffectiveOrder, len(queue))
	used := int64(0)
	for i, o := range queue {
		e := o.Amount
		if rem := balance - used; e > rem {
			e = rem
		}
		if e < 0 {
			e = 0
		}
		used += e
		id := make([]byte, len(o.ID))
		copy(id, o.ID)
		out[i] = EffectiveOrder{
			ID:        id,
			Amount:    o.Amount,
			Effective: e,
			Expire:    o.Expire,
		}
	}
	return out
}

func totalEffective(orders []EffectiveOrder) int64 {
	total := int64(0)
	for _, o := range orders {
		total += o.Effective
	}
	return total
}

func findOrder(queue []Order, id []byte) int {
	for i := range queue {
		if string(queue[i].ID) == string(id) {
			return i
		}
	}
	return -1
}

// NewManager creates an empty manager.
func NewManager() *Manager {
	return &Manager{accounts: map[string]*account{}}
}

// Deposit creates the account if missing and adds x to its balance.
func (m *Manager) Deposit(acct []byte, t, x int64) error {
	if !nonEmpty(acct) || !validTime(t) || !validAmount(x) {
		return bad(ReasonInvalidParam, "account id, timestamp or amount out of range")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if t < m.m {
		return bad(ReasonTimeRegression, "")
	}
	current := int64(0)
	if acc := m.accounts[string(acct)]; acc != nil {
		current = acc.balance
	}
	if current+x > MaxBalance {
		return bad(ReasonInvalidParam, "balance would exceed 1e15")
	}
	acc, ok := m.accounts[string(acct)]
	if !ok {
		acc = &account{}
		m.accounts[string(acct)] = acc
	}
	acc.balance += x
	m.depositedTotal += x
	m.m = t
	return nil
}

// Debit reduces the balance by x, requiring x <= available balance.
func (m *Manager) Debit(acct []byte, t, x int64) error {
	if !nonEmpty(acct) || !validTime(t) || !validAmount(x) {
		return bad(ReasonInvalidParam, "account id, timestamp or amount out of range")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if t < m.m {
		return bad(ReasonTimeRegression, "")
	}
	acc := m.accounts[string(acct)]
	if acc == nil {
		return bad(ReasonAccountMissing, "")
	}
	acc.queue = sweep(acc.queue, t)
	orders := effective(acc.queue, acc.balance)
	available := acc.balance - totalEffective(orders)
	if x > available {
		return bad(ReasonAvailableShort, "")
	}
	acc.balance -= x
	m.debitedTotal += x
	m.m = t
	return nil
}

// Freeze appends a new order with nominal amount a and expiry exp to the queue.
// exp == 0 means never expires; otherwise t < exp <= 1e12.
func (m *Manager) Freeze(acct []byte, t int64, id []byte, a, exp int64) error {
	if !nonEmpty(acct) || !nonEmpty(id) || !validTime(t) || !validAmount(a) {
		return bad(ReasonInvalidParam, "account id, order id, timestamp or amount out of range")
	}
	if exp != 0 && (exp <= t || exp > MaxTime) {
		return bad(ReasonInvalidParam, "expiry out of range")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if t < m.m {
		return bad(ReasonTimeRegression, "")
	}
	acc := m.accounts[string(acct)]
	if acc == nil {
		return bad(ReasonAccountMissing, "")
	}
	acc.queue = sweep(acc.queue, t)
	if findOrder(acc.queue, id) >= 0 {
		return bad(ReasonIDDuplicate, "")
	}
	storedID := make([]byte, len(id))
	copy(storedID, id)
	acc.queue = append(acc.queue, Order{ID: storedID, Amount: a, Expire: exp})
	m.m = t
	return nil
}

// Unfreeze reduces order id's nominal amount by x, removing it at zero.
func (m *Manager) Unfreeze(acct []byte, t int64, id []byte, x int64) error {
	if !nonEmpty(acct) || !nonEmpty(id) || !validTime(t) || !validAmount(x) {
		return bad(ReasonInvalidParam, "account id, order id, timestamp or amount out of range")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if t < m.m {
		return bad(ReasonTimeRegression, "")
	}
	acc := m.accounts[string(acct)]
	if acc == nil {
		return bad(ReasonAccountMissing, "")
	}
	acc.queue = sweep(acc.queue, t)
	idx := findOrder(acc.queue, id)
	if idx < 0 {
		return bad(ReasonOrderMissing, "")
	}
	if x > acc.queue[idx].Amount {
		return bad(ReasonUnfreezeOver, "")
	}
	acc.queue[idx].Amount -= x
	if acc.queue[idx].Amount == 0 {
		acc.queue = append(acc.queue[:idx], acc.queue[idx+1:]...)
	}
	m.m = t
	return nil
}

// Seize judicially deducts x (1 <= x <= effective amount) from a single order.
// Both B and the order nominal amount decrease by x; the order leaves the
// queue once its nominal amount reaches zero.
func (m *Manager) Seize(acct []byte, t int64, id []byte, x int64) error {
	if !nonEmpty(acct) || !nonEmpty(id) || !validTime(t) || !validAmount(x) {
		return bad(ReasonInvalidParam, "account id, order id, timestamp or amount out of range")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if t < m.m {
		return bad(ReasonTimeRegression, "")
	}
	acc := m.accounts[string(acct)]
	if acc == nil {
		return bad(ReasonAccountMissing, "")
	}
	acc.queue = sweep(acc.queue, t)
	idx := findOrder(acc.queue, id)
	if idx < 0 {
		return bad(ReasonOrderMissing, "")
	}
	orders := effective(acc.queue, acc.balance)
	if x > orders[idx].Effective {
		return bad(ReasonSeizeOver, "")
	}
	acc.balance -= x
	acc.queue[idx].Amount -= x
	if acc.queue[idx].Amount == 0 {
		acc.queue = append(acc.queue[:idx], acc.queue[idx+1:]...)
	}
	m.seizedTotal += x
	m.m = t
	return nil
}

// SeizeQueue deducts x across the queue in registration order as one atomic
// step. It judges x against the start-of-step effective-amount snapshot; when
// x exceeds the total effective amount the whole call is rejected with no
// partial application.
func (m *Manager) SeizeQueue(acct []byte, t int64, x int64) error {
	if !nonEmpty(acct) || !validTime(t) || !validAmount(x) {
		return bad(ReasonInvalidParam, "account id, timestamp or amount out of range")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if t < m.m {
		return bad(ReasonTimeRegression, "")
	}
	acc := m.accounts[string(acct)]
	if acc == nil {
		return bad(ReasonAccountMissing, "")
	}
	acc.queue = sweep(acc.queue, t)
	orders := effective(acc.queue, acc.balance)
	if x > totalEffective(orders) {
		return bad(ReasonSeizeQOver, "")
	}
	remaining := x
	for i := range acc.queue {
		if remaining == 0 {
			break
		}
		deduct := orders[i].Effective
		if deduct > remaining {
			deduct = remaining
		}
		acc.queue[i].Amount -= deduct
		remaining -= deduct
	}
	kept := acc.queue[:0]
	for _, o := range acc.queue {
		if o.Amount > 0 {
			kept = append(kept, o)
		}
	}
	acc.queue = kept
	acc.balance -= x
	m.seizedTotal += x
	m.m = t
	return nil
}

// Query returns the effective account state at time t.
func (m *Manager) Query(acct []byte, t int64) (Snapshot, error) {
	if !nonEmpty(acct) || !validTime(t) {
		return Snapshot{}, bad(ReasonInvalidParam, "account id or timestamp out of range")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if t < m.m {
		return Snapshot{}, bad(ReasonTimeRegression, "")
	}
	acc := m.accounts[string(acct)]
	if acc == nil {
		return Snapshot{}, bad(ReasonAccountMissing, "")
	}
	acc.queue = sweep(acc.queue, t)
	orders := effective(acc.queue, acc.balance)
	m.m = t
	return Snapshot{
		Balance:   acc.balance,
		Available: acc.balance - totalEffective(orders),
		Orders:    orders,
	}, nil
}

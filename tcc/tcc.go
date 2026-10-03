// Package tcc 在 ledger 之上维护 TCC 分支记录（Try/Confirm/Cancel），
// 提供空回滚、悬挂防护、到期自动取消与容量控制。
package tcc

import (
	"container/heap"
	"io"
	"log"
	"sync"

	"ontology/ledger"
)

const (
	minNow       = 0
	maxNow       = 1_000_000_000_000_000 // 1e15
	maxAmount    = 1_000_000_000_000     // 1e12
	maxTTL       = 1_000_000_000
	maxCapacityN = 1_000_000
)

// Logger 让调用方接收“输入、输出与判定依据”日志；nil 表示不打印。
type Logger interface {
	Printf(format string, v ...any)
}

// TCC 是分支事务资源管理器。
type TCC struct {
	mu    sync.Mutex
	lg    *ledger.Ledger
	log   Logger
	ttl   int64
	capN  int
	clock int64
	recs  map[string]*Branch
	hp    expireHeap
	refs  map[string]*heapEntry
	// expiryPeeks 为最近一次到期扫描考察的堆项数（含未到期的根项）。
	expiryPeeks int
}

// Option 配置 TCC。
type Option func(*TCC)

// WithLogger 设置日志输出。
func WithLogger(w io.Writer) Option {
	return func(t *TCC) {
		if w != nil {
			t.log = log.New(w, "tcc ", log.LstdFlags|log.Lmicroseconds)
		}
	}
}

// New 创建资源管理器：ttl ∈ [1,1e9]，N ∈ [1,1e6]。
func New(lg *ledger.Ledger, ttl, N int64, opts ...Option) (*TCC, error) {
	if ttl < 1 || ttl > maxTTL || N < 1 || N > maxCapacityN {
		return nil, ErrInvalid
	}
	t := &TCC{
		lg:   lg,
		ttl:  ttl,
		capN: int(N),
		recs: make(map[string]*Branch),
		refs: make(map[string]*heapEntry),
	}
	for _, o := range opts {
		o(t)
	}
	return t, nil
}

// Try 预留资源；幂等与悬挂规则见 DESIGN。
func (t *TCC) Try(xid, br, acct string, amount, now int64) error {
	if !nonEmpty(xid, br, acct) || amount < 1 || amount > maxAmount {
		return t.fail("Try", key(xid, br), ErrInvalid)
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if err := t.checkClock(now); err != nil {
		return t.fail("Try", key(xid, br), err)
	}
	popped, converted := t.applyExpired(now)
	k := key(xid, br)
	if b := t.recs[k]; b != nil {
		switch b.State {
		case StateTried:
			if b.Acct != acct || b.Amount != amount {
				t.rollbackExpired(popped, converted)
				return t.fail("Try", k, ErrMismatch)
			}
			t.commit("Try(idempotent)", k, now)
			return nil
		case StateConfirmed:
			t.rollbackExpired(popped, converted)
			return t.fail("Try", k, ErrState)
		default:
			t.rollbackExpired(popped, converted)
			return t.fail("Try", k, ErrHanging)
		}
	}
	if len(t.recs) >= t.capN {
		t.rollbackExpired(popped, converted)
		return t.fail("Try", k, ErrCapacity)
	}
	if !t.lg.Freeze(acct, amount) {
		t.rollbackExpired(popped, converted)
		return t.fail("Try", k, ErrInsufficient)
	}
	deadline := now + t.ttl
	b := &Branch{State: StateTried, Acct: acct, Amount: amount, Deadline: deadline}
	t.recs[k] = b
	e := &heapEntry{key: k, deadline: deadline}
	heap.Push(&t.hp, e)
	t.refs[k] = e
	t.commit("Try", k, now)
	return nil
}

// Confirm 确认预留；Confirmed 幂等成功。
func (t *TCC) Confirm(xid, br string, now int64) error {
	if !nonEmpty(xid, br) {
		return t.fail("Confirm", key(xid, br), ErrInvalid)
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if err := t.checkClock(now); err != nil {
		return t.fail("Confirm", key(xid, br), err)
	}
	popped, converted := t.applyExpired(now)
	k := key(xid, br)
	b := t.recs[k]
	switch {
	case b == nil:
		t.rollbackExpired(popped, converted)
		return t.fail("Confirm", k, ErrNoBranch)
	case b.State == StateTried:
		heap.Remove(&t.hp, t.refs[k].index)
		delete(t.refs, k)
		t.lg.Confirm(b.Acct, b.Amount)
		b.State = StateConfirmed
		t.commit("Confirm", k, now)
		return nil
	case b.State == StateConfirmed:
		t.commit("Confirm(idempotent)", k, now)
		return nil
	case b.Reason == Expired:
		t.rollbackExpired(popped, converted)
		return t.fail("Confirm", k, ErrExpired)
	default:
		t.rollbackExpired(popped, converted)
		return t.fail("Confirm", k, ErrConflict)
	}
}

// Cancel 取消预留；记录不存在则写空回滚标记；任何 Cancelled 幂等成功。
func (t *TCC) Cancel(xid, br string, now int64) error {
	if !nonEmpty(xid, br) {
		return t.fail("Cancel", key(xid, br), ErrInvalid)
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if err := t.checkClock(now); err != nil {
		return t.fail("Cancel", key(xid, br), err)
	}
	popped, converted := t.applyExpired(now)
	k := key(xid, br)
	b := t.recs[k]
	switch {
	case b == nil:
		if len(t.recs) >= t.capN {
			t.rollbackExpired(popped, converted)
			return t.fail("Cancel", k, ErrCapacity)
		}
		t.recs[k] = &Branch{State: StateCancelled, Reason: Empty}
		t.commit("Cancel(empty-rollback)", k, now)
		return nil
	case b.State == StateTried:
		heap.Remove(&t.hp, t.refs[k].index)
		delete(t.refs, k)
		t.lg.Unfreeze(b.Acct, b.Amount)
		b.State = StateCancelled
		b.Reason = Cancel
		t.commit("Cancel", k, now)
		return nil
	default: // Confirmed 或任何原因的 Cancelled
		if b.State == StateConfirmed {
			t.rollbackExpired(popped, converted)
			return t.fail("Cancel", k, ErrConflict)
		}
		t.commit("Cancel(idempotent:"+string(b.Reason)+")", k, now)
		return nil
	}
}

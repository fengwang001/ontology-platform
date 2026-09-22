// Package budget accounts resident bytes against a hard limit.
package budget

import (
	"errors"
	"sync"
)

// ErrTooLarge is returned when one item exceeds the whole limit.
var ErrTooLarge = errors.New("budget: item exceeds limit")

// Budget is a blocking byte accountant with a hard upper limit.
type Budget struct {
	mu    sync.Mutex
	cond  *sync.Cond
	limit int64
	used  int64
	peak  int64
}

// New returns a Budget with the given hard limit in bytes.
func New(limit int64) *Budget {
	b := &Budget{limit: limit}
	b.cond = sync.NewCond(&b.mu)
	return b
}

// Limit returns the configured hard limit.
func (b *Budget) Limit() int64 { return b.limit }

// Acquire blocks until n bytes can be accounted without exceeding the
// limit, then accounts them. It never lets Used exceed Limit.
func (b *Budget) Acquire(n int64) error {
	if n > b.limit {
		return ErrTooLarge
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	for b.used+n > b.limit {
		b.cond.Wait()
	}
	b.used += n
	if b.used > b.peak {
		b.peak = b.used
	}
	return nil
}

// TryAcquire is Acquire without blocking; ok=false means it would exceed.
func (b *Budget) TryAcquire(n int64) (bool, error) {
	if n > b.limit {
		return false, ErrTooLarge
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.used+n > b.limit {
		return false, nil
	}
	b.used += n
	if b.used > b.peak {
		b.peak = b.used
	}
	return true, nil
}

// Release returns n bytes to the budget and wakes waiters.
func (b *Budget) Release(n int64) {
	b.mu.Lock()
	b.used -= n
	if b.used < 0 {
		b.used = 0
	}
	b.mu.Unlock()
	b.cond.Broadcast()
}

// Used returns currently accounted bytes.
func (b *Budget) Used() int64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.used
}

// Peak returns the historical maximum of Used.
func (b *Budget) Peak() int64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.peak
}

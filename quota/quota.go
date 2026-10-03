// Package quota implements a per-tenant lazy token bucket measured in
// millibytes (1 byte = 1000 millibytes).
package quota

const milliPerByte = 1000

// Bucket is a lazily refilled token bucket.
type Bucket struct {
	rate       int64 // millibytes per millisecond
	cap        int64 // millibytes
	tokens     int64 // millibytes currently available
	lastRefill int64 // millisecond timestamp of last settled refill
}

// NewBucket creates a bucket. rate is bytes/sec, burst is bytes.
func NewBucket(rate, burst int64) *Bucket {
	cap := burst * milliPerByte
	// rate bytes/sec == rate millibytes/ms (1000mb per byte, 1000ms per s).
	return &Bucket{rate: rate, cap: cap, tokens: cap, lastRefill: 0}
}

// LastRefill reports the last settled timestamp.
func (b *Bucket) LastRefill() int64 { return b.lastRefill }

// Tokens reports the currently settled token balance in millibytes.
func (b *Bucket) Tokens() int64 { return b.tokens }

// Cap reports the bucket capacity in millibytes.
func (b *Bucket) Cap() int64 { return b.cap }

// productAtLeast reports rate*delta >= need without computing the product,
// which can reach 1e9*1e12 = 1e21 and overflow int64.
func productAtLeast(rate, delta, need int64) bool {
	if rate <= 0 || need <= 0 {
		return need <= 0
	}
	q, r := need/rate, need%rate
	if delta > q {
		return true
	}
	return delta == q && r == 0
}

// refilledTo returns the balance after accruing up to now. It never forms an
// intermediate value larger than cap (<= 1e12).
func (b *Bucket) refilledTo(now int64) int64 {
	if now <= b.lastRefill || b.rate <= 0 {
		return b.tokens
	}
	delta := now - b.lastRefill
	need := b.cap - b.tokens
	if productAtLeast(b.rate, delta, need) {
		return b.cap
	}
	return b.tokens + b.rate*delta
}

// PeekBalance returns the virtual balance after refilling to now, without
// mutating the bucket.
func (b *Bucket) PeekBalance(now int64) int64 {
	return b.refilledTo(now)
}

// RefillTo settles the refill up to now and moves lastRefill to now.
func (b *Bucket) RefillTo(now int64) {
	if now <= b.lastRefill {
		return
	}
	b.tokens = b.refilledTo(now)
	b.lastRefill = now
}

// Debit refills to now and atomically removes cost millibytes; it reports
// whether the virtual balance was sufficient. On failure no state changes.
func (b *Bucket) Debit(now, cost int64) bool {
	balance := b.refilledTo(now)
	if balance < cost {
		return false
	}
	b.tokens = balance - cost
	b.lastRefill = now
	return true
}

// Refund refills to now and adds back millibytes, capped at capacity.
func (b *Bucket) Refund(now, back int64) {
	b.RefillTo(now)
	b.tokens += back
	if b.tokens > b.cap {
		b.tokens = b.cap
	}
}

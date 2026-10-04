// Package numplan manages number blocks and their home operators.
//
// Blocks are append-only: a block is never removed or overwritten. The home
// operator of a number at time t is a pure function of t — among blocks with
// the same total length that match the number as a prefix and became effective
// no later than t, the one with the longest prefix wins.
package numplan

import (
	"errors"
	"sync"
	"sync/atomic"
)

const (
	minDigits = 5
	maxDigits = 15
	maxOp     = 10000
	maxTime   = int64(1_000_000_000_000)
)

// MaxOperator reports the largest valid operator identifier.
func MaxOperator() int64 { return maxOp }

// ValidOperator reports whether op is in [1, 10^4].
func ValidOperator(op int64) bool { return op >= 1 && op <= maxOp }

var (
	ErrInvalidArgument   = errors.New("invalid argument")
	ErrClockRewound      = errors.New("clock rewound")
	ErrBlockExists       = errors.New("block already exists")
	ErrNumberUnallocated = errors.New("number not allocated")
	ErrFrozen            = errors.New("number in freeze")
	ErrPendingExists     = errors.New("pending port order exists")
	ErrDonorMismatch     = errors.New("donor does not match current server")
	ErrSameOperator      = errors.New("recipient equals donor")
	ErrLeadTime          = errors.New("lead time shorter than Lmin")
	ErrOrderNotFound     = errors.New("order not found")
	ErrAlreadyEffective  = errors.New("order already effective")
)

type block struct {
	prefix string
	op     int64
	effAt  int64
}

// Plan is an immutable-append number plan.
type Plan struct {
	mu     sync.RWMutex
	blocks map[int]map[string]block // total length -> prefix -> block
	maxNow int64
	probes atomic.Int64
}

// New creates an empty plan.
func New() *Plan { return &Plan{blocks: make(map[int]map[string]block)} }

// ValidNumber reports whether s is a 5..15 digit decimal number.
func ValidNumber(s string) bool {
	if len(s) < minDigits || len(s) > maxDigits {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

func validPrefix(prefix string, length int) bool {
	if length < minDigits || length > maxDigits {
		return false
	}
	if len(prefix) < 1 || len(prefix) > length {
		return false
	}
	for i := 0; i < len(prefix); i++ {
		if prefix[i] < '0' || prefix[i] > '9' {
			return false
		}
	}
	return true
}

// AssignBlock registers a block; skeleton.
//
// Every number of total length digits beginning with prefix belongs to op from
// now on. A duplicate (prefix, length) pair is rejected.
func (p *Plan) AssignBlock(prefix string, length int, op, now int64) error {
	if !validPrefix(prefix, length) || op < 1 || op > maxOp || now < 0 || now > maxTime {
		return ErrInvalidArgument
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if now < p.maxNow {
		return ErrClockRewound
	}
	byPrefix := p.blocks[length]
	if byPrefix == nil {
		byPrefix = make(map[string]block)
		p.blocks[length] = byPrefix
	}
	if _, exists := byPrefix[prefix]; exists {
		return ErrBlockExists
	}
	byPrefix[prefix] = block{prefix: prefix, op: op, effAt: now}
	p.maxNow = now
	return nil
}

// OwnerAt returns the home operator at time t (0, false when unallocated).
// Only the number's own prefixes (one per digit position) are ever examined,
// so the number of records probed never exceeds the number of digits.
func (p *Plan) OwnerAt(number string, t int64) (int64, bool) {
	if !ValidNumber(number) || t < 0 || t > maxTime {
		return 0, false
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	op, ok, probes := p.ownerLocked(number, t)
	p.probes.Add(int64(probes))
	return op, ok
}

// OwnerAtProbed is OwnerAt but reports how many block records were examined
// through probed (it does not touch the plan's internal Probes counter).
func (p *Plan) OwnerAtProbed(number string, t int64, probed *int64) int64 {
	if !ValidNumber(number) || t < 0 || t > maxTime {
		return 0
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	op, ok, n := p.ownerLocked(number, t)
	*probed = int64(n)
	if !ok {
		return 0
	}
	return op
}

func (p *Plan) ownerLocked(number string, t int64) (int64, bool, int) {
	byPrefix := p.blocks[len(number)]
	var bestOp int64
	bestLen := 0
	found := false
	var probes int
	for k := len(number); k >= 1; k-- {
		b, ok := byPrefix[number[:k]]
		if !ok {
			continue
		}
		probes++
		if b.effAt <= t && k > bestLen {
			bestOp = b.op
			bestLen = k
			found = true
		}
	}
	return bestOp, found, probes
}

// MaxNow reports the largest accepted timestamp.
func (p *Plan) MaxNow() int64 {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.maxNow
}

// Probes returns block records examined since the previous Probes call and
// resets the counter.
func (p *Plan) Probes() int64 { return p.probes.Swap(0) }

// Package booking implements a contract ad-inventory reservation book with
// oversell ratio, directed cross-subset feasibility checks and a FIFO
// waitlist with promotion.
package booking

import (
	"errors"
	"sort"
	"sync"
)

// Rejection reasons, checked in this exact priority order.
var (
	ErrInvalidParam  = errors.New("booking: invalid parameter")
	ErrDuplicateID   = errors.New("booking: contract already exists")
	ErrNotFound      = errors.New("booking: contract not found")
	ErrNotBooked     = errors.New("booking: contract is not booked")
	ErrNeverBookable = errors.New("booking: contract can never be booked")
	ErrInfeasible    = errors.New("booking: infeasible")
)

const (
	maxUnits   = 10
	maxStock   = int64(1e12)
	maxRho     = int64(1000)
	maxQty     = int64(1e12)
	rhoPercent = int64(100)
)

type contract struct {
	id   string
	mask int
	qty  int64
}

// Book is a reservation book over C inventory units. It is safe for
// concurrent use; every operation (including waitlist promotion) is atomic.
type Book struct {
	mu     sync.Mutex
	c      int
	full   int // 2^C
	cap    []int64
	load   []int64
	booked map[string]*contract
	wait   []*contract
	inWait map[string]bool
	checks int64
}

// New builds a book with C units, per-unit stocks s and oversell ratio rho
// (a percentage). Cap(T) = floor(rho * sum(s[T]) / 100) per nonempty subset.
func New(c int, s []int64, rho int64) (*Book, error) {
	if c < 1 || c > maxUnits || len(s) != c || rho < 1 || rho > maxRho {
		return nil, ErrInvalidParam
	}
	for _, v := range s {
		if v < 0 || v > maxStock {
			return nil, ErrInvalidParam
		}
	}
	full := 1 << c
	b := &Book{
		c:      c,
		full:   full,
		cap:    make([]int64, full),
		load:   make([]int64, full),
		booked: make(map[string]*contract),
		inWait: make(map[string]bool),
	}
	for t := 1; t < full; t++ {
		var sum int64
		for j := 0; j < c; j++ {
			if t&(1<<j) != 0 {
				sum += s[j]
			}
		}
		b.cap[t] = rho * sum / rhoPercent
	}
	return b, nil
}

// Book attempts to reserve qty on the directed unit set mask.
func (b *Book) Book(id string, mask int, qty int64) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.bookLocked(id, mask, qty)
}

// Cancel removes a booked contract (triggering promotion) or a waitlisted
// one (no promotion).
func (b *Book) Cancel(id string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.cancelLocked(id)
}

// Resize changes the qty of a booked contract.
func (b *Book) Resize(id string, q2 int64) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.resizeLocked(id, q2)
}

// Retarget changes the mask of a booked contract.
func (b *Book) Retarget(id string, mask2 int) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.retargetLocked(id, mask2)
}

func (b *Book) bookLocked(id string, mask int, qty int64) error {
	if id == "" || mask < 1 || mask >= b.full || qty < 1 || qty > maxQty {
		return ErrInvalidParam
	}
	if b.booked[id] != nil || b.inWait[id] {
		return ErrDuplicateID
	}
	if qty > b.cap[mask] {
		return ErrNeverBookable
	}
	if !b.fitsAdd(mask, qty) {
		b.wait = append(b.wait, &contract{id: id, mask: mask, qty: qty})
		b.inWait[id] = true
		return nil
	}
	b.addLoad(mask, qty)
	b.booked[id] = &contract{id: id, mask: mask, qty: qty}
	return nil
}

func (b *Book) cancelLocked(id string) error {
	if id == "" {
		return ErrInvalidParam
	}
	if ct := b.booked[id]; ct != nil {
		b.addLoad(ct.mask, -ct.qty)
		delete(b.booked, id)
		b.promote()
		return nil
	}
	if b.inWait[id] {
		for i, ct := range b.wait {
			if ct.id == id {
				b.wait = append(b.wait[:i], b.wait[i+1:]...)
				break
			}
		}
		delete(b.inWait, id)
		return nil
	}
	return ErrNotFound
}

func (b *Book) resizeLocked(id string, q2 int64) error {
	if id == "" || q2 < 1 || q2 > maxQty {
		return ErrInvalidParam
	}
	ct := b.booked[id]
	if ct == nil {
		if b.inWait[id] {
			return ErrNotBooked
		}
		return ErrNotFound
	}
	switch {
	case q2 == ct.qty:
		return nil
	case q2 < ct.qty:
		b.addLoad(ct.mask, q2-ct.qty)
		ct.qty = q2
		b.promote()
		return nil
	default:
		if !b.fitsAdd(ct.mask, q2-ct.qty) {
			return ErrInfeasible
		}
		b.addLoad(ct.mask, q2-ct.qty)
		ct.qty = q2
		return nil
	}
}

func (b *Book) retargetLocked(id string, mask2 int) error {
	if id == "" || mask2 < 1 || mask2 >= b.full {
		return ErrInvalidParam
	}
	ct := b.booked[id]
	if ct == nil {
		if b.inWait[id] {
			return ErrNotBooked
		}
		return ErrNotFound
	}
	if mask2 == ct.mask {
		return nil
	}
	if !b.fitsRetarget(ct.mask, mask2, ct.qty) {
		return ErrInfeasible
	}
	b.addLoad(ct.mask, -ct.qty)
	b.addLoad(mask2, ct.qty)
	ct.mask = mask2
	b.promote()
	return nil
}

// fitsAdd reports whether adding qty on mask keeps every affected subset
// feasible. It examines all 2^(C-|mask|) supersets of mask without early
// exit, counting each examination.
func (b *Book) fitsAdd(mask int, qty int64) bool {
	ok := true
	for t := 1; t < b.full; t++ {
		if t&mask != mask {
			continue
		}
		b.checks++
		if b.load[t]+qty > b.cap[t] {
			ok = false
		}
	}
	return ok
}

// fitsRetarget examines subsets containing mask2 but not mask1, i.e. the
// subsets whose load actually grows when a contract moves from mask1 to
// mask2. Subsets containing both are unaffected (the qty stays inside).
func (b *Book) fitsRetarget(mask1, mask2 int, qty int64) bool {
	ok := true
	for t := 1; t < b.full; t++ {
		if t&mask2 != mask2 || t&mask1 == mask1 {
			continue
		}
		b.checks++
		if b.load[t]+qty > b.cap[t] {
			ok = false
		}
	}
	return ok
}

// addLoad applies a signed qty delta to every subset containing mask.
func (b *Book) addLoad(mask int, delta int64) {
	for t := 1; t < b.full; t++ {
		if t&mask == mask {
			b.load[t] += delta
		}
	}
}

// promote scans the waitlist once in arrival order, booking every contract
// that fits the current booked set (including earlier promotions from this
// same scan) and leaving the rest in place without blocking.
func (b *Book) promote() {
	kept := b.wait[:0]
	for _, ct := range b.wait {
		if b.fitsAdd(ct.mask, ct.qty) {
			b.addLoad(ct.mask, ct.qty)
			b.booked[ct.id] = ct
			delete(b.inWait, ct.id)
			continue
		}
		kept = append(kept, ct)
	}
	for i := len(kept); i < len(b.wait); i++ {
		b.wait[i] = nil
	}
	b.wait = kept
}

// SubsetChecks returns the number of subset feasibility examinations
// performed so far (the never-bookable single-point check is not counted).
func (b *Book) SubsetChecks() int64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.checks
}

// IsBooked reports whether id is currently booked.
func (b *Book) IsBooked(id string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	_, ok := b.booked[id]
	return ok
}

// Contract is a snapshot of a booked or waitlisted contract.
type Contract struct {
	ID   string
	Mask int
	Qty  int64
}

// BookedContracts returns the booked contracts sorted by id (deterministic).
func (b *Book) BookedContracts() []Contract {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]Contract, 0, len(b.booked))
	for _, ct := range b.booked {
		out = append(out, Contract{ID: ct.id, Mask: ct.mask, Qty: ct.qty})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// WaitlistContracts returns the waitlisted contracts in arrival order.
func (b *Book) WaitlistContracts() []Contract {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]Contract, len(b.wait))
	for i, ct := range b.wait {
		out[i] = Contract{ID: ct.id, Mask: ct.mask, Qty: ct.qty}
	}
	return out
}

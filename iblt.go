package ontology

import (
	"errors"
	"sort"
	"sync"
)

const (
	ibltSalt uint64 = 0x9E3779B97F4A7C15
	ibltXorC uint64 = 0xD6E8FEB86659FD93
)

// mix applies the SplitMix64 finalizer to z with uint64 wrap-around.
func mix(z uint64) uint64 {
	z ^= z >> 30
	z *= 0xBF58476D1CE4E5B9
	z ^= z >> 27
	z *= 0x94D049BB133111EB
	z ^= z >> 31
	return z
}

// g is the key checksum mixed into every cell the key touches.
func g(x uint64) uint64 { return mix(x ^ ibltXorC) }

// positions returns the three cell indices (one per segment) that x maps to.
func positions(x uint64, segment int) [3]int {
	var p [3]int
	for i := 0; i < 3; i++ {
		h := mix(x + uint64(i+1)*ibltSalt)
		p[i] = i*segment + int(h%uint64(segment))
	}
	return p
}

// ErrInvalidSize is returned by New when m is not a positive multiple of 3.
var ErrInvalidSize = errors.New("iblt: m must be a positive multiple of 3")

// ErrSizeMismatch is returned by Subtract when the sketches differ in m.
var ErrSizeMismatch = errors.New("iblt: cannot subtract sketches with different m")

// ErrNegativeLimit is returned by Decode when limit is negative.
var ErrNegativeLimit = errors.New("iblt: decode limit must not be negative")

// ErrUndecodable is returned by Decode when the peel cannot empty every cell.
var ErrUndecodable = errors.New("iblt: sketch is not decodable")

// ErrLimitExceeded is returned by Decode when more than limit distinct keys
// would have to be recovered.
var ErrLimitExceeded = errors.New("iblt: decode limit exceeded")

// cell is one bucket of the sketch.
type cell struct {
	count int64
	key   uint64
	check uint64
}

// IBLT is a reversible invertible Bloom lookup table over uint64 keys with
// three hash groups. The zero value is not usable; create one with New.
type IBLT struct {
	mu sync.Mutex
	m  int
	// cells stores the three m/3-sized segments consecutively.
	cells []cell
}

// New creates an empty sketch with m cells.
func New(m int) (*IBLT, error) {
	if m <= 0 || m%3 != 0 {
		return nil, ErrInvalidSize
	}
	return &IBLT{m: m, cells: make([]cell, m)}, nil
}

// M reports the number of cells.
func (s *IBLT) M() int { return s.m }

// Add inserts one key into the sketch.
func (s *IBLT) Add(x uint64) { s.accumulate(x, 1) }

// Remove deletes one key from the sketch. It performs no membership check:
// the sketch is purely a signed accumulation.
func (s *IBLT) Remove(x uint64) { s.accumulate(x, -1) }

// snapshotLocked must be called while the caller does not hold s.mu. It
// returns a value copy of all cells taken under the lock.
func (s *IBLT) snapshot() []cell {
	s.mu.Lock()
	cp := append([]cell(nil), s.cells...)
	s.mu.Unlock()
	return cp
}

// Subtract returns a new sketch equal to a minus b, cell by cell: counts are
// subtracted and the key/check accumulators are XORed. Both operands are read
// as point-in-time snapshots and neither is modified. Subtracting a sketch
// from itself yields the all-zero sketch.
func Subtract(a, b *IBLT) (*IBLT, error) {
	a.mu.Lock()
	am := a.m
	a.mu.Unlock()
	b.mu.Lock()
	bm := b.m
	b.mu.Unlock()
	if am != bm {
		return nil, ErrSizeMismatch
	}

	ac := a.snapshot()
	// A single locked snapshot of this exact operand guarantees that
	// s-s == 0 even when concurrent mutations are in flight.
	b.mu.Lock()
	bc := append([]cell(nil), b.cells...)
	b.mu.Unlock()

	out := &IBLT{m: am, cells: make([]cell, am)}
	if a == b {
		// Same object: the two snapshots would race with each other; one
		// snapshot XORed with itself is deterministically zero.
		return out, nil
	}
	for i := range ac {
		out.cells[i] = cell{
			count: ac[i].count - bc[i].count,
			key:   ac[i].key ^ bc[i].key,
			check: ac[i].check ^ bc[i].check,
		}
	}
	return out, nil
}

// Decode attempts to recover the symmetric difference from a private copy of
// the sketch, so the receiver is never modified. Keys present on the left
// side of a Subtract (peeled count +1) land in onlyA; count -1 keys land in
// onlyB. Both slices are returned sorted.
//
// Error precedence is:
//  1. limit < 0 -> ErrNegativeLimit;
//  2. while peeling, the first of ErrLimitExceeded / ErrUndecodable that
//     occurs. A key seen for the second time makes the sketch undecodable
//     before the limit check. Success requires every cell to be all-zero.
func (s *IBLT) Decode(limit int) (onlyA []uint64, onlyB []uint64, err error) {
	if limit < 0 {
		return nil, nil, ErrNegativeLimit
	}

	s.mu.Lock()
	m := s.m
	work := append([]cell(nil), s.cells...)
	s.mu.Unlock()
	segment := m / 3

	aOnly := make([]uint64, 0)
	bOnly := make([]uint64, 0)
	seen := make(map[uint64]bool)

	for {
		pure := -1
		for i := 0; i < m; i++ {
			c := &work[i]
			if (c.count == 1 || c.count == -1) && g(c.key) == c.check {
				pure = i
				break
			}
		}
		if pure == -1 {
			break
		}

		c := &work[pure]
		x := c.key
		side := c.count // +1: only A; -1: only B

		// A key cannot legitimately peel twice; this marks an impure
		// remainder and is reported before the limit overflow check.
		if seen[x] {
			return nil, nil, ErrUndecodable
		}
		if len(aOnly)+len(bOnly) >= limit {
			return nil, nil, ErrLimitExceeded
		}
		seen[x] = true
		if side == 1 {
			aOnly = append(aOnly, x)
		} else {
			bOnly = append(bOnly, x)
		}

		// Undo x in the working copy: a +1 key is Removed, a -1 key is
		// Added back.
		gx := g(x)
		undo := -side
		for _, idx := range positions(x, segment) {
			wc := &work[idx]
			wc.count += undo
			wc.key ^= x
			wc.check ^= gx
		}
	}

	for i := range work {
		if work[i] != (cell{}) {
			return nil, nil, ErrUndecodable
		}
	}

	sort.Slice(aOnly, func(i, j int) bool { return aOnly[i] < aOnly[j] })
	sort.Slice(bOnly, func(i, j int) bool { return bOnly[i] < bOnly[j] })
	return aOnly, bOnly, nil
}

// accumulate applies one signed insertion of x: count moves by delta and both
// XOR accumulators fold x and g(x).
func (s *IBLT) accumulate(x uint64, delta int64) {
	gx := g(x)
	s.mu.Lock()
	for _, idx := range positions(x, s.m/3) {
		c := &s.cells[idx]
		c.count += delta
		c.key ^= x
		c.check ^= gx
	}
	s.mu.Unlock()
}

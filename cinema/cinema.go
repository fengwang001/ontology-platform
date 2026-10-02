// Package cinema implements a deterministic, concurrency-safe seat
// reservation registry for a single auditorium.
//
// The auditorium has rows numbered 1..R and seats numbered 1..W per row.
// A party of k people is seated in k consecutive seats of a single row.
// Holds expire after a fixed TTL; confirmed orders occupy seats forever.
package cinema

import (
	"errors"
	"fmt"
	"sync"
)

// Status is the state of a single seat at a given moment.
type Status int

const (
	// Free means the seat can be selected by a new Hold.
	Free Status = iota
	// Held means the seat is covered by an active (unexpired) hold.
	Held
	// Confirmed means the seat is permanently occupied by an order.
	Confirmed
)

func (s Status) String() string {
	switch s {
	case Free:
		return "free"
	case Held:
		return "held"
	case Confirmed:
		return "confirmed"
	}
	return "unknown"
}

var (
	// ErrInvalidDimensions rejects a registry with R not in [1,26],
	// W not in [1,40] or T < 1.
	ErrInvalidDimensions = errors.New("cinema: invalid dimensions")
	// ErrClockRegression rejects an operation whose now is smaller than
	// the maximum now seen by any previously accepted operation.
	ErrClockRegression = errors.New("cinema: clock regression")
	// ErrInvalidPartySize rejects a Hold with k not in [1,8].
	ErrInvalidPartySize = errors.New("cinema: invalid party size")
	// ErrNoSeats rejects a Hold when no row has k consecutive free seats.
	ErrNoSeats = errors.New("cinema: no consecutive free seats")
	// ErrHoldNotFound rejects Confirm/Release for an id never issued.
	ErrHoldNotFound = errors.New("cinema: hold id never issued")
	// ErrHoldConfirmed rejects Confirm/Release for an already confirmed hold.
	ErrHoldConfirmed = errors.New("cinema: hold already confirmed")
	// ErrHoldReleased rejects Confirm/Release for an already released hold.
	ErrHoldReleased = errors.New("cinema: hold already released")
	// ErrHoldExpired rejects Confirm/Release when now >= expiry.
	ErrHoldExpired = errors.New("cinema: hold expired")
)

const (
	maxRows  = 26
	maxWidth = 40
	maxParty = 8
)

type holdState int

const (
	holdActive holdState = iota
	holdConfirmed
	holdReleased
)

type hold struct {
	id      int
	row     int // 1-based
	start   int // 1-based first seat
	k       int
	created int64
	expiry  int64
	state   holdState
	closed  int64 // confirm/release time, valid when state != holdActive
}

// Registry is a seat reservation registry. All methods are safe for
// concurrent use; the result is equivalent to some serial order.
type Registry struct {
	mu     sync.Mutex
	rows   int
	width  int
	ttl    int64
	maxNow int64
	hasNow bool
	nextID int
	holds  map[int]*hold
}

// NewRegistry builds a registry for an auditorium with the given number
// of rows, seats per row and hold TTL (in now units).
func NewRegistry(rows, width int, ttl int64) (*Registry, error) {
	if rows < 1 || rows > maxRows || width < 1 || width > maxWidth || ttl < 1 {
		return nil, fmt.Errorf("%w: rows=%d width=%d ttl=%d",
			ErrInvalidDimensions, rows, width, ttl)
	}
	return &Registry{
		rows:   rows,
		width:  width,
		ttl:    ttl,
		nextID: 1,
		holds:  make(map[int]*hold),
	}, nil
}

// checkClock enforces the monotonic clock rule for accepted operations.
func (r *Registry) checkClock(now int64) error {
	if r.hasNow && now < r.maxNow {
		return fmt.Errorf("%w: now=%d maxNow=%d", ErrClockRegression, now, r.maxNow)
	}
	return nil
}

// accept records now as seen by an accepted operation.
func (r *Registry) accept(now int64) {
	r.maxNow = now
	r.hasNow = true
}

// Hold reserves k consecutive free seats in a single row at time now.
// On success it returns the hold id (issued from 1, gapless), the row
// and the 1-based starting seat. The hold expires at now+T.
func (r *Registry) Hold(k int, now int64) (id, row, start int, err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.checkClock(now); err != nil {
		return 0, 0, 0, err
	}
	if k < 1 || k > maxParty {
		return 0, 0, 0, fmt.Errorf("%w: k=%d", ErrInvalidPartySize, k)
	}
	row, start, ok := r.selectSeats(k, now)
	if !ok {
		return 0, 0, 0, ErrNoSeats
	}
	id = r.nextID
	r.nextID++
	r.holds[id] = &hold{id: id, row: row, start: start, k: k, created: now, expiry: now + r.ttl}
	r.accept(now)
	return id, row, start, nil
}

// Confirm turns an active hold into a permanent occupation.
func (r *Registry) Confirm(id int, now int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.checkClock(now); err != nil {
		return err
	}
	h, err := r.activeHold(id, now)
	if err != nil {
		return err
	}
	h.state = holdConfirmed
	h.closed = now
	r.accept(now)
	return nil
}

// Release frees the seats of an active hold immediately.
func (r *Registry) Release(id int, now int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.checkClock(now); err != nil {
		return err
	}
	h, err := r.activeHold(id, now)
	if err != nil {
		return err
	}
	h.state = holdReleased
	h.closed = now
	r.accept(now)
	return nil
}

// activeHold resolves id to an active hold, distinguishing the four
// rejection reasons in priority order: never issued, confirmed,
// released, expired.
func (r *Registry) activeHold(id int, now int64) (*hold, error) {
	h, ok := r.holds[id]
	if !ok {
		return nil, fmt.Errorf("%w: id=%d", ErrHoldNotFound, id)
	}
	if h.state == holdConfirmed {
		return nil, fmt.Errorf("%w: id=%d", ErrHoldConfirmed, id)
	}
	if h.state == holdReleased {
		return nil, fmt.Errorf("%w: id=%d", ErrHoldReleased, id)
	}
	if now >= h.expiry {
		return nil, fmt.Errorf("%w: id=%d expiry=%d now=%d", ErrHoldExpired, id, h.expiry, now)
	}
	return h, nil
}

// Seats reports the status of every seat at time now. The result is
// indexed [row-1][seat-1]. Seats is a pure query: it never checks or
// updates the maximum seen now and never mutates any state.
func (r *Registry) Seats(now int64) [][]Status {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([][]Status, r.rows)
	for i := range out {
		out[i] = make([]Status, r.width)
	}
	for _, h := range r.holds {
		st, occupied := h.statusAt(now)
		if !occupied {
			continue
		}
		for c := h.start; c < h.start+h.k; c++ {
			out[h.row-1][c-1] = st
		}
	}
	return out
}

// statusAt reports whether the hold occupies its seats at now. A hold
// occupies nothing before it was created; a confirmed hold shows as
// held between creation and confirmation and as confirmed afterwards;
// a released hold frees its seats from the release time on.
func (h *hold) statusAt(now int64) (Status, bool) {
	if now < h.created {
		return Free, false
	}
	switch h.state {
	case holdConfirmed:
		if now >= h.closed {
			return Confirmed, true
		}
	case holdReleased:
		if now >= h.closed {
			return Free, false
		}
	}
	// Active, or viewed before its confirm/release took effect.
	if now < h.expiry {
		return Held, true
	}
	return Free, false
}

// candidate is one selectable segment [s, s+k-1] in a row.
type candidate struct {
	row    int
	start  int // 1-based
	dev    int // |(2s+k-1) - (W+1)|
	orphan bool
}

// selectSeats finds the segment a Hold of k seats would pick at now.
// Pass 1 considers only orphan-free candidates across all rows; pass 2
// (all candidates) is used only when pass 1 is empty. Ordering: row
// ascending, then center deviation ascending, then start ascending.
func (r *Registry) selectSeats(k int, now int64) (row, start int, ok bool) {
	free := r.freeGrid(now)
	var all, clean []candidate
	for ri := 0; ri < r.rows; ri++ {
		for s := 1; s+k-1 <= r.width; s++ {
			cand, valid := r.evalCandidate(free[ri], ri+1, s, k)
			if !valid {
				continue
			}
			all = append(all, cand)
			if !cand.orphan {
				clean = append(clean, cand)
			}
		}
	}
	pool := clean
	if len(pool) == 0 {
		pool = all
	}
	if len(pool) == 0 {
		return 0, 0, false
	}
	best := pool[0]
	for _, c := range pool[1:] {
		if c.row < best.row ||
			(c.row == best.row && c.dev < best.dev) ||
			(c.row == best.row && c.dev == best.dev && c.start < best.start) {
			best = c
		}
	}
	return best.row, best.start, true
}

// evalCandidate checks the segment [s, s+k-1] of one row and computes
// its center deviation and orphan flag. The orphan flag is set when the
// maximal free run immediately left or right of the segment has length
// exactly 1 (a lone free seat next to a wall counts; length 0 or >=2
// does not).
func (r *Registry) evalCandidate(free []bool, row, s, k int) (candidate, bool) {
	for c := s; c < s+k; c++ {
		if !free[c-1] {
			return candidate{}, false
		}
	}
	left := 0
	for c := s - 1; c >= 1 && free[c-1]; c-- {
		left++
	}
	right := 0
	for c := s + k; c <= r.width && free[c-1]; c++ {
		right++
	}
	dev := 2*s + k - 1 - (r.width + 1)
	if dev < 0 {
		dev = -dev
	}
	return candidate{
		row:    row,
		start:  s,
		dev:    dev,
		orphan: left == 1 || right == 1,
	}, true
}

// freeGrid marks every seat free (true) or occupied (false) at now.
func (r *Registry) freeGrid(now int64) [][]bool {
	free := make([][]bool, r.rows)
	for i := range free {
		row := make([]bool, r.width)
		for j := range row {
			row[j] = true
		}
		free[i] = row
	}
	for _, h := range r.holds {
		if _, occupied := h.statusAt(now); !occupied {
			continue
		}
		for c := h.start; c < h.start+h.k; c++ {
			free[h.row-1][c-1] = false
		}
	}
	return free
}

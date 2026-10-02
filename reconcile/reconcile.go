// Package reconcile implements a deterministic four-round reconciliation
// matcher between bank statement lines and general-ledger (book) lines.
package reconcile

import (
	"errors"
	"sort"
	"sync"
)

// Side identifies which side of the reconciliation a line belongs to.
type Side int

const (
	// Bank is the bank-statement side.
	Bank Side = iota
	// Book is the general-ledger side.
	Book
)

const (
	maxLinesPerSide = 100000
	maxAbsAmount    = int64(1_000_000_000_000) // 10^12 cents
	maxDay          = int64(1_000_000_000)
	maxWindow       = int64(1_000_000_000)
)

var (
	ErrInvalidSide   = errors.New("reconcile: invalid side")
	ErrEmptyID       = errors.New("reconcile: empty line id")
	ErrInvalidAmount = errors.New("reconcile: amount is zero or out of range")
	ErrInvalidDay    = errors.New("reconcile: day out of range")
	ErrSideFull      = errors.New("reconcile: side already holds 100000 lines")
	ErrDuplicateID   = errors.New("reconcile: duplicate line id on this side")
	ErrInvalidWindow = errors.New("reconcile: date tolerance out of range")
	ErrMatchNotFound = errors.New("reconcile: match id never produced")
	ErrMatchReversed = errors.New("reconcile: match already reversed")
)

// Line is a single statement/ledger row.
type Line struct {
	ID  string
	Amt int64
	Day int64
	Ref string
}

// Match describes one produced match. BankIDs and BookIDs are sorted in
// ascending byte order.
type Match struct {
	MID     int64
	Round   int
	BankIDs []string
	BookIDs []string
}

// Pair is a forbidden (bank line id, book line id) combination.
type Pair struct {
	BankID string
	BookID string
}

type line struct {
	id      string
	amt     int64
	day     int64
	ref     string
	matched bool
}

type match struct {
	mid     int64
	round   int
	bankIDs []string
	bookIDs []string
	active  bool
}

// Matcher holds all reconciliation state. It is safe for concurrent use;
// every operation behaves as if executed in some serial order and Reconcile
// runs all four rounds atomically.
type Matcher struct {
	mu        sync.Mutex
	bank      map[string]*line
	book      map[string]*line
	matches   map[int64]*match
	forbidden map[Pair]struct{}
	nextMID   int64
}

// New returns an empty Matcher.
func New() *Matcher {
	return &Matcher{
		bank:      make(map[string]*line),
		book:      make(map[string]*line),
		matches:   make(map[int64]*match),
		forbidden: make(map[Pair]struct{}),
		nextMID:   1,
	}
}

// AddLine registers one line on the given side.
//
// Validation order: invalid side, invalid line (empty id, zero or
// out-of-range amount, day out of range, side full), duplicate id. Only the
// first reason is reported and a rejected call changes nothing.
func (m *Matcher) AddLine(side Side, id string, amt int64, day int64, ref string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if side != Bank && side != Book {
		return ErrInvalidSide
	}
	if id == "" {
		return ErrEmptyID
	}
	if amt == 0 || amt > maxAbsAmount || amt < -maxAbsAmount {
		return ErrInvalidAmount
	}
	if day < 0 || day > maxDay {
		return ErrInvalidDay
	}
	lines := m.bank
	if side == Book {
		lines = m.book
	}
	if len(lines) >= maxLinesPerSide {
		return ErrSideFull
	}
	if _, dup := lines[id]; dup {
		return ErrDuplicateID
	}
	lines[id] = &line{id: id, amt: amt, day: day, ref: ref}
	return nil
}

// Reconcile runs the four rounds over all currently unmatched lines.
// The whole call is one atomic step.
func (m *Matcher) Reconcile(w int64) ([]Match, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if w < 0 || w > maxWindow {
		return nil, ErrInvalidWindow
	}
	var produced []Match
	produced = append(produced, m.roundPair(w, 1, true)...)
	produced = append(produced, m.roundPair(w, 2, false)...)
	produced = append(produced, m.roundOneToMany(w)...)
	produced = append(produced, m.roundManyToOne(w)...)
	return produced, nil
}

// roundPair implements rounds 1 and 2: each unmatched bank line (ascending
// id) picks the unmatched book line with equal amount, date distance at most
// w and no forbidden pair; with useRef the book line must additionally have
// a non-empty ref equal to the bank line's ref. Ties break by smallest date
// distance, then smallest book id.
func (m *Matcher) roundPair(w int64, round int, useRef bool) []Match {
	type bucketKey struct {
		ref string
		amt int64
	}
	buckets := make(map[bucketKey][]*line)
	for _, cand := range m.book {
		if cand.matched {
			continue
		}
		if useRef {
			if cand.ref == "" {
				continue
			}
			buckets[bucketKey{cand.ref, cand.amt}] = append(buckets[bucketKey{cand.ref, cand.amt}], cand)
		} else {
			buckets[bucketKey{"", cand.amt}] = append(buckets[bucketKey{"", cand.amt}], cand)
		}
	}
	var produced []Match
	for _, b := range m.sortedUnmatched(m.bank) {
		key := bucketKey{"", b.amt}
		if useRef {
			key.ref = b.ref
		}
		var best *line
		var bestDist int64
		for _, cand := range buckets[key] {
			if cand.matched {
				continue
			}
			if m.isForbidden(b.id, cand.id) {
				continue
			}
			dist := absDiff(b.day, cand.day)
			if dist > w {
				continue
			}
			if best == nil || dist < bestDist || (dist == bestDist && cand.id < best.id) {
				best, bestDist = cand, dist
			}
		}
		if best == nil {
			continue
		}
		produced = append(produced, m.commit(round, []string{b.id}, []string{best.id}))
	}
	return produced
}

// roundOneToMany implements round 3: each unmatched bank line with a
// non-empty ref (ascending id) gathers the set S of all unmatched book lines
// with the same ref, date distance at most w and no forbidden pair. If
// |S| >= 2 and the amounts of S sum to the bank amount, the whole set is
// matched at once; no subset is ever tried.
func (m *Matcher) roundOneToMany(w int64) []Match {
	byRef := make(map[string][]*line)
	for _, cand := range m.book {
		if !cand.matched {
			byRef[cand.ref] = append(byRef[cand.ref], cand)
		}
	}
	var produced []Match
	for _, b := range m.sortedUnmatched(m.bank) {
		if b.ref == "" {
			continue
		}
		var set []*line
		var sum int64
		for _, cand := range byRef[b.ref] {
			if cand.matched || m.isForbidden(b.id, cand.id) || absDiff(b.day, cand.day) > w {
				continue
			}
			set = append(set, cand)
			sum += cand.amt
		}
		if len(set) < 2 || sum != b.amt {
			continue
		}
		bookIDs := make([]string, 0, len(set))
		for _, cand := range set {
			bookIDs = append(bookIDs, cand.id)
		}
		produced = append(produced, m.commit(3, []string{b.id}, bookIDs))
	}
	return produced
}

// roundManyToOne implements round 4, the mirror image of round 3 with the
// sides swapped.
func (m *Matcher) roundManyToOne(w int64) []Match {
	byRef := make(map[string][]*line)
	for _, cand := range m.bank {
		if !cand.matched {
			byRef[cand.ref] = append(byRef[cand.ref], cand)
		}
	}
	var produced []Match
	for _, k := range m.sortedUnmatched(m.book) {
		if k.ref == "" {
			continue
		}
		var set []*line
		var sum int64
		for _, cand := range byRef[k.ref] {
			if cand.matched || m.isForbidden(cand.id, k.id) || absDiff(k.day, cand.day) > w {
				continue
			}
			set = append(set, cand)
			sum += cand.amt
		}
		if len(set) < 2 || sum != k.amt {
			continue
		}
		bankIDs := make([]string, 0, len(set))
		for _, cand := range set {
			bankIDs = append(bankIDs, cand.id)
		}
		produced = append(produced, m.commit(4, bankIDs, []string{k.id}))
	}
	return produced
}

// commit marks the given lines matched and records a new match with the next
// match id.
func (m *Matcher) commit(round int, bankIDs, bookIDs []string) Match {
	sort.Strings(bankIDs)
	sort.Strings(bookIDs)
	for _, id := range bankIDs {
		m.bank[id].matched = true
	}
	for _, id := range bookIDs {
		m.book[id].matched = true
	}
	mt := &match{mid: m.nextMID, round: round, bankIDs: bankIDs, bookIDs: bookIDs, active: true}
	m.matches[mt.mid] = mt
	m.nextMID++
	return Match{MID: mt.mid, Round: mt.round, BankIDs: bankIDs, BookIDs: bookIDs}
}

// Reverse undoes a still-active match: all of its lines become unmatched
// again and every (bank, book) combination of the match is added to the
// forbidden set, so the same combination can never be re-matched. The match
// id is not recycled. It returns the restored lines of the match.
func (m *Matcher) Reverse(mid int64) (Match, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	mt, ok := m.matches[mid]
	if !ok {
		return Match{}, ErrMatchNotFound
	}
	if !mt.active {
		return Match{}, ErrMatchReversed
	}
	mt.active = false
	for _, id := range mt.bankIDs {
		m.bank[id].matched = false
	}
	for _, id := range mt.bookIDs {
		m.book[id].matched = false
	}
	for _, b := range mt.bankIDs {
		for _, k := range mt.bookIDs {
			m.forbidden[Pair{BankID: b, BookID: k}] = struct{}{}
		}
	}
	return Match{MID: mt.mid, Round: mt.round, BankIDs: mt.bankIDs, BookIDs: mt.bookIDs}, nil
}

// Unmatched returns the unmatched lines of one side, sorted by id.
func (m *Matcher) Unmatched(side Side) []Line {
	m.mu.Lock()
	defer m.mu.Unlock()
	var lines map[string]*line
	switch side {
	case Bank:
		lines = m.bank
	case Book:
		lines = m.book
	default:
		return nil
	}
	var out []Line
	for _, l := range m.sortedUnmatched(lines) {
		out = append(out, Line{ID: l.id, Amt: l.amt, Day: l.day, Ref: l.ref})
	}
	return out
}

// Matches returns all active matches ordered by smallest bank id, then mid.
func (m *Matcher) Matches() []Match {
	m.mu.Lock()
	defer m.mu.Unlock()
	var active []*match
	for _, mt := range m.matches {
		if mt.active {
			active = append(active, mt)
		}
	}
	sort.Slice(active, func(i, j int) bool {
		if active[i].bankIDs[0] != active[j].bankIDs[0] {
			return active[i].bankIDs[0] < active[j].bankIDs[0]
		}
		return active[i].mid < active[j].mid
	})
	out := make([]Match, 0, len(active))
	for _, mt := range active {
		out = append(out, Match{
			MID:     mt.mid,
			Round:   mt.round,
			BankIDs: append([]string(nil), mt.bankIDs...),
			BookIDs: append([]string(nil), mt.bookIDs...),
		})
	}
	return out
}

// Forbidden returns all forbidden pairs, sorted by bank id then book id.
func (m *Matcher) Forbidden() []Pair {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Pair, 0, len(m.forbidden))
	for p := range m.forbidden {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].BankID != out[j].BankID {
			return out[i].BankID < out[j].BankID
		}
		return out[i].BookID < out[j].BookID
	})
	return out
}

func (m *Matcher) isForbidden(bankID, bookID string) bool {
	_, ok := m.forbidden[Pair{BankID: bankID, BookID: bookID}]
	return ok
}

func (m *Matcher) sortedUnmatched(lines map[string]*line) []*line {
	out := make([]*line, 0, len(lines))
	for _, l := range lines {
		if !l.matched {
			out = append(out, l)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].id < out[j].id })
	return out
}

func absDiff(a, b int64) int64 {
	if a >= b {
		return a - b
	}
	return b - a
}

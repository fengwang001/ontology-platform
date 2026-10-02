package reconcile_test

// A deliberately naive, literal step-by-step implementation of the matching
// rules, used as an independent oracle for the randomized comparison test.
// It keeps plain slices and re-scans everything, mirroring the specification
// text as directly as possible.

import (
	"sort"

	"ontology/reconcile"
)

type naiveLine struct {
	id      string
	amt     int64
	day     int64
	ref     string
	matched bool
}

type naiveMatch struct {
	mid     int64
	round   int
	bankIDs []string
	bookIDs []string
	active  bool
}

type naive struct {
	bank      []*naiveLine
	book      []*naiveLine
	bankIDs   map[string]bool
	bookIDs   map[string]bool
	matches   map[int64]*naiveMatch
	forbidden map[[2]string]bool
	nextMid   int64
}

func newNaive() *naive {
	return &naive{
		bankIDs:   make(map[string]bool),
		bookIDs:   make(map[string]bool),
		matches:   make(map[int64]*naiveMatch),
		forbidden: make(map[[2]string]bool),
		nextMid:   1,
	}
}

func (n *naive) addLine(side reconcile.Side, id string, amt, day int64, ref string) error {
	if side != reconcile.Bank && side != reconcile.Book {
		return reconcile.ErrInvalidSide
	}
	if id == "" {
		return reconcile.ErrEmptyID
	}
	if amt == 0 || amt > 1_000_000_000_000 || amt < -1_000_000_000_000 {
		return reconcile.ErrInvalidAmount
	}
	if day < 0 || day > 1_000_000_000 {
		return reconcile.ErrInvalidDay
	}
	seen := n.bankIDs
	if side == reconcile.Book {
		seen = n.bookIDs
	}
	if len(seen) >= 100000 {
		return reconcile.ErrSideFull
	}
	if seen[id] {
		return reconcile.ErrDuplicateID
	}
	seen[id] = true
	l := &naiveLine{id: id, amt: amt, day: day, ref: ref}
	if side == reconcile.Bank {
		n.bank = append(n.bank, l)
	} else {
		n.book = append(n.book, l)
	}
	return nil
}

func naiveAbs(a, b int64) int64 {
	if a >= b {
		return a - b
	}
	return b - a
}

func naiveSortedUnmatched(lines []*naiveLine) []*naiveLine {
	var out []*naiveLine
	for _, l := range lines {
		if !l.matched {
			out = append(out, l)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].id < out[j].id })
	return out
}

func (n *naive) commit(round int, bankIDs, bookIDs []string) reconcile.Match {
	sort.Strings(bankIDs)
	sort.Strings(bookIDs)
	for _, l := range n.bank {
		for _, id := range bankIDs {
			if l.id == id {
				l.matched = true
			}
		}
	}
	for _, l := range n.book {
		for _, id := range bookIDs {
			if l.id == id {
				l.matched = true
			}
		}
	}
	mt := &naiveMatch{mid: n.nextMid, round: round, bankIDs: bankIDs, bookIDs: bookIDs, active: true}
	n.matches[mt.mid] = mt
	n.nextMid++
	return reconcile.Match{MID: mt.mid, Round: mt.round, BankIDs: bankIDs, BookIDs: bookIDs}
}

// Rounds 1 and 2: process unmatched bank lines in ascending id order; each
// takes the best book line (smallest date distance, then smallest id).
func (n *naive) naiveRoundPair(w int64, round int, useRef bool) []reconcile.Match {
	var produced []reconcile.Match
	for _, b := range naiveSortedUnmatched(n.bank) {
		var best *naiveLine
		var bestDist int64
		for _, k := range n.book {
			if k.matched {
				continue
			}
			if useRef && (k.ref == "" || k.ref != b.ref) {
				continue
			}
			if k.amt != b.amt {
				continue
			}
			if n.forbidden[[2]string{b.id, k.id}] {
				continue
			}
			dist := naiveAbs(b.day, k.day)
			if dist > w {
				continue
			}
			if best == nil || dist < bestDist || (dist == bestDist && k.id < best.id) {
				best, bestDist = k, dist
			}
		}
		if best != nil {
			produced = append(produced, n.commit(round, []string{b.id}, []string{best.id}))
		}
	}
	return produced
}

// Round 3: one bank line to many book lines sharing its non-empty ref.
func (n *naive) naiveRoundOneToMany(w int64) []reconcile.Match {
	var produced []reconcile.Match
	for _, b := range naiveSortedUnmatched(n.bank) {
		if b.ref == "" {
			continue
		}
		var set []string
		var sum int64
		for _, k := range n.book {
			if k.matched || k.ref != b.ref {
				continue
			}
			if n.forbidden[[2]string{b.id, k.id}] {
				continue
			}
			if naiveAbs(b.day, k.day) > w {
				continue
			}
			set = append(set, k.id)
			sum += k.amt
		}
		if len(set) >= 2 && sum == b.amt {
			produced = append(produced, n.commit(3, []string{b.id}, set))
		}
	}
	return produced
}

// Round 4: many bank lines to one book line sharing its non-empty ref.
func (n *naive) naiveRoundManyToOne(w int64) []reconcile.Match {
	var produced []reconcile.Match
	for _, k := range naiveSortedUnmatched(n.book) {
		if k.ref == "" {
			continue
		}
		var set []string
		var sum int64
		for _, b := range n.bank {
			if b.matched || b.ref != k.ref {
				continue
			}
			if n.forbidden[[2]string{b.id, k.id}] {
				continue
			}
			if naiveAbs(k.day, b.day) > w {
				continue
			}
			set = append(set, b.id)
			sum += b.amt
		}
		if len(set) >= 2 && sum == k.amt {
			produced = append(produced, n.commit(4, set, []string{k.id}))
		}
	}
	return produced
}

func (n *naive) reconcile(w int64) ([]reconcile.Match, error) {
	if w < 0 || w > 1_000_000_000 {
		return nil, reconcile.ErrInvalidWindow
	}
	var produced []reconcile.Match
	produced = append(produced, n.naiveRoundPair(w, 1, true)...)
	produced = append(produced, n.naiveRoundPair(w, 2, false)...)
	produced = append(produced, n.naiveRoundOneToMany(w)...)
	produced = append(produced, n.naiveRoundManyToOne(w)...)
	return produced, nil
}

func (n *naive) reverse(mid int64) (reconcile.Match, error) {
	mt, ok := n.matches[mid]
	if !ok {
		return reconcile.Match{}, reconcile.ErrMatchNotFound
	}
	if !mt.active {
		return reconcile.Match{}, reconcile.ErrMatchReversed
	}
	mt.active = false
	for _, l := range n.bank {
		for _, id := range mt.bankIDs {
			if l.id == id {
				l.matched = false
			}
		}
	}
	for _, l := range n.book {
		for _, id := range mt.bookIDs {
			if l.id == id {
				l.matched = false
			}
		}
	}
	for _, b := range mt.bankIDs {
		for _, k := range mt.bookIDs {
			n.forbidden[[2]string{b, k}] = true
		}
	}
	return reconcile.Match{MID: mt.mid, Round: mt.round, BankIDs: mt.bankIDs, BookIDs: mt.bookIDs}, nil
}

func (n *naive) unmatched(side reconcile.Side) []reconcile.Line {
	lines := n.bank
	if side == reconcile.Book {
		lines = n.book
	}
	var out []reconcile.Line
	for _, l := range naiveSortedUnmatched(lines) {
		out = append(out, reconcile.Line{ID: l.id, Amt: l.amt, Day: l.day, Ref: l.ref})
	}
	return out
}

func (n *naive) activeMatches() []reconcile.Match {
	var active []*naiveMatch
	for _, mt := range n.matches {
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
	out := make([]reconcile.Match, 0, len(active))
	for _, mt := range active {
		out = append(out, reconcile.Match{MID: mt.mid, Round: mt.round, BankIDs: mt.bankIDs, BookIDs: mt.bookIDs})
	}
	return out
}

func (n *naive) forbiddenPairs() []reconcile.Pair {
	out := make([]reconcile.Pair, 0, len(n.forbidden))
	for p := range n.forbidden {
		out = append(out, reconcile.Pair{BankID: p[0], BookID: p[1]})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].BankID != out[j].BankID {
			return out[i].BankID < out[j].BankID
		}
		return out[i].BookID < out[j].BookID
	})
	return out
}

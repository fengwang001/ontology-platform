// Package broadphase maintains broad-phase collision pairs and contact
// pairs among 2D integer rectangle objects using inflated ("fat")
// bounding boxes and an x-axis endpoint sweep.
//
// Each object owns a tight box and a fat box. The fat box is the tight
// box expanded by a fixed margin M on every side, taken at Insert time
// and recomputed ("refit") only when a Move pushes the tight box outside
// the current fat box; the new fat box replaces the old one (no union).
// Two boxes intersect iff on both axes one's lo is strictly less than
// the other's hi, so touching edges do not count. A broad-phase pair is
// a collidable pair of objects whose fat boxes intersect; a contact pair
// is a collidable pair whose tight boxes intersect. Two objects are
// collidable iff (a.layer&b.mask) != 0 and (b.layer&a.mask) != 0.
//
// All fat-box x endpoints are kept sorted by (coordinate, type, id) with
// hi endpoints ordered before lo endpoints at equal coordinates. A refit
// repositions the object's two endpoints; the counter crossed
// accumulates endpoint-order flips against other objects' endpoints, and
// pairChecks counts y-overlap comparisons made exactly when a lo
// endpoint flips with a hi endpoint of a collidable object, so
// pairChecks never exceeds crossed.
//
// All methods are safe for concurrent use and behave as if executed in
// some serial order; replaying the same operation sequence yields the
// same events and final pair sets.
package broadphase

import (
	"errors"
	"fmt"
	"sort"
	"sync"
)

var (
	// ErrInvalidParam reports out-of-range constructor arguments, ids,
	// boxes or filter bits.
	ErrInvalidParam = errors.New("broadphase: invalid parameter")
	// ErrAlreadyExists reports an Insert with an already registered id.
	ErrAlreadyExists = errors.New("broadphase: object already exists")
	// ErrNotExists reports an operation on an unregistered id.
	ErrNotExists = errors.New("broadphase: object does not exist")
	// ErrFull reports an Insert when the object count reached capacity.
	ErrFull = errors.New("broadphase: object capacity reached")
)

const (
	maxMargin   int64 = 1_000_000
	maxCapacity       = 100_000
	minCoord    int64 = -1_000_000_000
	maxCoord    int64 = 1_000_000_000
	maxFilter         = 65_535
)

// Box is a half-open axis-aligned rectangle [Lx,Hx) x [Ly,Hy).
type Box struct {
	Lx, Hx, Ly, Hy int64
}

// intersects reports whether two boxes overlap on both axes; touching
// edges (lo == hi) do not count.
func (b Box) intersects(o Box) bool {
	return b.Lx < o.Hx && o.Lx < b.Hx && b.Ly < o.Hy && o.Ly < b.Hy
}

// yOverlap reports whether two boxes overlap on the y axis.
func yOverlap(a, b Box) bool {
	return a.Ly < b.Hy && b.Ly < a.Hy
}

// contains reports whether o lies entirely inside b on both axes.
func (b Box) contains(o Box) bool {
	return o.Lx >= b.Lx && o.Hx <= b.Hx && o.Ly >= b.Ly && o.Hy <= b.Hy
}

func validBox(t Box) bool {
	return t.Lx >= minCoord && t.Hx <= maxCoord && t.Lx < t.Hx &&
		t.Ly >= minCoord && t.Hy <= maxCoord && t.Ly < t.Hy
}

func validFilter(f int) bool { return f >= 0 && f <= maxFilter }

// Pair identifies an unordered object pair with A < B.
type Pair struct {
	A, B int64
}

func makePair(x, y int64) Pair {
	if x < y {
		return Pair{A: x, B: y}
	}
	return Pair{A: y, B: x}
}

func sortPairs(ps []Pair) {
	sort.Slice(ps, func(i, j int) bool {
		if ps[i].A != ps[j].A {
			return ps[i].A < ps[j].A
		}
		return ps[i].B < ps[j].B
	})
}

// Events lists the pair-set transitions caused by one accepted
// operation. Every list is sorted by (A, B) and never nil.
type Events struct {
	FatEnter     []Pair
	FatExit      []Pair
	ContactEnter []Pair
	ContactExit  []Pair
}

// MoveEvents is the result of Move; Refit reports whether the fat box
// was recomputed.
type MoveEvents struct {
	Events
	Refit bool
}

type object struct {
	tight, fat  Box
	layer, mask uint16
}

func collidable(a, b *object) bool {
	return a.layer&b.mask != 0 && b.layer&a.mask != 0
}

// endpoint is one fat-box x endpoint. Endpoints are kept sorted by
// (coord, id) in per-type slices; the combined sweep order is
// (coord, type, id) with hi before lo at equal coordinates.
type endpoint struct {
	coord int64
	id    int64
}

// Broadphase maintains the pair sets. The zero value is not usable;
// construct with New. All methods are safe for concurrent use.
type Broadphase struct {
	mu         sync.RWMutex
	margin     int64
	capacity   int
	objects    map[int64]*object
	los        []endpoint // lo endpoints sorted by (coord, id)
	his        []endpoint // hi endpoints sorted by (coord, id)
	fatPairs   map[Pair]struct{}
	contacts   map[Pair]struct{}
	partners   map[int64]map[int64]struct{}
	crossed    int64
	pairChecks int64
}

// New creates an empty maintainer. margin must be in [0, 1e6] and
// capacity in [1, 1e5].
func New(margin int64, capacity int) (*Broadphase, error) {
	if margin < 0 || margin > maxMargin || capacity < 1 || capacity > maxCapacity {
		return nil, fmt.Errorf("%w: margin=%d capacity=%d", ErrInvalidParam, margin, capacity)
	}
	return &Broadphase{
		margin:   margin,
		capacity: capacity,
		objects:  make(map[int64]*object),
		fatPairs: make(map[Pair]struct{}),
		contacts: make(map[Pair]struct{}),
		partners: make(map[int64]map[int64]struct{}),
	}, nil
}

func (b *Broadphase) expand(t Box) Box {
	return Box{
		Lx: t.Lx - b.margin, Hx: t.Hx + b.margin,
		Ly: t.Ly - b.margin, Hy: t.Hy + b.margin,
	}
}

func endpointLess(a, b endpoint) bool {
	return a.coord < b.coord || (a.coord == b.coord && a.id < b.id)
}

// lowerBound returns the count of endpoints strictly before (coord, id).
func lowerBound(s []endpoint, coord, id int64) int {
	return sort.Search(len(s), func(i int) bool {
		return !endpointLess(s[i], endpoint{coord: coord, id: id})
	})
}

// lowerBoundCoord returns the count of endpoints with coord < c.
func lowerBoundCoord(s []endpoint, c int64) int {
	return sort.Search(len(s), func(i int) bool { return s[i].coord >= c })
}

// upperBoundCoord returns the count of endpoints with coord <= c.
func upperBoundCoord(s []endpoint, c int64) int {
	return sort.Search(len(s), func(i int) bool { return s[i].coord > c })
}

func insertEndpoint(s *[]endpoint, coord, id int64) {
	i := lowerBound(*s, coord, id)
	*s = append(*s, endpoint{})
	copy((*s)[i+1:], (*s)[i:])
	(*s)[i] = endpoint{coord: coord, id: id}
}

func removeEndpoint(s *[]endpoint, coord, id int64) {
	i := lowerBound(*s, coord, id)
	copy((*s)[i:], (*s)[i+1:])
	*s = (*s)[:len(*s)-1]
}

// countFlips accumulates the endpoint-order flips caused by moving one
// endpoint of id from oldC to newC. Both endpoints of id must already
// be removed from the slices, so only flips against other objects'
// endpoints are counted. crossed accumulates every flip; a flip between
// a lo and a hi endpoint of collidable objects performs one y-overlap
// comparison, counted in pairChecks.
func (b *Broadphase) countFlips(isLo bool, oldC, newC, id int64, newFat Box) {
	lo, hi := oldC, newC
	if hi < lo {
		lo, hi = hi, lo
	}
	var same, opp []endpoint
	if isLo {
		r1 := lowerBound(b.los, oldC, id)
		r2 := lowerBound(b.los, newC, id)
		if r1 > r2 {
			r1, r2 = r2, r1
		}
		same = b.los[r1:r2]
		// hi endpoints strictly between (lo,lo,id) and (hi,lo,id) in the
		// combined order are those with coord in (lo, hi].
		opp = b.his[upperBoundCoord(b.his, lo):upperBoundCoord(b.his, hi)]
	} else {
		r1 := lowerBound(b.his, oldC, id)
		r2 := lowerBound(b.his, newC, id)
		if r1 > r2 {
			r1, r2 = r2, r1
		}
		same = b.his[r1:r2]
		// lo endpoints strictly between (lo,hi,id) and (hi,hi,id) in the
		// combined order are those with coord in [lo, hi).
		opp = b.los[lowerBoundCoord(b.los, lo):lowerBoundCoord(b.los, hi)]
	}
	b.crossed += int64(len(same) + len(opp))
	o := b.objects[id]
	for _, e := range opp {
		if p := b.objects[e.id]; collidable(o, p) {
			b.pairChecks++
			_ = yOverlap(newFat, p.fat)
		}
	}
}

// xOverlapping lists the ids (excluding exclude) whose fat box x
// interval intersects [ql,qh).
func (b *Broadphase) xOverlapping(ql, qh, exclude int64) []int64 {
	countLo := lowerBoundCoord(b.los, qh)
	countHi := len(b.his) - upperBoundCoord(b.his, ql)
	var out []int64
	if countLo <= countHi {
		for _, e := range b.los[:countLo] {
			if e.id != exclude && b.objects[e.id].fat.Hx > ql {
				out = append(out, e.id)
			}
		}
	} else {
		for _, e := range b.his[len(b.his)-countHi:] {
			if e.id != exclude && b.objects[e.id].fat.Lx < qh {
				out = append(out, e.id)
			}
		}
	}
	return out
}

func (b *Broadphase) addPair(a, c int64) {
	b.fatPairs[makePair(a, c)] = struct{}{}
	if b.partners[a] == nil {
		b.partners[a] = make(map[int64]struct{})
	}
	if b.partners[c] == nil {
		b.partners[c] = make(map[int64]struct{})
	}
	b.partners[a][c] = struct{}{}
	b.partners[c][a] = struct{}{}
}

func (b *Broadphase) removePair(a, c int64) {
	p := makePair(a, c)
	delete(b.fatPairs, p)
	delete(b.contacts, p)
	delete(b.partners[a], c)
	delete(b.partners[c], a)
}

// syncContacts recomputes the contact pairs of id by checking exactly
// its current broad-phase partners.
func (b *Broadphase) syncContacts(id int64) {
	o := b.objects[id]
	for pid := range b.partners[id] {
		pair := makePair(id, pid)
		_, isContact := b.contacts[pair]
		if o.tight.intersects(b.objects[pid].tight) {
			if !isContact {
				b.contacts[pair] = struct{}{}
			}
		} else if isContact {
			delete(b.contacts, pair)
		}
	}
}

// snapshot returns the current fat-pair and contact partners of id.
func (b *Broadphase) snapshot(id int64) (fat, con map[int64]struct{}) {
	fat = make(map[int64]struct{}, len(b.partners[id]))
	con = make(map[int64]struct{})
	for pid := range b.partners[id] {
		fat[pid] = struct{}{}
		if _, ok := b.contacts[makePair(id, pid)]; ok {
			con[pid] = struct{}{}
		}
	}
	return fat, con
}

func makeEvents(self int64, fatB, fatA, conB, conA map[int64]struct{}) Events {
	ev := Events{
		FatEnter:     []Pair{},
		FatExit:      []Pair{},
		ContactEnter: []Pair{},
		ContactExit:  []Pair{},
	}
	for pid := range fatA {
		if _, ok := fatB[pid]; !ok {
			ev.FatEnter = append(ev.FatEnter, makePair(self, pid))
		}
	}
	for pid := range fatB {
		if _, ok := fatA[pid]; !ok {
			ev.FatExit = append(ev.FatExit, makePair(self, pid))
		}
	}
	for pid := range conA {
		if _, ok := conB[pid]; !ok {
			ev.ContactEnter = append(ev.ContactEnter, makePair(self, pid))
		}
	}
	for pid := range conB {
		if _, ok := conA[pid]; !ok {
			ev.ContactExit = append(ev.ContactExit, makePair(self, pid))
		}
	}
	sortPairs(ev.FatEnter)
	sortPairs(ev.FatExit)
	sortPairs(ev.ContactEnter)
	sortPairs(ev.ContactExit)
	return ev
}

// Insert registers an object with the given tight box; the fat box is
// the tight box expanded by M on every side. filter optionally carries
// exactly two values (layer, mask), both defaulting to 1. It returns the
// pairs formed with the already registered objects.
func (b *Broadphase) Insert(id int64, tight Box, filter ...int) (Events, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	layer, mask := 1, 1
	if len(filter) == 2 {
		layer, mask = filter[0], filter[1]
	}
	if id <= 0 || !validBox(tight) || (len(filter) != 0 && len(filter) != 2) ||
		!validFilter(layer) || !validFilter(mask) {
		return Events{}, fmt.Errorf("%w: insert id=%d tight=%+v filter=%v", ErrInvalidParam, id, tight, filter)
	}
	if _, ok := b.objects[id]; ok {
		return Events{}, fmt.Errorf("%w: insert id=%d", ErrAlreadyExists, id)
	}
	if len(b.objects) >= b.capacity {
		return Events{}, fmt.Errorf("%w: insert id=%d", ErrFull, id)
	}

	o := &object{tight: tight, fat: b.expand(tight), layer: uint16(layer), mask: uint16(mask)}
	b.objects[id] = o
	insertEndpoint(&b.los, o.fat.Lx, id)
	insertEndpoint(&b.his, o.fat.Hx, id)
	for _, pid := range b.xOverlapping(o.fat.Lx, o.fat.Hx, id) {
		if p := b.objects[pid]; collidable(o, p) && yOverlap(o.fat, p.fat) {
			b.addPair(id, pid)
		}
	}
	b.syncContacts(id)
	fatA, conA := b.snapshot(id)
	return makeEvents(id, nil, fatA, nil, conA), nil
}

// Move replaces the tight box of id. If the new tight box lies entirely
// inside the current fat box on both axes, the fat box is kept and only
// contacts are recomputed; otherwise the fat box is refit to the new
// tight box expanded by M (never unioned with the old fat box).
func (b *Broadphase) Move(id int64, tight Box) (MoveEvents, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if id <= 0 || !validBox(tight) {
		return MoveEvents{}, fmt.Errorf("%w: move id=%d tight=%+v", ErrInvalidParam, id, tight)
	}
	o, ok := b.objects[id]
	if !ok {
		return MoveEvents{}, fmt.Errorf("%w: move id=%d", ErrNotExists, id)
	}

	fatB, conB := b.snapshot(id)
	refit := !o.fat.contains(tight)
	if refit {
		newFat := b.expand(tight)
		removeEndpoint(&b.los, o.fat.Lx, id)
		removeEndpoint(&b.his, o.fat.Hx, id)
		b.countFlips(true, o.fat.Lx, newFat.Lx, id, newFat)
		b.countFlips(false, o.fat.Hx, newFat.Hx, id, newFat)
		insertEndpoint(&b.los, newFat.Lx, id)
		insertEndpoint(&b.his, newFat.Hx, id)
		o.tight = tight
		o.fat = newFat
		var drop []int64
		for pid := range b.partners[id] {
			if p := b.objects[pid]; !collidable(o, p) || !o.fat.intersects(p.fat) {
				drop = append(drop, pid)
			}
		}
		for _, pid := range drop {
			b.removePair(id, pid)
		}
		for _, pid := range b.xOverlapping(o.fat.Lx, o.fat.Hx, id) {
			if _, ok := b.partners[id][pid]; ok {
				continue
			}
			if p := b.objects[pid]; collidable(o, p) && yOverlap(o.fat, p.fat) {
				b.addPair(id, pid)
			}
		}
	} else {
		o.tight = tight
	}
	b.syncContacts(id)
	fatA, conA := b.snapshot(id)
	return MoveEvents{Events: makeEvents(id, fatB, fatA, conB, conA), Refit: refit}, nil
}

// Remove deletes id; all of its broad-phase and contact pairs exit.
func (b *Broadphase) Remove(id int64) (Events, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if id <= 0 {
		return Events{}, fmt.Errorf("%w: remove id=%d", ErrInvalidParam, id)
	}
	o, ok := b.objects[id]
	if !ok {
		return Events{}, fmt.Errorf("%w: remove id=%d", ErrNotExists, id)
	}

	fatB, conB := b.snapshot(id)
	ps := make([]int64, 0, len(b.partners[id]))
	for pid := range b.partners[id] {
		ps = append(ps, pid)
	}
	for _, pid := range ps {
		b.removePair(id, pid)
	}
	removeEndpoint(&b.los, o.fat.Lx, id)
	removeEndpoint(&b.his, o.fat.Hx, id)
	delete(b.objects, id)
	delete(b.partners, id)
	return makeEvents(id, fatB, nil, conB, nil), nil
}

// SetFilter changes the filter bits of id without touching any box or
// counter; pairs may enter or exit as collidability changes.
func (b *Broadphase) SetFilter(id int64, layer, mask int) (Events, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if id <= 0 || !validFilter(layer) || !validFilter(mask) {
		return Events{}, fmt.Errorf("%w: setfilter id=%d layer=%d mask=%d", ErrInvalidParam, id, layer, mask)
	}
	o, ok := b.objects[id]
	if !ok {
		return Events{}, fmt.Errorf("%w: setfilter id=%d", ErrNotExists, id)
	}

	fatB, conB := b.snapshot(id)
	o.layer = uint16(layer)
	o.mask = uint16(mask)
	var drop []int64
	for pid := range b.partners[id] {
		if !collidable(o, b.objects[pid]) {
			drop = append(drop, pid)
		}
	}
	for _, pid := range drop {
		b.removePair(id, pid)
	}
	for _, pid := range b.xOverlapping(o.fat.Lx, o.fat.Hx, id) {
		if _, ok := b.partners[id][pid]; ok {
			continue
		}
		if p := b.objects[pid]; collidable(o, p) && yOverlap(o.fat, p.fat) {
			b.addPair(id, pid)
		}
	}
	b.syncContacts(id)
	fatA, conA := b.snapshot(id)
	return makeEvents(id, fatB, fatA, conB, conA), nil
}

// Pairs returns all current broad-phase pairs, sorted by (A, B).
func (b *Broadphase) Pairs() []Pair {
	b.mu.RLock()
	defer b.mu.RUnlock()
	out := make([]Pair, 0, len(b.fatPairs))
	for p := range b.fatPairs {
		out = append(out, p)
	}
	sortPairs(out)
	return out
}

// Contacts returns all current contact pairs, sorted by (A, B).
func (b *Broadphase) Contacts() []Pair {
	b.mu.RLock()
	defer b.mu.RUnlock()
	out := make([]Pair, 0, len(b.contacts))
	for p := range b.contacts {
		out = append(out, p)
	}
	sortPairs(out)
	return out
}

// FatBox returns the current fat box of id.
func (b *Broadphase) FatBox(id int64) (Box, bool) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	if o, ok := b.objects[id]; ok {
		return o.fat, true
	}
	return Box{}, false
}

// Tight returns the current tight box of id.
func (b *Broadphase) Tight(id int64) (Box, bool) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	if o, ok := b.objects[id]; ok {
		return o.tight, true
	}
	return Box{}, false
}

// Crossed returns the total number of endpoint-order flips recorded by
// refitting Moves.
func (b *Broadphase) Crossed() int64 {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.crossed
}

// PairChecks returns the total number of y-overlap comparisons performed
// on lo/hi endpoint flips between collidable objects.
func (b *Broadphase) PairChecks() int64 {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.pairChecks
}

// Len returns the number of registered objects.
func (b *Broadphase) Len() int {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return len(b.objects)
}

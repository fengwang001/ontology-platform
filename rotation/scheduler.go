// Package rotation: scheduler, clock and per-boundary settlement live here.
package rotation

import (
	"sort"
	"sync"
)

// Logger receives one deterministic line per operation and boundary step.
type Logger interface {
	Logf(format string, args ...any)
}

// Scheduler is the entry point. One lock serializes every operation, which
// makes concurrent calls equivalent to the serial order of lock acquisition.
type Scheduler struct {
	mu sync.Mutex

	now     int64
	regions map[string]*regionState
	nextOrd int

	logger Logger

	// assessments is the append-only confirmation-violation record.
	assessments []Assessment
	// decisions stores the frozen selected group ids per region and slot.
	decisions map[string]map[int64][]int
}

// regionState carries the dynamic state of one region.
//
// Time is settled in two phases around a boundary b:
//   - arriving at b: accrue the ended slot [b-L,b), promote the previously
//     pre-picked selection for [b,b+L) to "current" (reconciling it against
//     operations issued exactly at b), then pre-pick [b+L,b+2L) and create
//     its notices (the confirmation window for that slot);
//   - leaving b (clock moves strictly beyond b): the current slot freezes and
//     outstanding notices are assessed as unconfirmed.
//
// If the clock stops exactly at b the current slot stays mutable, which is
// precisely why "change/cancel exactly on the boundary" differs from "one
// instant after it".
type regionState struct {
	region *Region
	book   *orderBook

	curStart    int64
	curEnd      int64
	curFrozen   bool
	curPick     []int
	curAssessed bool

	nextStart int64
	nextPick  []int

	curNotices  map[string]*slotNotice
	nextNotices map[string]*slotNotice
}

type slotNotice struct {
	limit     int64
	confirmed bool
	withdrawn bool
	assessed  bool
}

// New creates an empty scheduler; nil logger disables logging.
func New(logger Logger) *Scheduler {
	return &Scheduler{
		regions:   map[string]*regionState{},
		nextOrd:   1,
		logger:    logger,
		decisions: map[string]map[int64][]int{},
	}
}

func (s *Scheduler) log(format string, args ...any) {
	if s.logger != nil {
		s.logger.Logf(format, args...)
	}
}

// --- topology ---------------------------------------------------------------

// AddRegion creates a region with fixed slot length slotLen (> 0).
func (s *Scheduler) AddRegion(id string, slotLen int64) error {
	if id == "" || slotLen <= 0 {
		return errf(RejectInvalid, "invalid region %q slotLen %d", id, slotLen)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.regions[id]; ok {
		return errf(RejectInvalid, "region %q already exists", id)
	}
	r := &Region{ID: id, slotLen: slotLen, groups: map[int]*Group{}, users: map[string]*User{}}
	st := newRegionState(s, r)
	s.regions[id] = st
	s.decisions[id] = map[int64][]int{}
	s.log("AddRegion id=%s slotLen=%d -> ok curSlot=[%d,%d)", id, slotLen, st.curStart, st.curEnd)
	return nil
}

func newRegionState(s *Scheduler, r *Region) *regionState {
	st := &regionState{
		region:      r,
		book:        newOrderBook(r),
		curStart:    s.now - modNonNeg(s.now, r.slotLen),
		curNotices:  map[string]*slotNotice{},
		nextNotices: map[string]*slotNotice{},
	}
	st.curEnd = st.curStart + r.slotLen
	// The pre-notified frame is exactly the following slot [curEnd, ...).
	st.nextStart = st.curEnd
	// Inside a slot at creation the current slot is already immutable; on a
	// boundary it stays open. The next slot is pre-picked and notifiable.
	st.curFrozen = s.now > st.curStart
	st.curPick = nil
	st.nextPick = pickProjected(st, st.book.effectiveLevel(st.nextStart))
	st.generateNotices(st.nextPick, st.nextNotices)
	return st
}

// AddGroup adds a rotation group.
func (s *Scheduler) AddGroup(regionID string, groupID int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, err := s.requireRegion(regionID)
	if err != nil {
		return err
	}
	if groupID <= 0 {
		return errf(RejectInvalid, "group id must be positive, got %d", groupID)
	}
	if _, ok := st.region.groups[groupID]; ok {
		return errf(RejectInvalid, "group %d already exists in %s", groupID, regionID)
	}
	st.region.groups[groupID] = &Group{ID: groupID, region: st.region}
	st.region.groupIDs = append(st.region.groupIDs, groupID)
	sort.Ints(st.region.groupIDs)
	s.log("AddGroup region=%s id=%d -> ok", regionID, groupID)
	return nil
}

// AddUser adds a user into a group. reservedPower is used only for Reserved.
func (s *Scheduler) AddUser(userID string, regionID string, groupID int, cat Category, reservedPower int64) error {
	if userID == "" || cat < Exempt || cat > Normal || reservedPower < 0 {
		return errf(RejectInvalid, "invalid user arguments: %q cat=%d power=%d", userID, cat, reservedPower)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	st, err := s.requireRegion(regionID)
	if err != nil {
		return err
	}
	if _, ok := st.region.users[userID]; ok {
		return errf(RejectInvalid, "user %q already exists", userID)
	}
	if _, ok := st.region.groups[groupID]; !ok {
		return errf(RejectInvalid, "group %d not found in region %s", groupID, regionID)
	}
	u := &User{ID: userID, category: cat, groupID: groupID, reservedPower: reservedPower}
	st.region.users[userID] = u
	st.region.groups[groupID].members = append(st.region.groups[groupID].members, userID)
	s.log("AddUser user=%s region=%s group=%d cat=%d power=%d -> ok", userID, regionID, groupID, cat, reservedPower)
	return nil
}

// MoveUser moves a user to another group. Refused with 时段进行中 while the
// user's current group is limited in the current slot.
func (s *Scheduler) MoveUser(userID, regionID string, toGroup int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, err := s.requireRegion(regionID)
	if err != nil {
		return err
	}
	u, ok := st.region.users[userID]
	if !ok {
		return errf(RejectInvalid, "user %q not found", userID)
	}
	if _, ok := st.region.groups[toGroup]; !ok {
		return errf(RejectInvalid, "group %d not found in region %s", toGroup, regionID)
	}
	if u.groupID == toGroup {
		return errf(RejectInvalid, "user %q already in group %d", userID, toGroup)
	}
	if st.groupSelected(u.groupID) {
		s.log("MoveUser user=%s -> %d REJECT in-slot group=%d slot=%d", userID, toGroup, u.groupID, st.curStart)
		return errf(RejectInSlot, "时段进行中: group %d limited in slot %d", u.groupID, st.curStart)
	}
	old := st.region.groups[u.groupID]
	old.members = removeString(old.members, userID)
	u.groupID = toGroup
	st.region.groups[toGroup].members = append(st.region.groups[toGroup].members, userID)
	s.log("MoveUser user=%s %d->%d -> ok", userID, old.ID, toGroup)
	return nil
}

// ChangeCategory changes a user's class (and reserved power); same in-slot
// guard as MoveUser.
func (s *Scheduler) ChangeCategory(userID, regionID string, cat Category, reservedPower int64) error {
	if cat < Exempt || cat > Normal || reservedPower < 0 {
		return errf(RejectInvalid, "invalid category cat=%d power=%d", cat, reservedPower)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	st, err := s.requireRegion(regionID)
	if err != nil {
		return err
	}
	u, ok := st.region.users[userID]
	if !ok {
		return errf(RejectInvalid, "user %q not found", userID)
	}
	if st.groupSelected(u.groupID) {
		s.log("ChangeCategory user=%s REJECT in-slot group=%d", userID, u.groupID)
		return errf(RejectInSlot, "时段进行中: group %d limited in slot %d", u.groupID, st.curStart)
	}
	u.category = cat
	u.reservedPower = reservedPower
	s.log("ChangeCategory user=%s cat=%d power=%d -> ok", userID, cat, reservedPower)
	return nil
}

func (s *Scheduler) requireRegion(id string) (*regionState, error) {
	st, ok := s.regions[id]
	if !ok {
		return nil, errf(RejectInvalid, "region %q not found", id)
	}
	return st, nil
}

func modNonNeg(a, b int64) int64 {
	m := a % b
	if m < 0 {
		m += b
	}
	return m
}

func removeString(xs []string, x string) []string {
	for i, v := range xs {
		if v == x {
			return append(xs[:i], xs[i+1:]...)
		}
	}
	return xs
}

func (st *regionState) aligned(t int64) bool { return modNonNeg(t, st.region.slotLen) == 0 }

func (st *regionState) groupSelected(g int) bool {
	for _, id := range st.curPick {
		if id == g {
			return true
		}
	}
	return false
}

// --- orders -----------------------------------------------------------------

// Issue adds an order: 0 < level <= group count, [start,end) aligned to slot
// boundaries. start must not precede the next slot start after the slot
// containing now (orders targeting started/ended slots are illegal).
func (s *Scheduler) Issue(regionID string, level int, start, end int64) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, err := s.requireRegion(regionID)
	if err != nil {
		return 0, err
	}
	n := len(st.region.groups)
	if level <= 0 || level > n {
		return 0, errf(RejectInvalid, "level %d out of range [1,%d]", level, n)
	}
	if end <= start || !st.aligned(start) || !st.aligned(end) {
		return 0, errf(RejectInvalid, "window [%d,%d) not aligned to slotLen %d", start, end, st.region.slotLen)
	}
	if start < st.curEnd {
		return 0, errf(RejectInvalid, "order start %d must be >= next slot start %d", start, st.curEnd)
	}
	id := s.nextOrd
	s.nextOrd++
	st.book.add(id, start, end, level)
	if start == st.nextStart {
		st.reconcileNext(s)
	}
	s.log("Issue id=%d region=%s level=%d [%d,%d) -> ok", id, regionID, level, start, end)
	return id, nil
}

// ChangeLevel changes an order level. Inside a slot it applies from the next
// boundary; exactly on a boundary it applies to the slot opening there.
func (s *Scheduler) ChangeLevel(orderID, level int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, o, err := s.findOrder(orderID)
	if err != nil {
		return err
	}
	n := len(st.region.groups)
	if level <= 0 || level > n {
		return errf(RejectInvalid, "level %d out of range [1,%d]", level, n)
	}
	effFrom := s.changeBoundary(st)
	if effFrom < o.start {
		effFrom = o.start
	}
	st.book.changeLevel(o, effFrom, level)
	st.applyBoundaryEdit(s, effFrom)
	s.log("ChangeLevel id=%d level=%d effFrom=%d now=%d -> ok", orderID, level, effFrom, s.now)
	return nil
}

// Cancel cancels an order. Before its window starts it vanishes entirely
// (视同从未存在); otherwise the cancel applies at the boundary in effect.
func (s *Scheduler) Cancel(orderID int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, o, err := s.findOrder(orderID)
	if err != nil {
		return err
	}
	effFrom := s.changeBoundary(st)
	drop := effFrom < o.start
	st.book.cancel(o, effFrom, drop)
	if drop {
		if o.start == st.nextStart {
			st.reconcileNext(s)
		}
		s.log("Cancel id=%d effFrom=%d <= start %d -> removed as never existed", orderID, effFrom, o.start)
		return nil
	}
	st.applyBoundaryEdit(s, effFrom)
	s.log("Cancel id=%d effFrom=%d now=%d -> canceled", orderID, effFrom, s.now)
	return nil
}

func (s *Scheduler) findOrder(id int) (*regionState, *order, error) {
	for _, st := range s.regions {
		if o, ok := st.book.byID[id]; ok {
			return st, o, nil
		}
	}
	return nil, nil, errf(RejectNoOrder, "order %d does not exist or was canceled", id)
}

// changeBoundary returns the effective boundary of an edit at s.now:
// curStart if the clock is exactly on it and the slot is open, else the
// following boundary (an edit strictly inside a slot never changes it).
func (s *Scheduler) changeBoundary(st *regionState) int64 {
	if !st.curFrozen && s.now == st.curStart {
		return st.curStart
	}
	return st.curEnd
}

// applyBoundaryEdit refreshes whichever not-yet-frozen slot the edit reaches:
// the open current slot for an edit effective exactly at curStart, otherwise
// the pre-picked next slot.
func (st *regionState) applyBoundaryEdit(s *Scheduler, effFrom int64) {
	switch {
	case effFrom == st.curStart && !st.curFrozen:
		st.reconcileCurrent(s)
	case effFrom == st.nextStart:
		st.reconcileNext(s)
	}
}

// --- clock ------------------------------------------------------------------

// Advance moves the clock to t (>= now). Every crossed slot boundary of every
// region is settled in chronological order without skipping any.
func (s *Scheduler) Advance(t int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if t < s.now {
		return errf(RejectClockBack, "clock cannot move backwards: %d -> %d", s.now, t)
	}
	if t == s.now {
		return nil
	}
	s.log("Advance %d -> %d: settling boundaries", s.now, t)
	for {
		var st *regionState
		nextB := int64(1<<62 - 1)
		for _, r := range s.regions {
			b := r.curEnd
			if b <= t && b < nextB {
				nextB, st = b, r
			}
		}
		if st == nil {
			break
		}
		st.arriveBoundary(s, nextB, nextB < t)
	}
	s.now = t
	s.log("Advance -> %d done", t)
	return nil
}

// arriveBoundary settles one region at boundary b. freeze is true when the
// clock moves strictly beyond b (then the new slot is immediately immutable);
// when the clock stops exactly at b it remains open for boundary edits.
func (st *regionState) arriveBoundary(s *Scheduler, b int64, freeze bool) {
	slotLen := st.region.slotLen

	// If the clock had stopped exactly at the previous boundary and now moves
	// beyond it, any boundary edit was already reconciled; assess then freeze.
	if !st.curAssessed {
		st.assessCurrent(s)
		st.curAssessed = true
	}

	// 1) accrue the ended slot [b-L,b) and freeze its decision.
	for _, gid := range st.curPick {
		st.region.groups[gid].accrued += slotLen
	}
	s.decisions[st.region.ID][st.curStart] = append([]int(nil), st.curPick...)

	// 2) promote the pre-picked slot [b,b+L), pruning dead orders first,
	//    then assess notices that were not confirmed before b. Reconciliation
	//    covers level edits issued exactly at b (the slot is still open).
	st.book.prune(b)
	st.curStart, st.curEnd = b, b+slotLen
	// Recompute from real accrued state after settlement; the pre-picked
	// notices created during the confirmation window are reconciled against
	// this fresh selection (withdrawn where the level moved).
	fresh := pickGroups(st, st.book.effectiveLevel(b))
	st.curNotices = st.nextNotices
	st.reconcileNoticeSet(s, st.nextPick, fresh, st.curNotices, b)
	st.curPick = fresh
	st.reconcileCurrent(s)
	// Outstanding notices are assessed only when the slot becomes immutable
	// (clock strictly beyond its start). When the clock stops at the boundary
	// an edit there may still withdraw notices before assessment happens.
	if freeze {
		st.assessCurrent(s)
	}
	st.curAssessed = freeze

	// 3) pre-pick the following slot and open its confirmation window.
	st.nextStart = st.curEnd
	st.nextPick = pickProjected(st, st.book.effectiveLevel(st.nextStart))
	st.nextNotices = map[string]*slotNotice{}
	st.generateNotices(st.nextPick, st.nextNotices)

	st.curFrozen = freeze
	s.log("boundary=%d region=%s: accrued ended slot, opened current=%v(level=%d,frozen=%v) prepicked=%v(level=%d)",
		b, st.region.ID, st.curPick, st.book.effectiveLevel(b), freeze, st.nextPick,
		st.book.effectiveLevel(st.nextStart))
}

// reconcileNoticeSet aligns an existing notice set with a freshly computed
// selection: notices of groups absent from the selection are withdrawn, and
// missing ones are generated. Confirmed/withdrawn handling lives in notify.go.
func (st *regionState) reconcileNoticeSet(s *Scheduler, prePick, want []int, notices map[string]*slotNotice, slot int64) {
	wantSet := map[int]bool{}
	for _, g := range want {
		wantSet[g] = true
	}
	var dropped []int
	for _, g := range prePick {
		if !wantSet[g] {
			dropped = append(dropped, g)
		}
	}
	preSet := map[int]bool{}
	for _, g := range prePick {
		preSet[g] = true
	}
	var added []int
	for _, g := range want {
		if !preSet[g] {
			added = append(added, g)
		}
	}
	st.withdrawNotices(notices, dropped)
	st.generateNotices(added, notices)
	if len(dropped) > 0 || len(added) > 0 {
		s.log("promote slot=%d dropped=%v added=%v", slot, dropped, added)
	}
}

// --- selection and reconciliation ------------------------------------------

// pickGroups chooses level groups by minimum accrued time, ties by group id.
// Cost is O(G log G) with G the number of groups; users are never scanned.
func pickGroups(st *regionState, level int) []int {
	if level <= 0 {
		return nil
	}
	ids := append([]int(nil), st.region.groupIDs...)
	sortRanks(st, ids)
	if level > len(ids) {
		level = len(ids)
	}
	return ids[:level]
}

// pickProjected ranks groups by the cumulative time they will have after the
// current slot accrues (its limited groups effectively +slotLen). A pre-pick
// computed before a boundary is then identical to a fresh pick afterwards.
func pickProjected(st *regionState, level int) []int {
	if level <= 0 {
		return nil
	}
	current := map[int]bool{}
	for _, g := range st.curPick {
		current[g] = true
	}
	ids := append([]int(nil), st.region.groupIDs...)
	slotLen := st.region.slotLen
	sort.Slice(ids, func(i, j int) bool {
		gi, gj := st.region.groups[ids[i]], st.region.groups[ids[j]]
		ai, aj := gi.accrued, gj.accrued
		if current[gi.ID] {
			ai += slotLen
		}
		if current[gj.ID] {
			aj += slotLen
		}
		if ai != aj {
			return ai < aj
		}
		return gi.ID < gj.ID
	})
	if level > len(ids) {
		level = len(ids)
	}
	return ids[:level]
}

func sortRanks(st *regionState, ids []int) {
	sort.Slice(ids, func(i, j int) bool {
		gi, gj := st.region.groups[ids[i]], st.region.groups[ids[j]]
		if gi.accrued != gj.accrued {
			return gi.accrued < gj.accrued
		}
		return gi.ID < gj.ID
	})
}

// reconcileCurrent adjusts the open current slot to the effective level at
// curStart. Retained groups are the highest ranked of the current pick;
// removed groups withdraw notices (no assessment), added groups receive
// notices that are immediately past deadline when the edit is at the start.
func (st *regionState) reconcileCurrent(s *Scheduler) {
	want := pickGroups(st, st.book.effectiveLevel(st.curStart))
	kept, dropped, added := diffSelection(st, st.curPick, want)
	st.withdrawNotices(st.curNotices, dropped)
	st.generateNotices(added, st.curNotices)
	st.curPick = mergeRanked(st, kept, added)
	if len(dropped) > 0 || len(added) > 0 {
		s.log("reconcileCurrent slot=%d kept=%v dropped=%v added=%v -> %v",
			st.curStart, kept, dropped, added, st.curPick)
	}
}

// reconcileNext adjusts the pre-picked notifiable slot; the same ranking rule.
func (st *regionState) reconcileNext(s *Scheduler) {
	want := pickProjected(st, st.book.effectiveLevel(st.nextStart))
	kept, dropped, added := diffSelectionProjected(st, st.nextPick, want)
	st.withdrawNotices(st.nextNotices, dropped)
	st.generateNotices(added, st.nextNotices)
	st.nextPick = mergeProjected(st, kept, added)
	s.log("reconcileNext slot=%d level=%d kept=%v dropped=%v added=%v -> %v",
		st.nextStart, st.book.effectiveLevel(st.nextStart), kept, dropped, added, st.nextPick)
}

// diffSelection returns kept/dropped/added groups when moving from cur to
// want. Kept groups are the highest-ranked members of cur that remain wanted,
// which keeps the result a pure function of (accrued,id).
func diffSelection(st *regionState, cur, want []int) (kept, dropped, added []int) {
	wantSet := map[int]bool{}
	for _, g := range want {
		wantSet[g] = true
	}
	rankedCur := append([]int(nil), cur...)
	sortRanks(st, rankedCur)
	for _, g := range rankedCur {
		if wantSet[g] {
			kept = append(kept, g)
		} else {
			dropped = append(dropped, g)
		}
	}
	keptSet := map[int]bool{}
	for _, g := range kept {
		keptSet[g] = true
	}
	for _, g := range want {
		if !keptSet[g] {
			added = append(added, g)
		}
	}
	return kept, dropped, added
}

func mergeRanked(st *regionState, kept, added []int) []int {
	out := append(append([]int(nil), kept...), added...)
	sortRanks(st, out)
	return out
}

// diffSelectionProjected is diffSelection using the post-accrual projected
// rank, which is the rank on which next-slot pre-picks must agree.
func diffSelectionProjected(st *regionState, cur, want []int) (kept, dropped, added []int) {
	wantSet := map[int]bool{}
	for _, g := range want {
		wantSet[g] = true
	}
	rankedCur := append([]int(nil), cur...)
	current := map[int]bool{}
	for _, g := range st.curPick {
		current[g] = true
	}
	slotLen := st.region.slotLen
	rank := func(id int) (int64, int) {
		a := st.region.groups[id].accrued
		if current[id] {
			a += slotLen
		}
		return a, id
	}
	sort.Slice(rankedCur, func(i, j int) bool {
		ai, ii := rank(rankedCur[i])
		aj, jj := rank(rankedCur[j])
		if ai != aj {
			return ai < aj
		}
		return ii < jj
	})
	for _, g := range rankedCur {
		if wantSet[g] {
			kept = append(kept, g)
		} else {
			dropped = append(dropped, g)
		}
	}
	keptSet := map[int]bool{}
	for _, g := range kept {
		keptSet[g] = true
	}
	for _, g := range want {
		if !keptSet[g] {
			added = append(added, g)
		}
	}
	return kept, dropped, added
}

func mergeProjected(st *regionState, kept, added []int) []int {
	out := append(append([]int(nil), kept...), added...)
	current := map[int]bool{}
	for _, g := range st.curPick {
		current[g] = true
	}
	slotLen := st.region.slotLen
	sort.Slice(out, func(i, j int) bool {
		gi, gj := st.region.groups[out[i]], st.region.groups[out[j]]
		ai, aj := gi.accrued, gj.accrued
		if current[gi.ID] {
			ai += slotLen
		}
		if current[gj.ID] {
			aj += slotLen
		}
		if ai != aj {
			return ai < aj
		}
		return gi.ID < gj.ID
	})
	return out
}

// Package trade implements the trade-session state machine on top of
// inventory and escrow.
//
// Expiry is a view: a session whose deadline has been reached at the
// operation's now is treated as cancelled for that operation's judgments,
// but locks are only materialized (released in escrow) once an accepted
// operation commits the clock past the deadline. Rejected operations
// therefore never change observable state nor advance the clock.
package trade

import (
	"errors"
	"sync"

	"ontology/escrow"
	"ontology/inventory"
)

var (
	ErrParam            = errors.New("trade: invalid parameter")
	ErrClock            = errors.New("trade: clock rollback")
	ErrNoPlayer         = errors.New("trade: player not registered")
	ErrSessionLimit     = errors.New("trade: too many open sessions")
	ErrNoSession        = errors.New("trade: session not found")
	ErrNotMember        = errors.New("trade: not a session member")
	ErrInsufficient     = errors.New("trade: insufficient available balance")
	ErrStale            = errors.New("trade: stale version")
	ErrAlreadyConfirmed = errors.New("trade: already confirmed")
	ErrEmptyTrade       = errors.New("trade: empty trade")
	ErrGoldCap          = errors.New("trade: gold cap exceeded")
	ErrSlots            = errors.New("trade: slot limit exceeded")
)

// maxClock is the largest legal value of now (10^12 ms).
const maxClock = int64(1_000_000_000_000)

type offer struct {
	items map[string]int64
	gold  int64
}

func (o offer) empty() bool { return o.gold == 0 && len(o.items) == 0 }

type session struct {
	a, b      string
	offers    [2]offer // index 0: a, index 1: b
	ver       int64
	confirmed [2]bool
	expiresAt int64
}

func (s *session) side(who string) int {
	if who == s.a {
		return 0
	}
	if who == s.b {
		return 1
	}
	return -1
}

// validAt reports whether the session is still open at the given time;
// the deadline is inclusive (now >= expiresAt cancels).
func (s *session) validAt(now int64) bool { return s.expiresAt > now }

// System is the escrowed trading service. All methods are safe for
// concurrent use and behave as some serial order.
type System struct {
	mu       sync.Mutex
	inv      *inventory.Inventory
	esc      *escrow.Escrow
	r        int64 // tax rate in per-mille
	ttl      int64 // session lifetime E
	maxOpen  int64 // per-player open-session limit Q
	maxNow   int64 // largest accepted now; -1 before any accepted op
	nextSID  int64
	sessions map[int64]*session
	byPlayer map[string]map[int64]struct{}
}

// New validates the configuration and creates a System.
func New(r, goldCap, slots, ttl, maxOpen int64) (*System, error) {
	if r < 0 || r > 1000 ||
		goldCap < 1 || goldCap > 1_000_000_000_000_000 ||
		slots < 1 || slots > 10_000 ||
		ttl < 1 || ttl > 1_000_000_000 ||
		maxOpen < 1 || maxOpen > 10_000 {
		return nil, ErrParam
	}
	return &System{
		inv:      inventory.New(goldCap, slots),
		esc:      escrow.New(),
		r:        r,
		ttl:      ttl,
		maxOpen:  maxOpen,
		maxNow:   -1,
		sessions: map[int64]*session{},
		byPlayer: map[string]map[int64]struct{}{},
	}, nil
}

// Grant delegates to the inventory.
func (s *System) Grant(player, item string, qty int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.inv.Grant(player, item, qty)
}

// GrantGold delegates to the inventory.
func (s *System) GrantGold(player string, g int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.inv.GrantGold(player, g)
}

// Open starts a session between a and b, returning its id (from 1,
// increasing; rejected opens do not consume an id).
// Rejection order: invalid params > clock rollback > unknown player >
// a at session limit > b at session limit.
func (s *System) Open(now int64, a, b string) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if a == "" || b == "" || a == b {
		return 0, ErrParam
	}
	if err := s.checkClock(now); err != nil {
		return 0, err
	}
	if !s.inv.Has(a) || !s.inv.Has(b) {
		return 0, ErrNoPlayer
	}
	if s.validSessions(a, now) >= s.maxOpen {
		return 0, ErrSessionLimit
	}
	if s.validSessions(b, now) >= s.maxOpen {
		return 0, ErrSessionLimit
	}
	s.maxNow = now
	s.materialize(a, now)
	s.materialize(b, now)
	s.nextSID++
	sid := s.nextSID
	s.sessions[sid] = &session{a: a, b: b, expiresAt: now + s.ttl}
	s.addSession(a, sid)
	s.addSession(b, sid)
	return sid, nil
}

// Offer replaces the caller's quote in session sid: the session's locks
// become the new quote, ver increments, both confirmations are cleared
// and the deadline refreshes to now+E (even for an identical quote).
// Rejection order: invalid params > clock rollback > no such session
// (expired or finished) > not a member > insufficient available balance.
func (s *System) Offer(now, sid int64, who string, items map[string]int64, gold int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := validateOffer(sid, who, items, gold, s.inv.GoldCap()); err != nil {
		return err
	}
	if err := s.checkClock(now); err != nil {
		return err
	}
	sess := s.sessions[sid]
	if sess == nil || !sess.validAt(now) {
		return ErrNoSession
	}
	side := sess.side(who)
	if side < 0 {
		return ErrNotMember
	}
	if !s.canCover(who, sid, sess.offers[side], items, gold, now) {
		return ErrInsufficient
	}
	s.maxNow = now
	s.materialize(who, now)
	locked := cloneItems(items)
	s.esc.Replace(sid, who, locked, gold)
	sess.offers[side] = offer{items: locked, gold: gold}
	sess.ver++
	sess.confirmed = [2]bool{}
	sess.expiresAt = now + s.ttl
	return nil
}

// Confirm confirms the given version. When the other side has already
// confirmed, this confirm settles the trade atomically; the cap checks
// run only then, and on failure the confirm is not recorded. Confirm
// never refreshes the deadline.
// Rejection order: invalid params > clock rollback > no such session >
// not a member > stale version > already confirmed > empty trade >
// gold cap (a then b) > slot limit (a then b).
func (s *System) Confirm(now, sid int64, who string, ver int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if sid < 1 || who == "" || ver < 0 {
		return ErrParam
	}
	if err := s.checkClock(now); err != nil {
		return err
	}
	sess := s.sessions[sid]
	if sess == nil || !sess.validAt(now) {
		return ErrNoSession
	}
	side := sess.side(who)
	if side < 0 {
		return ErrNotMember
	}
	if ver != sess.ver {
		return ErrStale
	}
	if sess.confirmed[side] {
		return ErrAlreadyConfirmed
	}
	if sess.offers[0].empty() && sess.offers[1].empty() {
		return ErrEmptyTrade
	}
	if !sess.confirmed[1-side] {
		s.maxNow = now
		s.materialize(sess.a, now)
		s.materialize(sess.b, now)
		sess.confirmed[side] = true
		return nil
	}
	offA, offB := sess.offers[0], sess.offers[1]
	taxA := tax(offA.gold, s.r) // gold a->b, borne by b
	taxB := tax(offB.gold, s.r) // gold b->a, borne by a
	if s.inv.Gold(sess.a)-offA.gold+offB.gold-taxB > s.inv.GoldCap() {
		return ErrGoldCap
	}
	if s.inv.Gold(sess.b)-offB.gold+offA.gold-taxA > s.inv.GoldCap() {
		return ErrGoldCap
	}
	if kindsAfter(s.inv, sess.a, offA.items, offB.items) > s.inv.Slots() {
		return ErrSlots
	}
	if kindsAfter(s.inv, sess.b, offB.items, offA.items) > s.inv.Slots() {
		return ErrSlots
	}
	s.maxNow = now
	s.materialize(sess.a, now)
	s.materialize(sess.b, now)
	for item, q := range offA.items {
		s.inv.AddItem(sess.a, item, -q)
		s.inv.AddItem(sess.b, item, q)
	}
	for item, q := range offB.items {
		s.inv.AddItem(sess.b, item, -q)
		s.inv.AddItem(sess.a, item, q)
	}
	s.inv.AddGold(sess.a, -offA.gold+offB.gold-taxB)
	s.inv.AddGold(sess.b, -offB.gold+offA.gold-taxA)
	s.inv.Burn(taxA + taxB)
	s.endSession(sid)
	return nil
}

// Cancel cancels the session; either member may cancel.
// Rejection order: invalid params > clock rollback > no such session >
// not a member.
func (s *System) Cancel(now, sid int64, who string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if sid < 1 || who == "" {
		return ErrParam
	}
	if err := s.checkClock(now); err != nil {
		return err
	}
	sess := s.sessions[sid]
	if sess == nil || !sess.validAt(now) {
		return ErrNoSession
	}
	if sess.side(who) < 0 {
		return ErrNotMember
	}
	s.maxNow = now
	s.materialize(sess.a, now)
	s.materialize(sess.b, now)
	s.endSession(sid)
	return nil
}

// Gold returns the player's gold balance.
func (s *System) Gold(player string) int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.inv.Gold(player)
}

// Qty returns the player's held quantity of item.
func (s *System) Qty(player, item string) int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.inv.Qty(player, item)
}

// Kinds returns the number of item kinds the player holds.
func (s *System) Kinds(player string) int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.inv.Kinds(player)
}

// Burned returns the total burned tax gold.
func (s *System) Burned() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.inv.Burned()
}

// LockedQty returns the player's locked quantity of item across sessions
// still valid at the committed clock.
func (s *System) LockedQty(player, item string) int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.materialize(player, s.maxNow)
	return s.esc.LockedQty(player, item)
}

// LockedGold returns the player's locked gold across sessions still
// valid at the committed clock.
func (s *System) LockedGold(player string) int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.materialize(player, s.maxNow)
	return s.esc.LockedGold(player)
}

// OpenSessions returns the number of sessions the player still has open
// at the committed clock.
func (s *System) OpenSessions(player string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.materialize(player, s.maxNow)
	return len(s.byPlayer[player])
}

// Version returns the session's current version, if it is still open at
// the committed clock.
func (s *System) Version(sid int64) (int64, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if sess := s.sessions[sid]; sess != nil && sess.validAt(s.maxNow) {
		return sess.ver, true
	}
	return 0, false
}

// Confirmed reports whether the given side has confirmed the current
// version of the session.
func (s *System) Confirmed(sid int64, who string) (bool, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess := s.sessions[sid]
	if sess == nil || !sess.validAt(s.maxNow) {
		return false, false
	}
	side := sess.side(who)
	if side < 0 {
		return false, false
	}
	return sess.confirmed[side], true
}

// checkClock validates the now range and rejects clock rollbacks.
func (s *System) checkClock(now int64) error {
	if now < 0 || now > maxClock {
		return ErrParam
	}
	if now < s.maxNow {
		return ErrClock
	}
	return nil
}

func (s *System) addSession(player string, sid int64) {
	set, ok := s.byPlayer[player]
	if !ok {
		set = map[int64]struct{}{}
		s.byPlayer[player] = set
	}
	set[sid] = struct{}{}
}

// validSessions counts the player's sessions still open at now (view;
// no state is mutated).
func (s *System) validSessions(player string, now int64) int64 {
	var n int64
	for sid := range s.byPlayer[player] {
		if sess := s.sessions[sid]; sess != nil && sess.validAt(now) {
			n++
		}
	}
	return n
}

// materialize releases all of the player's sessions whose deadline has
// been reached at now. It is only called once now is committed (an
// accepted operation or the committed clock), so the release is already
// logically true and unobservable.
func (s *System) materialize(player string, now int64) {
	for sid := range s.byPlayer[player] {
		if sess := s.sessions[sid]; sess != nil && !sess.validAt(now) {
			s.endSession(sid)
		}
	}
}

// endSession releases the session's locks and removes it from the indexes.
func (s *System) endSession(sid int64) {
	sess, ok := s.sessions[sid]
	if !ok {
		return
	}
	s.esc.Release(sid)
	delete(s.sessions, sid)
	for _, p := range [2]string{sess.a, sess.b} {
		if set, ok := s.byPlayer[p]; ok {
			delete(set, sid)
			if len(set) == 0 {
				delete(s.byPlayer, p)
			}
		}
	}
}

// canCover reports whether the new quote fits the player's available
// balance: holdings minus locks held in the player's other sessions
// that are still valid at now. Pure view; mutates nothing.
func (s *System) canCover(who string, sid int64, old offer, items map[string]int64, gold, now int64) bool {
	expiredItems := map[string]int64{}
	var expiredGold int64
	for otherSID := range s.byPlayer[who] {
		if otherSID == sid {
			continue
		}
		other := s.sessions[otherSID]
		if other == nil || other.validAt(now) {
			continue
		}
		o := other.offers[other.side(who)]
		for item, q := range o.items {
			expiredItems[item] += q
		}
		expiredGold += o.gold
	}
	for item, q := range items {
		lockedElsewhere := s.esc.LockedQty(who, item) - old.items[item] - expiredItems[item]
		if q > s.inv.Qty(who, item)-lockedElsewhere {
			return false
		}
	}
	goldLockedElsewhere := s.esc.LockedGold(who) - old.gold - expiredGold
	return gold <= s.inv.Gold(who)-goldLockedElsewhere
}

func validateOffer(sid int64, who string, items map[string]int64, gold, goldCap int64) error {
	if sid < 1 || who == "" {
		return ErrParam
	}
	if len(items) > 16 {
		return ErrParam
	}
	for item, q := range items {
		if item == "" || q < 1 || q > 1_000_000_000 {
			return ErrParam
		}
	}
	if gold < 0 || gold > goldCap {
		return ErrParam
	}
	return nil
}

// tax returns ceil(g*r/1000), the tax on gold g borne by the receiver.
func tax(g, r int64) int64 {
	return (g*r + 999) / 1000
}

// kindsAfter computes the player's item-kind count after paying out and
// receiving in, in that order.
func kindsAfter(inv *inventory.Inventory, player string, out, in map[string]int64) int64 {
	kinds := inv.Kinds(player)
	for item, q := range out {
		pre := inv.Qty(player, item)
		post := pre - q + in[item]
		if pre > 0 && post == 0 {
			kinds--
		}
	}
	for item, q := range in {
		if _, paid := out[item]; paid {
			continue // already accounted above
		}
		if inv.Qty(player, item) == 0 && q > 0 {
			kinds++
		}
	}
	return kinds
}

func cloneItems(items map[string]int64) map[string]int64 {
	if len(items) == 0 {
		return nil
	}
	out := make(map[string]int64, len(items))
	for item, q := range items {
		out[item] = q
	}
	return out
}

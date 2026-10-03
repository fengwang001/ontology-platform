// Package prefs implements a layered subscription preference conflict
// resolver across platform, organization and user rule layers.
package prefs

import (
	"errors"
	"strings"
	"sync"
)

// Layer identifiers.
const (
	LayerPlatform = 1
	LayerOrg      = 2
	LayerUser     = 3
)

// Channels.
const (
	ChAny   = "*"
	ChEmail = "email"
	ChSMS   = "sms"
	ChPush  = "push"
)

// Effects.
const (
	EffAllow = "allow"
	EffDeny  = "deny"
)

// maxRules bounds the total number of stored rules.
const maxRules = 1000

// Rejection reasons, reported in a fixed priority order per operation.
var (
	ErrInvalidParam     = errors.New("prefs: invalid parameter")
	ErrClockRegression  = errors.New("prefs: clock regression")
	ErrPermissionDenied = errors.New("prefs: permission denied")
	ErrLockedOverride   = errors.New("prefs: overridden by locked rule")
	ErrCapacity         = errors.New("prefs: rule capacity exceeded")
	ErrNotFound         = errors.New("prefs: rule not found")
)

// Rule is a single preference rule keyed by (Layer, Cat, Ch).
type Rule struct {
	Layer  int
	Cat    string
	Ch     string
	Eff    string
	Locked bool
	Exp    int64
	Ts     int64
	Seq    int64
}

// key identifies a rule slot.
type key struct {
	layer int
	cat   string
	ch    string
}

// Decision is the outcome of Resolve. When no candidate rule exists,
// Allowed is false and the source fields are zero.
type Decision struct {
	Allowed bool
	Layer   int
	Cat     string
	Ch      string
	Seq     int64
}

// Store holds all rules for one user and their organization.
// All methods are safe for concurrent use.
type Store struct {
	mu    sync.RWMutex
	rules map[key]Rule
	maxTs int64
	tomb  int64
	seq   int64
}

// NewStore returns an empty Store.
func NewStore() *Store {
	return &Store{
		rules: make(map[key]Rule),
		tomb:  -1,
	}
}

// Set inserts or replaces the rule at (Layer, Cat, Ch). Rejections are
// checked in order: invalid parameter, clock regression, permission,
// locked override, capacity. A rejected Set changes nothing.
func (s *Store) Set(r Rule) error {
	if !validLayer(r.Layer) || !validCat(r.Cat) || !validRuleCh(r.Ch) ||
		!validEff(r.Eff) || r.Ts < 0 || (r.Exp != 0 && r.Exp <= r.Ts) {
		return ErrInvalidParam
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if r.Ts < s.maxTs {
		return ErrClockRegression
	}
	if r.Layer == LayerUser && r.Locked {
		return ErrPermissionDenied
	}
	if r.Layer == LayerUser && s.lockedCovered(r.Cat, r.Ch, r.Ts) {
		return ErrLockedOverride
	}
	k := key{layer: r.Layer, cat: r.Cat, ch: r.Ch}
	if _, ok := s.rules[k]; !ok && len(s.rules) >= maxRules {
		return ErrCapacity
	}
	s.seq++
	r.Seq = s.seq
	s.rules[k] = r
	s.maxTs = r.Ts
	return nil
}

// Remove deletes the rule at (layer, cat, ch). Rejections are checked in
// order: invalid parameter, clock regression, locked override (user layer
// only), rule not found. A rejected Remove changes nothing.
func (s *Store) Remove(layer int, cat, ch string, ts int64) error {
	if !validLayer(layer) || !validCat(cat) || !validRuleCh(ch) || ts < 0 {
		return ErrInvalidParam
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if ts < s.maxTs {
		return ErrClockRegression
	}
	if layer == LayerUser && s.lockedCovered(cat, ch, ts) {
		return ErrLockedOverride
	}
	k := key{layer: layer, cat: cat, ch: ch}
	if _, ok := s.rules[k]; !ok {
		return ErrNotFound
	}
	delete(s.rules, k)
	s.maxTs = ts
	return nil
}

// UnsubscribeAll invalidates all user-layer rules with Ts <= ts without
// deleting them. Later user-layer Sets (with greater ts) take effect again.
func (s *Store) UnsubscribeAll(ts int64) error {
	if ts < 0 {
		return ErrInvalidParam
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if ts < s.maxTs {
		return ErrClockRegression
	}
	s.tomb = ts
	s.maxTs = ts
	return nil
}

// Resolve decides whether a notification of category cat may be sent on
// channel ch at time now. It is read-only and does not advance the clock.
func (s *Store) Resolve(cat, ch string, now int64) (Decision, error) {
	if cat == "" || !validCat(cat) || !validConcreteCh(ch) || now < 0 {
		return Decision{}, ErrInvalidParam
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if now < s.maxTs {
		return Decision{}, ErrClockRegression
	}
	return s.resolve(cat, ch, now), nil
}

// resolve computes the decision under a held lock.
func (s *Store) resolve(cat, ch string, now int64) Decision {
	var best *Rule
	lockedMode := false
	for _, r := range s.rules {
		if !s.effective(r, now) || !catPrefix(r.Cat, cat) ||
			(r.Ch != ChAny && r.Ch != ch) {
			continue
		}
		cand := r
		switch {
		case best == nil:
			best = &cand
			lockedMode = cand.Locked
		case lockedMode && !cand.Locked:
			// Keep the locked incumbent.
		case !lockedMode && cand.Locked:
			best = &cand
			lockedMode = true
		case lockedMode && lockedBetter(cand, *best):
			best = &cand
		case !lockedMode && plainBetter(cand, *best):
			best = &cand
		}
	}
	if best == nil {
		return Decision{Allowed: false}
	}
	return Decision{
		Allowed: best.Eff == EffAllow,
		Layer:   best.Layer,
		Cat:     best.Cat,
		Ch:      best.Ch,
		Seq:     best.Seq,
	}
}

// effective reports whether r is in force at time now.
func (s *Store) effective(r Rule, now int64) bool {
	if r.Exp != 0 && now >= r.Exp {
		return false
	}
	if r.Layer == LayerUser && r.Ts <= s.tomb {
		return false
	}
	return true
}

// lockedCovered reports whether a user-layer rule with the given cat and ch
// would be overridden at time ts by an effective locked rule whose cat is a
// segment prefix of (or equal to) cat and whose ch is * or equal to ch.
func (s *Store) lockedCovered(cat, ch string, ts int64) bool {
	for _, r := range s.rules {
		if !r.Locked || !s.effective(r, ts) {
			continue
		}
		if catPrefix(r.Cat, cat) && (r.Ch == ChAny || r.Ch == ch) {
			return true
		}
	}
	return false
}

// lockedBetter compares two locked candidates: smaller layer wins, then
// more category segments, then exact channel match.
func lockedBetter(a, b Rule) bool {
	if a.Layer != b.Layer {
		return a.Layer < b.Layer
	}
	if sa, sb := catSegs(a.Cat), catSegs(b.Cat); sa != sb {
		return sa > sb
	}
	return a.Ch != ChAny && b.Ch == ChAny
}

// plainBetter compares two unlocked candidates: more category segments
// wins, then exact channel match, then larger layer.
func plainBetter(a, b Rule) bool {
	if sa, sb := catSegs(a.Cat), catSegs(b.Cat); sa != sb {
		return sa > sb
	}
	if ea, eb := a.Ch != ChAny, b.Ch != ChAny; ea != eb {
		return ea
	}
	return a.Layer > b.Layer
}

// catSegs returns the number of category segments; the root has 0.
func catSegs(cat string) int {
	if cat == "" {
		return 0
	}
	return strings.Count(cat, "/") + 1
}

// catPrefix reports whether ruleCat is a segment-wise prefix of queryCat.
// The empty root matches every category.
func catPrefix(ruleCat, queryCat string) bool {
	if ruleCat == "" {
		return true
	}
	rsegs := strings.Split(ruleCat, "/")
	qsegs := strings.Split(queryCat, "/")
	if len(rsegs) > len(qsegs) {
		return false
	}
	for i, seg := range rsegs {
		if seg != qsegs[i] {
			return false
		}
	}
	return true
}

func validLayer(layer int) bool {
	return layer == LayerPlatform || layer == LayerOrg || layer == LayerUser
}

func validEff(eff string) bool {
	return eff == EffAllow || eff == EffDeny
}

func validRuleCh(ch string) bool {
	return ch == ChAny || validConcreteCh(ch)
}

func validConcreteCh(ch string) bool {
	return ch == ChEmail || ch == ChSMS || ch == ChPush
}

// validCat validates rule/query category syntax: the empty root, or 1-4
// segments of 1-16 lowercase letters, digits or underscores.
func validCat(cat string) bool {
	if cat == "" {
		return true
	}
	segs := strings.Split(cat, "/")
	if len(segs) > 4 {
		return false
	}
	for _, seg := range segs {
		if len(seg) == 0 || len(seg) > 16 {
			return false
		}
		for _, c := range seg {
			if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '_') {
				return false
			}
		}
	}
	return true
}

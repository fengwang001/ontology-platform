// Package pref implements a layered subscription-preference conflict
// resolver. Rules are registered at one of three layers (platform,
// organization, user) and are arbitrated per (category, channel) query
// by expiry, unsubscribe-all tombstone, lock flags and specificity.
package pref

import (
	"errors"
	"regexp"
	"strings"
	"sync"
)

// Layers.
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
	Allow = "allow"
	Deny  = "deny"
)

// MaxRules bounds the number of stored rule keys.
const MaxRules = 1000

// Rejection reasons. Operations report only the first matching reason,
// in the order listed per operation in the package documentation.
var (
	ErrInvalidArgument  = errors.New("pref: invalid argument")
	ErrClockRegression  = errors.New("pref: clock regression")
	ErrPermissionDenied = errors.New("pref: insufficient permission")
	ErrLockedOverride   = errors.New("pref: covered by a locked rule")
	ErrCapacityExceeded = errors.New("pref: rule capacity exceeded")
	ErrRuleNotFound     = errors.New("pref: rule not found")
)

// Rule is a single preference rule. Seq is assigned by the Store on Set.
type Rule struct {
	Layer  int
	Cat    string
	Ch     string
	Eff    string
	Locked bool
	Exp    int64
	Ts     int64
	Seq    uint64
}

// Decision is the outcome of Resolve. When no candidate rule exists,
// Allow is false and the source fields (Layer/Cat/Ch/Seq) are zero.
type Decision struct {
	Allow bool
	Layer int
	Cat   string
	Ch    string
	Seq   uint64
}

type ruleKey struct {
	layer int
	cat   string
	ch    string
}

// Store holds the rules of one user and their organization. All methods
// are safe for concurrent use; results are equivalent to some serial
// order. Resolve is read-only.
type Store struct {
	mu    sync.RWMutex
	rules map[ruleKey]Rule
	tomb  int64 // last UnsubscribeAll ts, -1 initially
	maxTs int64 // max accepted mutation ts
	seq   uint64
}

// NewStore returns an empty Store.
func NewStore() *Store {
	return &Store{rules: make(map[ruleKey]Rule), tomb: -1}
}

var segmentRe = regexp.MustCompile(`^[a-z0-9_]{1,16}$`)

// validCat reports whether cat is the root ("") or 1-4 segments of
// 1-16 lowercase letters, digits or underscores each.
func validCat(cat string) bool {
	if cat == "" {
		return true
	}
	segs := strings.Split(cat, "/")
	if len(segs) > 4 {
		return false
	}
	for _, s := range segs {
		if !segmentRe.MatchString(s) {
			return false
		}
	}
	return true
}

func validLayer(layer int) bool {
	return layer == LayerPlatform || layer == LayerOrg || layer == LayerUser
}

func validCh(ch string) bool {
	switch ch {
	case ChAny, ChEmail, ChSMS, ChPush:
		return true
	}
	return false
}

func concreteCh(ch string) bool {
	return ch == ChEmail || ch == ChSMS || ch == ChPush
}

func validEff(eff string) bool {
	return eff == Allow || eff == Deny
}

func catSegs(cat string) []string {
	if cat == "" {
		return nil
	}
	return strings.Split(cat, "/")
}

// segPrefix reports whether ruleCat is a segment-wise prefix of queryCat
// (including equality and the root).
func segPrefix(ruleCat, queryCat string) bool {
	r, q := catSegs(ruleCat), catSegs(queryCat)
	if len(r) > len(q) {
		return false
	}
	for i := range r {
		if r[i] != q[i] {
			return false
		}
	}
	return true
}

// liveAt reports whether r is in effect at time t. The unsubscribe-all
// tombstone only suppresses user-layer rules with Ts <= tomb.
func (s *Store) liveAt(r Rule, t int64) bool {
	if r.Exp != 0 && t >= r.Exp {
		return false
	}
	if r.Layer == LayerUser && r.Ts <= s.tomb {
		return false
	}
	return true
}

// lockedCover reports whether a locked rule effective at ts covers
// (cat, ch): its cat is a segment prefix of cat and its ch is "*" or
// equal to ch. Locked rules only exist at platform/org layers, so the
// tombstone never suppresses them.
func (s *Store) lockedCover(cat, ch string, ts int64) bool {
	for _, r := range s.rules {
		if !r.Locked {
			continue
		}
		if r.Exp != 0 && ts >= r.Exp {
			continue
		}
		if segPrefix(r.Cat, cat) && (r.Ch == ChAny || r.Ch == ch) {
			return true
		}
	}
	return false
}

// Set registers or replaces the rule keyed by (layer, cat, ch).
// Rejection order: invalid argument, clock regression, permission
// denied, locked override, capacity exceeded.
func (s *Store) Set(layer int, cat, ch, eff string, locked bool, exp, ts int64) error {
	if !validLayer(layer) || !validCat(cat) || !validCh(ch) || !validEff(eff) ||
		(exp != 0 && exp <= ts) || ts < 0 {
		return ErrInvalidArgument
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if ts < s.maxTs {
		return ErrClockRegression
	}
	if layer == LayerUser && locked {
		return ErrPermissionDenied
	}
	if layer == LayerUser && s.lockedCover(cat, ch, ts) {
		return ErrLockedOverride
	}
	k := ruleKey{layer, cat, ch}
	if _, ok := s.rules[k]; !ok && len(s.rules) >= MaxRules {
		return ErrCapacityExceeded
	}
	s.seq++
	s.rules[k] = Rule{Layer: layer, Cat: cat, Ch: ch, Eff: eff, Locked: locked, Exp: exp, Ts: ts, Seq: s.seq}
	s.maxTs = ts
	return nil
}

// Remove deletes the rule keyed by (layer, cat, ch). Rejection order:
// invalid argument, clock regression, locked override (user layer
// only), rule not found.
func (s *Store) Remove(layer int, cat, ch string, ts int64) error {
	if !validLayer(layer) || !validCat(cat) || !validCh(ch) || ts < 0 {
		return ErrInvalidArgument
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if ts < s.maxTs {
		return ErrClockRegression
	}
	if layer == LayerUser && s.lockedCover(cat, ch, ts) {
		return ErrLockedOverride
	}
	k := ruleKey{layer, cat, ch}
	if _, ok := s.rules[k]; !ok {
		return ErrRuleNotFound
	}
	delete(s.rules, k)
	s.maxTs = ts
	return nil
}

// UnsubscribeAll suppresses all user-layer rules with Ts <= ts without
// deleting them. Rejection order: invalid argument, clock regression.
func (s *Store) UnsubscribeAll(ts int64) error {
	if ts < 0 {
		return ErrInvalidArgument
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

// Resolve arbitrates whether the notification category cat is allowed
// on the concrete channel ch at time now. It never mutates the store.
// Rejection order: invalid argument, clock regression.
func (s *Store) Resolve(cat, ch string, now int64) (Decision, error) {
	if cat == "" || !validCat(cat) || !concreteCh(ch) || now < 0 {
		return Decision{}, ErrInvalidArgument
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if now < s.maxTs {
		return Decision{}, ErrClockRegression
	}
	locked := false
	for _, r := range s.rules {
		if r.Locked && s.liveAt(r, now) && segPrefix(r.Cat, cat) && (r.Ch == ChAny || r.Ch == ch) {
			locked = true
			break
		}
	}
	var best Rule
	found := false
	for _, r := range s.rules {
		if !s.liveAt(r, now) {
			continue
		}
		if !segPrefix(r.Cat, cat) || (r.Ch != ChAny && r.Ch != ch) {
			continue
		}
		if locked && !r.Locked {
			continue
		}
		if !found || better(r, best, locked) {
			best, found = r, true
		}
	}
	if !found {
		return Decision{Allow: false}, nil
	}
	return Decision{Allow: best.Eff == Allow, Layer: best.Layer, Cat: best.Cat, Ch: best.Ch, Seq: best.Seq}, nil
}

// better reports whether a beats b. In locked mode the order is
// (smaller layer, more cat segments, exact channel); otherwise it is
// (more cat segments, exact channel, larger layer). Ties are broken by
// larger Seq so results never depend on map iteration order.
func better(a, b Rule, locked bool) bool {
	sa, sb := len(catSegs(a.Cat)), len(catSegs(b.Cat))
	ea, eb := a.Ch != ChAny, b.Ch != ChAny
	if locked {
		if a.Layer != b.Layer {
			return a.Layer < b.Layer
		}
		if sa != sb {
			return sa > sb
		}
		if ea != eb {
			return ea
		}
	} else {
		if sa != sb {
			return sa > sb
		}
		if ea != eb {
			return ea
		}
		if a.Layer != b.Layer {
			return a.Layer > b.Layer
		}
	}
	return a.Seq > b.Seq
}

// Snapshot returns the stored rules in a deterministic order (by Seq).
// It is intended for inspection and testing.
func (s *Store) Snapshot() []Rule {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Rule, 0, len(s.rules))
	for _, r := range s.rules {
		out = append(out, r)
	}
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j].Seq < out[j-1].Seq; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

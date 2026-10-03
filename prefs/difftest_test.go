package prefs

import (
	"errors"
	"fmt"
	"math/rand"
	"sort"
	"testing"
)

// naiveStore is a deliberately simple reference implementation: it keeps
// accepted rules in an unordered slice and resolves by enumerating every
// rule per the specification. It exists only to cross-check Store.
type naiveStore struct {
	rules []Rule
	maxTs int64
	tomb  int64
	seq   int64
}

func newNaiveStore() *naiveStore {
	return &naiveStore{tomb: -1}
}

func (n *naiveStore) effective(r Rule, now int64) bool {
	if r.Exp != 0 && now >= r.Exp {
		return false
	}
	if r.Layer == LayerUser && r.Ts <= n.tomb {
		return false
	}
	return true
}

func (n *naiveStore) find(k key) int {
	for i, r := range n.rules {
		if r.Layer == k.layer && r.Cat == k.cat && r.Ch == k.ch {
			return i
		}
	}
	return -1
}

func (n *naiveStore) lockedCovered(cat, ch string, ts int64) bool {
	for _, r := range n.rules {
		if r.Locked && n.effective(r, ts) &&
			catPrefix(r.Cat, cat) && (r.Ch == ChAny || r.Ch == ch) {
			return true
		}
	}
	return false
}

func (n *naiveStore) set(r Rule) error {
	switch {
	case !validLayer(r.Layer) || !validCat(r.Cat) || !validRuleCh(r.Ch) ||
		!validEff(r.Eff) || r.Ts < 0 || (r.Exp != 0 && r.Exp <= r.Ts):
		return ErrInvalidParam
	case r.Ts < n.maxTs:
		return ErrClockRegression
	case r.Layer == LayerUser && r.Locked:
		return ErrPermissionDenied
	case r.Layer == LayerUser && n.lockedCovered(r.Cat, r.Ch, r.Ts):
		return ErrLockedOverride
	case n.find(key{r.Layer, r.Cat, r.Ch}) < 0 && len(n.rules) >= maxRules:
		return ErrCapacity
	}
	n.seq++
	r.Seq = n.seq
	if i := n.find(key{r.Layer, r.Cat, r.Ch}); i >= 0 {
		n.rules[i] = r
	} else {
		n.rules = append(n.rules, r)
	}
	n.maxTs = r.Ts
	return nil
}

func (n *naiveStore) remove(layer int, cat, ch string, ts int64) error {
	switch {
	case !validLayer(layer) || !validCat(cat) || !validRuleCh(ch) || ts < 0:
		return ErrInvalidParam
	case ts < n.maxTs:
		return ErrClockRegression
	case layer == LayerUser && n.lockedCovered(cat, ch, ts):
		return ErrLockedOverride
	}
	i := n.find(key{layer, cat, ch})
	if i < 0 {
		return ErrNotFound
	}
	n.rules = append(n.rules[:i], n.rules[i+1:]...)
	n.maxTs = ts
	return nil
}

func (n *naiveStore) unsubscribeAll(ts int64) error {
	switch {
	case ts < 0:
		return ErrInvalidParam
	case ts < n.maxTs:
		return ErrClockRegression
	}
	n.tomb = ts
	n.maxTs = ts
	return nil
}

// resolve enumerates all rules, collects candidates, and sorts them by the
// specification's comparison order. It also returns a human-readable
// rationale for logging.
func (n *naiveStore) resolve(cat, ch string, now int64) (Decision, string, error) {
	if cat == "" || !validCat(cat) || !validConcreteCh(ch) || now < 0 {
		return Decision{}, "", ErrInvalidParam
	}
	if now < n.maxTs {
		return Decision{}, "", ErrClockRegression
	}
	var cands []Rule
	for _, r := range n.rules {
		if n.effective(r, now) && catPrefix(r.Cat, cat) &&
			(r.Ch == ChAny || r.Ch == ch) {
			cands = append(cands, r)
		}
	}
	if len(cands) == 0 {
		return Decision{Allowed: false}, "no candidate -> default deny", nil
	}
	locked := make([]Rule, 0, len(cands))
	for _, r := range cands {
		if r.Locked {
			locked = append(locked, r)
		}
	}
	var why string
	pool := cands
	if len(locked) > 0 {
		pool = locked
		// Locked: smaller layer, then more segments, then exact channel.
		sort.Slice(pool, func(i, j int) bool {
			a, b := pool[i], pool[j]
			if a.Layer != b.Layer {
				return a.Layer < b.Layer
			}
			if sa, sb := catSegs(a.Cat), catSegs(b.Cat); sa != sb {
				return sa > sb
			}
			return a.Ch != ChAny && b.Ch == ChAny
		})
		why = fmt.Sprintf("%d candidate(s), %d locked -> locked order (layer asc, segs desc, exact ch)", len(cands), len(locked))
	} else {
		// Unlocked: more segments, then exact channel, then larger layer.
		sort.Slice(pool, func(i, j int) bool {
			a, b := pool[i], pool[j]
			if sa, sb := catSegs(a.Cat), catSegs(b.Cat); sa != sb {
				return sa > sb
			}
			if ea, eb := a.Ch != ChAny, b.Ch != ChAny; ea != eb {
				return ea
			}
			return a.Layer > b.Layer
		})
		why = fmt.Sprintf("%d candidate(s), none locked -> plain order (segs desc, exact ch, layer desc)", len(cands))
	}
	w := pool[0]
	d := Decision{
		Allowed: w.Eff == EffAllow,
		Layer:   w.Layer,
		Cat:     w.Cat,
		Ch:      w.Ch,
		Seq:     w.Seq,
	}
	return d, fmt.Sprintf("%s; winner=%+v", why, w), nil
}

var (
	diffCats = []string{
		"", "a", "b", "a/b", "a/c", "a/b/c", "a/b/c/d",
		"billing", "billing/invoice", "billing/refund", "billingx", "billingx/a",
		"marketing", "marketing/promo", "news", "news_daily", "x1", "y2",
	}
	diffRuleChs  = []string{ChAny, ChAny, ChEmail, ChSMS, ChPush}
	diffQueryChs = []string{ChEmail, ChSMS, ChPush}
	diffEffs     = []string{EffAllow, EffDeny}
)

// reasonOf maps an error to its sentinel reason for comparison.
func reasonOf(err error) error {
	for _, sentinel := range []error{
		ErrInvalidParam, ErrClockRegression, ErrPermissionDenied,
		ErrLockedOverride, ErrCapacity, ErrNotFound,
	} {
		if errors.Is(err, sentinel) {
			return sentinel
		}
	}
	return nil
}

// TestDifferential replays 2000 random operation sequences against both
// Store and the naive reference, comparing every rejection reason and
// decision, and logging inputs, outputs and rationale.
func TestDifferential(t *testing.T) {
	const rounds = 2000
	for round := 0; round < rounds; round++ {
		rng := rand.New(rand.NewSource(int64(round) + 1))
		real := NewStore()
		naive := newNaiveStore()
		var curTs int64

		nextTs := func() int64 {
			ts := curTs + int64(rng.Intn(7)) - 2
			if ts < 0 {
				ts = 0
			}
			return ts
		}
		randCat := func() string { return diffCats[rng.Intn(len(diffCats))] }
		randQueryCat := func() string {
			for {
				c := diffCats[rng.Intn(len(diffCats))]
				if c != "" {
					return c
				}
			}
		}

		ops := 20 + rng.Intn(30)
		for op := 0; op < ops; op++ {
			switch kind := rng.Intn(100); {
			case kind < 50: // Set
				layer := 1 + rng.Intn(3)
				r := Rule{
					Layer: layer,
					Cat:   randCat(),
					Ch:    diffRuleChs[rng.Intn(len(diffRuleChs))],
					Eff:   diffEffs[rng.Intn(len(diffEffs))],
					Ts:    nextTs(),
				}
				if layer != LayerUser && rng.Intn(4) == 0 {
					r.Locked = true
				}
				if layer == LayerUser && rng.Intn(20) == 0 {
					r.Locked = true // exercise permission rejection
				}
				if rng.Intn(10) < 3 {
					r.Exp = r.Ts + 1 + int64(rng.Intn(5))
				}
				errReal := real.Set(r)
				errNaive := naive.set(r)
				t.Logf("round %d op %d: Set(%+v) -> real=%v naive=%v", round, op, r, errReal, errNaive)
				if reasonOf(errReal) != reasonOf(errNaive) {
					t.Fatalf("round %d op %d: Set(%+v) reason mismatch: real=%v naive=%v",
						round, op, r, errReal, errNaive)
				}
				if errReal == nil && r.Ts > curTs {
					curTs = r.Ts
				}
			case kind < 65: // Remove
				layer := 1 + rng.Intn(3)
				cat, ch := randCat(), diffRuleChs[rng.Intn(len(diffRuleChs))]
				ts := nextTs()
				errReal := real.Remove(layer, cat, ch, ts)
				errNaive := naive.remove(layer, cat, ch, ts)
				t.Logf("round %d op %d: Remove(%d, %q, %q, %d) -> real=%v naive=%v",
					round, op, layer, cat, ch, ts, errReal, errNaive)
				if reasonOf(errReal) != reasonOf(errNaive) {
					t.Fatalf("round %d op %d: Remove(%d, %q, %q, %d) reason mismatch: real=%v naive=%v",
						round, op, layer, cat, ch, ts, errReal, errNaive)
				}
				if errReal == nil && ts > curTs {
					curTs = ts
				}
			case kind < 75: // UnsubscribeAll
				ts := nextTs()
				errReal := real.UnsubscribeAll(ts)
				errNaive := naive.unsubscribeAll(ts)
				t.Logf("round %d op %d: UnsubscribeAll(%d) -> real=%v naive=%v",
					round, op, ts, errReal, errNaive)
				if reasonOf(errReal) != reasonOf(errNaive) {
					t.Fatalf("round %d op %d: UnsubscribeAll(%d) reason mismatch: real=%v naive=%v",
						round, op, ts, errReal, errNaive)
				}
				if errReal == nil && ts > curTs {
					curTs = ts
				}
			default: // Resolve
				cat := randQueryCat()
				ch := diffQueryChs[rng.Intn(len(diffQueryChs))]
				now := curTs + int64(rng.Intn(4))
				dReal, errReal := real.Resolve(cat, ch, now)
				dNaive, why, errNaive := naive.resolve(cat, ch, now)
				t.Logf("round %d op %d: Resolve(%q, %q, %d) -> real=%+v naive=%+v (%s)",
					round, op, cat, ch, now, dReal, dNaive, why)
				if reasonOf(errReal) != reasonOf(errNaive) {
					t.Fatalf("round %d op %d: Resolve(%q, %q, %d) reason mismatch: real=%v naive=%v",
						round, op, cat, ch, now, errReal, errNaive)
				}
				if errReal == nil && dReal != dNaive {
					t.Fatalf("round %d op %d: Resolve(%q, %q, %d) decision mismatch: real=%+v naive=%+v (%s)",
						round, op, cat, ch, now, dReal, dNaive, why)
				}
			}
		}
	}
}

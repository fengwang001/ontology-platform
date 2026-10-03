package pref

// Differential test: the map-based Store is checked against a naive
// slice-based reference implementation over random operation sequences.

import (
	"fmt"
	"math/rand"
	"strings"
	"testing"
)

// naiveStore is an independent reference implementation. It keeps rules
// in insertion order in a slice and resolves by exhaustive enumeration.
type naiveStore struct {
	rules []Rule
	tomb  int64
	maxTs int64
	seq   uint64
}

func newNaive() *naiveStore { return &naiveStore{tomb: -1} }

func naiveValidCat(cat string) bool {
	if cat == "" {
		return true
	}
	segs := strings.Split(cat, "/")
	if len(segs) > 4 {
		return false
	}
	for _, s := range segs {
		if len(s) == 0 || len(s) > 16 {
			return false
		}
		for i := 0; i < len(s); i++ {
			c := s[i]
			if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '_') {
				return false
			}
		}
	}
	return true
}

func naiveValidCh(ch string) bool {
	return ch == "*" || ch == "email" || ch == "sms" || ch == "push"
}

func naiveSegs(cat string) []string {
	if cat == "" {
		return nil
	}
	return strings.Split(cat, "/")
}

func naivePrefix(ruleCat, queryCat string) bool {
	r, q := naiveSegs(ruleCat), naiveSegs(queryCat)
	if len(r) > len(q) {
		return false
	}
	ok := true
	for i := 0; i < len(r); i++ {
		ok = ok && r[i] == q[i]
	}
	return ok
}

func (n *naiveStore) live(r Rule, t int64) bool {
	expired := r.Exp != 0 && t >= r.Exp
	tombed := r.Layer == 3 && r.Ts <= n.tomb
	return !expired && !tombed
}

func (n *naiveStore) lockedCover(cat, ch string, ts int64) bool {
	for _, r := range n.rules {
		if r.Locked && (r.Exp == 0 || ts < r.Exp) && naivePrefix(r.Cat, cat) && (r.Ch == "*" || r.Ch == ch) {
			return true
		}
	}
	return false
}

func (n *naiveStore) set(layer int, cat, ch, eff string, locked bool, exp, ts int64) error {
	if layer < 1 || layer > 3 || !naiveValidCat(cat) || !naiveValidCh(ch) ||
		(eff != "allow" && eff != "deny") || (exp != 0 && exp <= ts) || ts < 0 {
		return ErrInvalidArgument
	}
	if ts < n.maxTs {
		return ErrClockRegression
	}
	if layer == 3 && locked {
		return ErrPermissionDenied
	}
	if layer == 3 && n.lockedCover(cat, ch, ts) {
		return ErrLockedOverride
	}
	idx := -1
	for i, r := range n.rules {
		if r.Layer == layer && r.Cat == cat && r.Ch == ch {
			idx = i
			break
		}
	}
	if idx < 0 && len(n.rules) >= MaxRules {
		return ErrCapacityExceeded
	}
	n.seq++
	r := Rule{Layer: layer, Cat: cat, Ch: ch, Eff: eff, Locked: locked, Exp: exp, Ts: ts, Seq: n.seq}
	if idx < 0 {
		n.rules = append(n.rules, r)
	} else {
		n.rules[idx] = r
	}
	n.maxTs = ts
	return nil
}

func (n *naiveStore) remove(layer int, cat, ch string, ts int64) error {
	if layer < 1 || layer > 3 || !naiveValidCat(cat) || !naiveValidCh(ch) || ts < 0 {
		return ErrInvalidArgument
	}
	if ts < n.maxTs {
		return ErrClockRegression
	}
	if layer == 3 && n.lockedCover(cat, ch, ts) {
		return ErrLockedOverride
	}
	for i, r := range n.rules {
		if r.Layer == layer && r.Cat == cat && r.Ch == ch {
			n.rules = append(n.rules[:i], n.rules[i+1:]...)
			n.maxTs = ts
			return nil
		}
	}
	return ErrRuleNotFound
}

func (n *naiveStore) unsub(ts int64) error {
	if ts < 0 {
		return ErrInvalidArgument
	}
	if ts < n.maxTs {
		return ErrClockRegression
	}
	n.tomb = ts
	n.maxTs = ts
	return nil
}

// naiveRank compares two candidates; negative means a wins.
func naiveRank(a, b Rule, locked bool) int {
	sa, sb := len(naiveSegs(a.Cat)), len(naiveSegs(b.Cat))
	ex := func(r Rule) int {
		if r.Ch == "*" {
			return 0
		}
		return 1
	}
	if locked {
		if a.Layer != b.Layer {
			return a.Layer - b.Layer // smaller layer first
		}
		if sa != sb {
			return sb - sa // more segments first
		}
		if ex(a) != ex(b) {
			return ex(b) - ex(a) // exact channel first
		}
	} else {
		if sa != sb {
			return sb - sa
		}
		if ex(a) != ex(b) {
			return ex(b) - ex(a)
		}
		if a.Layer != b.Layer {
			return b.Layer - a.Layer // larger layer first
		}
	}
	switch {
	case a.Seq > b.Seq:
		return -1
	case a.Seq < b.Seq:
		return 1
	}
	return 0
}

func (n *naiveStore) resolve(cat, ch string, now int64) (Decision, error) {
	if cat == "" || !naiveValidCat(cat) || (ch != "email" && ch != "sms" && ch != "push") || now < 0 {
		return Decision{}, ErrInvalidArgument
	}
	if now < n.maxTs {
		return Decision{}, ErrClockRegression
	}
	var cands []Rule
	locked := false
	for _, r := range n.rules {
		if n.live(r, now) && naivePrefix(r.Cat, cat) && (r.Ch == "*" || r.Ch == ch) {
			cands = append(cands, r)
			if r.Locked {
				locked = true
			}
		}
	}
	var best *Rule
	for i := range cands {
		if locked && !cands[i].Locked {
			continue
		}
		if best == nil || naiveRank(cands[i], *best, locked) < 0 {
			best = &cands[i]
		}
	}
	if best == nil {
		return Decision{Allow: false}, nil
	}
	return Decision{Allow: best.Eff == "allow", Layer: best.Layer, Cat: best.Cat, Ch: best.Ch, Seq: best.Seq}, nil
}

var randSegs = []string{"billing", "billingx", "invoice", "refund", "marketing", "promo", "a", "b", "c1", "x_y", "9"}

func randCat(r *rand.Rand, allowRoot bool) string {
	if allowRoot && r.Intn(5) == 0 {
		return ""
	}
	depth := 1 + r.Intn(4)
	parts := make([]string, depth)
	for i := range parts {
		parts[i] = randSegs[r.Intn(len(randSegs))]
	}
	return strings.Join(parts, "/")
}

func randCh(r *rand.Rand, concrete bool) string {
	pool := []string{"*", "email", "sms", "push"}
	if concrete {
		pool = pool[1:]
	}
	return pool[r.Intn(len(pool))]
}

// TestDifferential replays 2000 random operation sequences against both
// implementations and requires identical rejections and decisions.
// Inputs, outputs and the deciding rule are logged (go test -v).
func TestDifferential(t *testing.T) {
	r := rand.New(rand.NewSource(20261003))
	for iter := 0; iter < 2000; iter++ {
		st := NewStore()
		nv := newNaive()
		clock := int64(0)
		log := func(format string, args ...interface{}) {
			t.Logf("iter "+fmt.Sprint(iter)+": "+format, args...)
		}
		ops := 3 + r.Intn(25)
		for op := 0; op < ops; op++ {
			switch kind := r.Intn(10); {
			case kind < 6: // Set
				layer := 1 + r.Intn(3)
				cat := randCat(r, true)
				ch := randCh(r, false)
				eff := []string{"allow", "deny"}[r.Intn(2)]
				locked := layer != 3 && r.Intn(4) == 0
				ts := clock + int64(r.Intn(3))
				var exp int64
				if r.Intn(3) != 0 && ts > 0 {
					exp = ts + int64(r.Intn(5))
				}
				switch r.Intn(20) { // inject invalid inputs
				case 0:
					cat = "Bad//cat"
				case 1:
					ch = "web"
				case 2:
					ts = clock - 1
				case 3:
					locked = true
					layer = 3
				case 4:
					exp = ts // exp <= ts
				}
				errA := st.Set(layer, cat, ch, eff, locked, exp, ts)
				errB := nv.set(layer, cat, ch, eff, locked, exp, ts)
				log("Set(layer=%d cat=%q ch=%q eff=%q locked=%v exp=%d ts=%d) -> %v | %v",
					layer, cat, ch, eff, locked, exp, ts, errA, errB)
				if errA != errB {
					t.Fatalf("Set mismatch: store=%v naive=%v (rules: %+v)", errA, errB, nv.rules)
				}
				if errA == nil && ts > clock {
					clock = ts
				}
			case kind < 8: // Remove
				layer := 1 + r.Intn(3)
				cat := randCat(r, true)
				ch := randCh(r, false)
				if r.Intn(2) == 0 && len(nv.rules) > 0 {
					pick := nv.rules[r.Intn(len(nv.rules))]
					layer, cat, ch = pick.Layer, pick.Cat, pick.Ch
				}
				ts := clock + int64(r.Intn(2))
				errA := st.Remove(layer, cat, ch, ts)
				errB := nv.remove(layer, cat, ch, ts)
				log("Remove(layer=%d cat=%q ch=%q ts=%d) -> %v | %v", layer, cat, ch, ts, errA, errB)
				if errA != errB {
					t.Fatalf("Remove mismatch: store=%v naive=%v (rules: %+v)", errA, errB, nv.rules)
				}
				if errA == nil && ts > clock {
					clock = ts
				}
			default: // UnsubscribeAll
				ts := clock + int64(r.Intn(2))
				if r.Intn(15) == 0 {
					ts = clock - 1
				}
				errA := st.UnsubscribeAll(ts)
				errB := nv.unsub(ts)
				log("UnsubscribeAll(ts=%d) -> %v | %v", ts, errA, errB)
				if errA != errB {
					t.Fatalf("UnsubscribeAll mismatch: store=%v naive=%v", errA, errB)
				}
				if errA == nil && ts > clock {
					clock = ts
				}
			}
		}
		queries := 3 + r.Intn(6)
		for q := 0; q < queries; q++ {
			cat := randCat(r, false)
			ch := randCh(r, true)
			if r.Intn(25) == 0 {
				cat = "" // invalid query
			}
			now := clock + int64(r.Intn(3))
			dA, errA := st.Resolve(cat, ch, now)
			dB, errB := nv.resolve(cat, ch, now)
			log("Resolve(cat=%q ch=%q now=%d) -> %+v,%v | %+v,%v", cat, ch, now, dA, errA, dB, errB)
			if errA != errB || dA != dB {
				t.Fatalf("Resolve mismatch: store=%+v,%v naive=%+v,%v (rules: %+v tomb=%d)",
					dA, errA, dB, errB, nv.rules, nv.tomb)
			}
		}
		// Identical replay must reproduce identical state.
		if got, want := len(st.Snapshot()), len(nv.rules); got != want {
			t.Fatalf("rule count mismatch: store=%d naive=%d", got, want)
		}
	}
}

package limiter_test

import (
	"errors"
	"fmt"
	"math/big"
	"math/rand"
	"reflect"
	"sort"
	"strings"
	"testing"

	"ontology/limiter"
	"ontology/rule"
)

// naive is an independent, deliberately unsophisticated implementation of
// the same specification: counters roll one window at a time, estimates
// use big.Int, and selection scans every rule. It exists only to
// cross-check the real limiter.
type naive struct {
	rules  map[string]*naiveRule
	ctr    map[string]map[string]*naiveCounter
	shadow map[string]int64
	maxNow int64
}

type naiveRule struct {
	pat  []rule.KV // canonical, sorted by key
	l, w int64
	mode limiter.Mode
}

type naiveCounter struct{ k, cur, prev int64 }

func newNaive() *naive {
	return &naive{
		rules:  map[string]*naiveRule{},
		ctr:    map[string]map[string]*naiveCounter{},
		shadow: map[string]int64{},
	}
}

func canon(pairs []rule.KV) []rule.KV {
	cp := make([]rule.KV, len(pairs))
	copy(cp, pairs)
	sort.Slice(cp, func(i, j int) bool { return cp[i].Key < cp[j].Key })
	return cp
}

func naiveValidPairs(pairs []rule.KV, max int) bool {
	if len(pairs) < 1 || len(pairs) > max {
		return false
	}
	seen := map[string]bool{}
	for _, kv := range pairs {
		if kv.Key == "" || kv.Value == "" || seen[kv.Key] {
			return false
		}
		seen[kv.Key] = true
	}
	return true
}

func encStr(s string) string { return fmt.Sprintf("%d:%s;", len(s), s) }

func patSignature(pat []rule.KV) string {
	var b strings.Builder
	for _, kv := range pat {
		b.WriteString(encStr(kv.Key))
		b.WriteString(encStr(kv.Value))
	}
	return b.String()
}

func patKeySet(pat []rule.KV) string {
	var b strings.Builder
	for _, kv := range pat {
		b.WriteString(encStr(kv.Key))
	}
	return b.String()
}

func (n *naive) addRule(id string, pairs []rule.KV, l, w int64, mode limiter.Mode) error {
	if id == "" || !naiveValidPairs(pairs, 3) ||
		l < 1 || l > 1_000_000_000 || w < 1 || w > 1_000_000_000 ||
		(mode != limiter.Enforce && mode != limiter.Shadow) {
		return limiter.ErrInvalidArgument
	}
	if _, ok := n.rules[id]; ok {
		return limiter.ErrRuleExists
	}
	sig := patSignature(canon(pairs))
	for _, r := range n.rules {
		if patSignature(r.pat) == sig {
			return limiter.ErrPatternExists
		}
	}
	n.rules[id] = &naiveRule{pat: canon(pairs), l: l, w: w, mode: mode}
	return nil
}

func (n *naive) setMode(id string, mode limiter.Mode) error {
	if mode != limiter.Enforce && mode != limiter.Shadow {
		return limiter.ErrInvalidArgument
	}
	r, ok := n.rules[id]
	if !ok {
		return limiter.ErrNotFound
	}
	r.mode = mode
	return nil
}

func (n *naive) removeRule(id string) error {
	if _, ok := n.rules[id]; !ok {
		return limiter.ErrNotFound
	}
	delete(n.rules, id)
	delete(n.ctr, id)
	return nil
}

// rollOneStep advances c to the window of now, one window at a time.
// Windows skipped entirely are empty, so they are jumped over directly.
func rollOneStep(c *naiveCounter, now, w int64) {
	target := now / w
	if target > c.k+1 {
		c.prev, c.cur = 0, 0
		c.k = target - 1
	}
	for c.k < target {
		c.prev = c.cur
		c.cur = 0
		c.k++
	}
}

// bigEst computes cur + floor(prev*(w-now%w)/w) with big.Int.
func bigEst(c *naiveCounter, now, w int64) int64 {
	cp := *c
	rollOneStep(&cp, now, w)
	num := new(big.Int).Mul(big.NewInt(cp.prev), big.NewInt(w-now%w))
	return new(big.Int).Add(new(big.Int).Quo(num, big.NewInt(w)), big.NewInt(cp.cur)).Int64()
}

// allow mirrors Limiter.Allow and also returns a human-readable reason.
func (n *naive) allow(desc []rule.KV, now int64) (limiter.Result, error, string) {
	if !naiveValidPairs(desc, 8) {
		return limiter.Result{}, limiter.ErrInvalidArgument, "invalid descriptor"
	}
	if now < 0 || now > 1_000_000_000_000_000 {
		return limiter.Result{}, limiter.ErrInvalidTime, "invalid time"
	}
	if now < n.maxNow {
		return limiter.Result{}, limiter.ErrClockBackwards, "clock backwards"
	}
	n.maxNow = now

	descMap := map[string]string{}
	for _, kv := range desc {
		descMap[kv.Key] = kv.Value
	}

	// Group matching rules by key set.
	type candidate struct {
		id    string
		r     *naiveRule
		exact int
	}
	groups := map[string][]candidate{}
	for id, r := range n.rules {
		ok := true
		for _, kv := range r.pat {
			v, present := descMap[kv.Key]
			if !present || (kv.Value != "*" && kv.Value != v) {
				ok = false
				break
			}
		}
		if !ok {
			continue
		}
		exact := 0
		for _, kv := range r.pat {
			if kv.Value != "*" {
				exact++
			}
		}
		ks := patKeySet(r.pat)
		groups[ks] = append(groups[ks], candidate{id: id, r: r, exact: exact})
	}

	var winners []candidate
	for _, g := range groups {
		best := g[0]
		for _, c := range g[1:] {
			if c.exact > best.exact || (c.exact == best.exact && c.id < best.id) {
				best = c
			}
		}
		winners = append(winners, best)
	}
	sort.Slice(winners, func(i, j int) bool { return winners[i].id < winners[j].id })

	res := limiter.Result{Allowed: true}
	if len(winners) == 0 {
		return res, nil, "no rule selected"
	}
	for _, wn := range winners {
		res.Matched = append(res.Matched, wn.id)
	}

	type eval struct {
		id   string
		vk   string
		r    *naiveRule
		est  int64
		pass bool
	}
	evals := make([]eval, 0, len(winners))
	rejectedBy := ""
	var why []string
	for _, wn := range winners {
		var vk strings.Builder
		for _, kv := range wn.r.pat {
			vk.WriteString(encStr(descMap[kv.Key]))
		}
		var est int64
		if c, ok := n.ctr[wn.id][vk.String()]; ok {
			est = bigEst(c, now, wn.r.w)
		}
		pass := est+1 <= wn.r.l
		evals = append(evals, eval{id: wn.id, vk: vk.String(), r: wn.r, est: est, pass: pass})
		why = append(why, fmt.Sprintf("%s est=%d L=%d pass=%v", wn.id, est, wn.r.l, pass))
		if wn.r.mode == limiter.Enforce && !pass && (rejectedBy == "" || wn.id < rejectedBy) {
			rejectedBy = wn.id
		}
	}
	if rejectedBy != "" {
		return limiter.Result{Allowed: false, RejectedBy: rejectedBy, Matched: res.Matched},
			nil, "enforce reject: " + strings.Join(why, "; ")
	}
	for _, ev := range evals {
		byValues := n.ctr[ev.id]
		if byValues == nil {
			byValues = map[string]*naiveCounter{}
			n.ctr[ev.id] = byValues
		}
		c, ok := byValues[ev.vk]
		if !ok {
			c = &naiveCounter{k: now / ev.r.w}
			byValues[ev.vk] = c
		}
		rollOneStep(c, now, ev.r.w)
		c.cur++
		if c.cur > ev.r.l+1 {
			c.cur = ev.r.l + 1
		}
		if ev.r.mode == limiter.Shadow && !ev.pass {
			n.shadow[ev.id]++
			res.ShadowRejected = append(res.ShadowRejected, ev.id)
		}
	}
	return res, nil, "allow: " + strings.Join(why, "; ")
}

func (n *naive) tracked() int {
	total := 0
	for _, byValues := range n.ctr {
		total += len(byValues)
	}
	return total
}

// --- random operation generation ---

var (
	simIDs    = []string{"r0", "r1", "r2", "r3", "r4"}
	simKeys   = []string{"k0", "k1", "k2"}
	simValues = []string{"v0", "v1", "v2"}
)

func randPairs(rnd *rand.Rand, maxN int, wildcard bool) []rule.KV {
	keys := rnd.Perm(len(simKeys))[:rnd.Intn(maxN)+1]
	out := make([]rule.KV, 0, len(keys))
	for _, ki := range keys {
		v := simValues[rnd.Intn(len(simValues))]
		if wildcard && rnd.Intn(3) == 0 {
			v = "*"
		}
		out = append(out, rule.KV{Key: simKeys[ki], Value: v})
	}
	return out
}

func TestRandomSequencesAgainstNaive(t *testing.T) {
	const sequences = 2000
	const opsPerSeq = 30

	for seq := 0; seq < sequences; seq++ {
		rnd := rand.New(rand.NewSource(int64(seq)))
		real := limiter.New()
		naive := newNaive()
		now := int64(0)

		for op := 0; op < opsPerSeq; op++ {
			kind := rnd.Intn(100)
			logPrefix := fmt.Sprintf("seq=%d op=%02d", seq, op)

			switch {
			case kind < 30: // AddRule
				id := simIDs[rnd.Intn(len(simIDs))]
				pairs := randPairs(rnd, 3, true)
				l, w := int64(rnd.Intn(4)+1), int64(rnd.Intn(180)+20)
				mode := limiter.Mode(rnd.Intn(2))
				switch rnd.Intn(12) { // occasional invalid input
				case 0:
					pairs = append(pairs, rule.KV{Key: "k3", Value: "v0"})
					if len(pairs) <= 3 {
						pairs = append(pairs, rule.KV{Key: "k4", Value: "v0"})
					}
				case 1:
					l = 0
				case 2:
					w = 1_000_000_001
				case 3:
					id = ""
				}
				errReal := real.AddRule(id, pairs, l, w, mode)
				errNaive := naive.addRule(id, pairs, l, w, mode)
				if !errors.Is(errReal, errNaive) || (errReal == nil) != (errNaive == nil) {
					t.Fatalf("%s AddRule(%q,%v,%d,%d,%d): real=%v naive=%v",
						logPrefix, id, pairs, l, w, mode, errReal, errNaive)
				}
				t.Logf("%s AddRule(id=%q pairs=%v L=%d W=%d mode=%d) -> %v",
					logPrefix, id, pairs, l, w, mode, errReal)

			case kind < 75: // Allow
				desc := randPairs(rnd, 3, false)
				switch rnd.Intn(16) { // occasional invalid descriptor
				case 0:
					desc = nil
				case 1:
					desc = append(desc, desc[0])
				case 2:
					desc = append(desc, rule.KV{Key: "", Value: "v0"})
				}
				switch rnd.Intn(16) { // mostly advance time
				case 0:
					now -= int64(rnd.Intn(50) + 1) // may go backwards or negative
				case 1:
					now = 1_000_000_000_000_001 // invalid
				default:
					now += int64(rnd.Intn(150))
				}
				resReal, errReal := real.Allow(desc, now)
				resNaive, errNaive, why := naive.allow(desc, now)
				if !errors.Is(errReal, errNaive) || (errReal == nil) != (errNaive == nil) {
					t.Fatalf("%s Allow(%v,%d): real err=%v naive err=%v",
						logPrefix, desc, now, errReal, errNaive)
				}
				if errReal == nil && !reflect.DeepEqual(resReal, resNaive) {
					t.Fatalf("%s Allow(%v,%d): real=%+v naive=%+v (%s)",
						logPrefix, desc, now, resReal, resNaive, why)
				}
				t.Logf("%s Allow(desc=%v now=%d) -> %+v err=%v why=%s",
					logPrefix, desc, now, resReal, errReal, why)

			case kind < 88: // SetMode
				id := simIDs[rnd.Intn(len(simIDs))]
				mode := limiter.Mode(rnd.Intn(2))
				if rnd.Intn(12) == 0 {
					mode = limiter.Mode(9)
				}
				errReal := real.SetMode(id, mode)
				errNaive := naive.setMode(id, mode)
				if !errors.Is(errReal, errNaive) || (errReal == nil) != (errNaive == nil) {
					t.Fatalf("%s SetMode(%q,%d): real=%v naive=%v",
						logPrefix, id, mode, errReal, errNaive)
				}
				t.Logf("%s SetMode(id=%q mode=%d) -> %v", logPrefix, id, mode, errReal)

			default: // RemoveRule
				id := simIDs[rnd.Intn(len(simIDs))]
				errReal := real.RemoveRule(id)
				errNaive := naive.removeRule(id)
				if !errors.Is(errReal, errNaive) || (errReal == nil) != (errNaive == nil) {
					t.Fatalf("%s RemoveRule(%q): real=%v naive=%v",
						logPrefix, id, errReal, errNaive)
				}
				t.Logf("%s RemoveRule(id=%q) -> %v", logPrefix, id, errReal)
			}

			if real.Tracked() != naive.tracked() {
				t.Fatalf("%s Tracked: real=%d naive=%d", logPrefix, real.Tracked(), naive.tracked())
			}
			for _, id := range simIDs {
				if real.ShadowReject(id) != naive.shadow[id] {
					t.Fatalf("%s ShadowReject(%q): real=%d naive=%d",
						logPrefix, id, real.ShadowReject(id), naive.shadow[id])
				}
			}
		}
	}
}

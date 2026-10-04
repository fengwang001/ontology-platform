// Package limiter combines multiple rate-limit rules into a single
// allow/deny decision for a request descriptor. It is the only package
// that holds state; all methods are safe for concurrent use and behave as
// if executed in some serial order.
package limiter

import (
	"errors"
	"sync"

	"ontology/rule"
	"ontology/window"
)

// Mode is the enforcement state of a rule.
type Mode int

const (
	// Enforce rules reject the whole request when over limit.
	Enforce Mode = iota
	// Shadow rules only record ShadowReject when over limit.
	Shadow
)

var (
	ErrInvalidArgument = errors.New("limiter: invalid argument")
	ErrRuleExists      = errors.New("limiter: rule id already exists")
	ErrPatternExists   = errors.New("limiter: pattern identical to an existing rule")
	ErrNotFound        = errors.New("limiter: rule not found")
	ErrInvalidTime     = errors.New("limiter: time out of range")
	ErrClockBackwards  = errors.New("limiter: clock moved backwards")
)

const (
	maxLimit  = 1_000_000_000
	maxWidth  = 1_000_000_000
	maxNow    = 1_000_000_000_000_000
	maxDescKV = 8
	maxPatKV  = 3
)

type entry struct {
	pat   rule.Pattern
	limit int64
	width int64
	mode  Mode
}

// Result is the outcome of one Allow call.
type Result struct {
	Allowed        bool
	RejectedBy     string   // smallest id (byte order) of the failing enforce rules
	Matched        []string // selected rule ids, byte order
	ShadowRejected []string // over-limit shadow rule ids, byte order
}

// Limiter is a descriptor-based hierarchical rate limiter.
type Limiter struct {
	mu       sync.Mutex
	rules    map[string]*entry
	counters map[string]map[string]*window.Counter // rule id -> values key -> counter
	shadowN  map[string]int64
	maxNow   int64
}

// New returns an empty limiter.
func New() *Limiter {
	return &Limiter{
		rules:    map[string]*entry{},
		counters: map[string]map[string]*window.Counter{},
		shadowN:  map[string]int64{},
	}
}

func validPairs(pairs []rule.KV, max int) bool {
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

func validMode(m Mode) bool { return m == Enforce || m == Shadow }

// AddRule registers a rule. Errors are reported in the order: invalid
// argument, id already exists, pattern identical to an existing rule.
func (l *Limiter) AddRule(id string, pairs []rule.KV, limit, width int64, mode Mode) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	if id == "" || !validPairs(pairs, maxPatKV) ||
		limit < 1 || limit > maxLimit || width < 1 || width > maxWidth ||
		!validMode(mode) {
		return ErrInvalidArgument
	}
	if _, ok := l.rules[id]; ok {
		return ErrRuleExists
	}
	pat := rule.NewPattern(pairs)
	for _, e := range l.rules {
		if e.pat.Signature() == pat.Signature() {
			return ErrPatternExists
		}
	}
	l.rules[id] = &entry{pat: pat, limit: limit, width: width, mode: mode}
	return nil
}

// SetMode switches a rule between Enforce and Shadow without resetting
// any of its counters.
func (l *Limiter) SetMode(id string, mode Mode) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	if !validMode(mode) {
		return ErrInvalidArgument
	}
	e, ok := l.rules[id]
	if !ok {
		return ErrNotFound
	}
	e.mode = mode
	return nil
}

// RemoveRule deletes a rule and all of its counters.
func (l *Limiter) RemoveRule(id string) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	if _, ok := l.rules[id]; !ok {
		return ErrNotFound
	}
	delete(l.rules, id)
	delete(l.counters, id)
	return nil
}

// Allow decides whether a request with the given descriptor may proceed
// at time now (milliseconds). Errors are reported in the order: invalid
// descriptor, invalid time, clock backwards; rejected calls change no
// state, not even maxNow. A validated call always advances maxNow.
func (l *Limiter) Allow(desc []rule.KV, now int64) (Result, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	if !validPairs(desc, maxDescKV) {
		return Result{}, ErrInvalidArgument
	}
	if now < 0 || now > maxNow {
		return Result{}, ErrInvalidTime
	}
	if now < l.maxNow {
		return Result{}, ErrClockBackwards
	}
	l.maxNow = now

	descMap := make(map[string]string, len(desc))
	for _, kv := range desc {
		descMap[kv.Key] = kv.Value
	}

	var matched []rule.Rule
	for id, e := range l.rules {
		if e.pat.Match(descMap) {
			matched = append(matched, rule.Rule{ID: id, Pattern: e.pat})
		}
	}
	selected := rule.Select(matched)
	res := Result{Allowed: true}
	if len(selected) == 0 {
		return res, nil
	}
	for _, r := range selected {
		res.Matched = append(res.Matched, r.ID)
	}

	type decision struct {
		id        string
		valuesKey string
		e         *entry
		pass      bool
	}
	decisions := make([]decision, 0, len(selected))
	rejectedBy := ""
	for _, r := range selected {
		e := l.rules[r.ID]
		vk := e.pat.ValuesKey(descMap)
		var est int64
		if c, ok := l.counters[r.ID][vk]; ok {
			est = c.Estimate(now, e.width)
		}
		pass := est+1 <= e.limit
		decisions = append(decisions, decision{id: r.ID, valuesKey: vk, e: e, pass: pass})
		if e.mode == Enforce && !pass && (rejectedBy == "" || r.ID < rejectedBy) {
			rejectedBy = r.ID
		}
	}

	// Any failing enforce rule rejects the whole request: no counter is
	// advanced or created and no shadow statistic is recorded.
	if rejectedBy != "" {
		return Result{Allowed: false, RejectedBy: rejectedBy, Matched: res.Matched}, nil
	}

	// Commit phase: roll and increment every selected rule's counter.
	for _, d := range decisions {
		byValues := l.counters[d.id]
		if byValues == nil {
			byValues = map[string]*window.Counter{}
			l.counters[d.id] = byValues
		}
		c, ok := byValues[d.valuesKey]
		if !ok {
			nc := window.NewCounter(now, d.e.width)
			c = &nc
			byValues[d.valuesKey] = c
		}
		c.Advance(now, d.e.width)
		c.Inc(d.e.limit + 1)
		if d.e.mode == Shadow && !d.pass {
			l.shadowN[d.id]++
			res.ShadowRejected = append(res.ShadowRejected, d.id)
		}
	}
	return res, nil
}

// Tracked returns the total number of live counters.
func (l *Limiter) Tracked() int {
	l.mu.Lock()
	defer l.mu.Unlock()

	n := 0
	for _, byValues := range l.counters {
		n += len(byValues)
	}
	return n
}

// ShadowReject returns how many requests the shadow rule id would have
// rejected so far.
func (l *Limiter) ShadowReject(id string) int64 {
	l.mu.Lock()
	defer l.mu.Unlock()

	return l.shadowN[id]
}

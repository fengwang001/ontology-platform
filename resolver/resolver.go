// Package resolver implements a caching alias-chain resolver.
//
// The resolver walks an alias chain for a name, consulting a bounded
// cache first and falling back to an injected upstream function for
// misses. Positive (alias/address) and negative answers are cached per
// name; the TTL of a resolution is the minimum remaining lifetime over
// all links of the chain.
package resolver

import (
	"errors"
	"fmt"
	"log"
	"sync"
	"time"
)

// MaxAliases is the maximum number of alias records allowed in a single
// resolution chain. Chains with more alias records are rejected.
const MaxAliases = 8

var (
	// ErrEmptyName is returned when the queried name (or an alias
	// target) is empty.
	ErrEmptyName = errors.New("resolver: empty name")
	// ErrChainLoop is returned when the alias chain (cache and upstream
	// answers combined) revisits a name.
	ErrChainLoop = errors.New("resolver: alias chain forms a loop")
	// ErrChainTooLong is returned when the chain contains more than
	// MaxAliases alias records.
	ErrChainTooLong = errors.New("resolver: alias chain too long")
)

// AnswerKind describes the kind of an upstream answer.
type AnswerKind int

const (
	// AnswerAlias is an alias record pointing to another name.
	AnswerAlias AnswerKind = iota
	// AnswerAddress is an address record carrying a set of addresses.
	AnswerAddress
	// AnswerNegative is a non-existence answer.
	AnswerNegative
)

// Answer is an upstream response for a single name.
type Answer struct {
	Kind AnswerKind

	// Target is the alias target name (AnswerAlias only).
	Target string
	// Addrs is the address set (AnswerAddress only).
	Addrs []string
	// TTL is the time to live of alias and address records.
	TTL time.Duration

	// AuthorityTTL is the TTL S of the authority record and Minimum is
	// its minimum field M (AnswerNegative only). The negative entry is
	// cached with TTL min(S, M).
	AuthorityTTL time.Duration
	Minimum      time.Duration
}

// Upstream answers a query for a single name.
type Upstream func(name string) (Answer, error)

// Result is the outcome of a successful resolution.
type Result struct {
	// Chain lists the names traversed, from the queried name through
	// the aliases up to and including the terminal name.
	Chain []string
	// Addrs is the terminal address set (empty when Negative is true).
	Addrs []string
	// Negative reports that the chain ends in a non-existence answer.
	Negative bool
	// TTL is the minimum remaining lifetime over all chain links.
	TTL time.Duration
}

// entryKind is the kind of a cached entry.
type entryKind int

const (
	entryAlias entryKind = iota
	entryAddress
	entryNegative
)

// entry is a single cached record. Expired entries (now >= expiresAt)
// are treated as misses and purged on the next store.
type entry struct {
	kind      entryKind
	target    string
	addrs     []string
	expiresAt time.Time
}

// record is a resolved record not yet committed to the cache.
type record struct {
	name   string
	kind   entryKind
	target string
	addrs  []string
	ttl    time.Duration
}

// call tracks an in-flight upstream query so concurrent resolvers of
// the same name share a single upstream call.
type call struct {
	done chan struct{}
	ans  Answer
	err  error
}

// Resolver resolves names along alias chains with a bounded cache.
// It is safe for concurrent use.
type Resolver struct {
	upstream Upstream
	capacity int
	now      func() time.Time
	logger   *log.Logger

	mu       sync.Mutex
	cache    map[string]*entry
	inflight map[string]*call

	// queryMu serializes upstream calls so that identical call and
	// answer sequences replay identically.
	queryMu sync.Mutex
}

// Option configures a Resolver.
type Option func(*Resolver)

// WithClock sets the time source (mainly for tests).
func WithClock(clock func() time.Time) Option {
	return func(r *Resolver) { r.now = clock }
}

// WithLogger sets a logger for resolution decisions.
func WithLogger(logger *log.Logger) Option {
	return func(r *Resolver) { r.logger = logger }
}

// New creates a Resolver with the given upstream and cache capacity.
func New(upstream Upstream, capacity int, opts ...Option) *Resolver {
	r := &Resolver{
		upstream: upstream,
		capacity: capacity,
		now:      time.Now,
		cache:    make(map[string]*entry),
		inflight: make(map[string]*call),
	}
	for _, opt := range opts {
		opt(r)
	}
	return r
}

// Resolve resolves name along its alias chain.
//
// Each link is first looked up in the cache; misses are fetched from
// the upstream and staged. Staged records are committed to the cache
// only when the whole chain resolves successfully, so a rejected
// resolution leaves the cache untouched.
func (r *Resolver) Resolve(name string) (Result, error) {
	if name == "" {
		return Result{}, ErrEmptyName
	}
	r.logf("resolve %q: start", name)

	visited := map[string]bool{name: true}
	chain := []string{name}
	var staged []record
	aliases := 0
	minTTL := time.Duration(1<<63 - 1)

	cur := name
	for {
		rec, remaining, ok := r.lookup(cur)
		if !ok {
			ans, err := r.fetch(cur)
			if err != nil {
				r.logf("resolve %q: upstream query %q failed: %v", name, cur, err)
				return Result{}, fmt.Errorf("resolver: upstream query %q: %w", cur, err)
			}
			rec = recordFromAnswer(cur, ans)
			remaining = rec.ttl
			if rec.ttl > 0 {
				staged = append(staged, rec)
			} else {
				r.logf("resolve %q: %q has zero TTL, used but not cached", name, cur)
			}
		}
		if remaining < minTTL {
			minTTL = remaining
		}

		switch rec.kind {
		case entryAddress:
			r.commit(staged)
			r.logf("resolve %q: addresses %v, chain %v, ttl %s", name, rec.addrs, chain, minTTL)
			return Result{Chain: chain, Addrs: rec.addrs, TTL: minTTL}, nil
		case entryNegative:
			r.commit(staged)
			r.logf("resolve %q: not exists, chain %v, ttl %s", name, chain, minTTL)
			return Result{Chain: chain, Negative: true, TTL: minTTL}, nil
		default: // entryAlias
			aliases++
			next := rec.target
			if visited[next] {
				r.logf("resolve %q: loop detected at %q", name, next)
				return Result{}, fmt.Errorf("%w: %q revisited", ErrChainLoop, next)
			}
			if aliases > MaxAliases {
				r.logf("resolve %q: chain too long (%d aliases)", name, aliases)
				return Result{}, ErrChainTooLong
			}
			if next == "" {
				return Result{}, ErrEmptyName
			}
			visited[next] = true
			chain = append(chain, next)
			cur = next
		}
	}
}

// Len returns the number of cached entries.
func (r *Resolver) Len() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.cache)
}

// lookup returns the cached record for name and its remaining lifetime.
// An entry is valid only while now < expiresAt.
func (r *Resolver) lookup(name string) (record, time.Duration, bool) {
	now := r.now()
	r.mu.Lock()
	defer r.mu.Unlock()
	e, ok := r.cache[name]
	if !ok {
		r.logf("lookup %q: cache miss", name)
		return record{}, 0, false
	}
	if !now.Before(e.expiresAt) {
		r.logf("lookup %q: cache entry expired at %s", name, e.expiresAt.Format(time.RFC3339Nano))
		return record{}, 0, false
	}
	remaining := e.expiresAt.Sub(now)
	r.logf("lookup %q: cache hit, remaining %s", name, remaining)
	return record{
		name:   name,
		kind:   e.kind,
		target: e.target,
		addrs:  e.addrs,
		ttl:    remaining,
	}, remaining, true
}

// fetch queries the upstream for name, deduplicating concurrent queries
// for the same name: waiters receive the exact same answer or error.
func (r *Resolver) fetch(name string) (Answer, error) {
	r.mu.Lock()
	if c, ok := r.inflight[name]; ok {
		r.mu.Unlock()
		r.logf("fetch %q: joining in-flight upstream query", name)
		<-c.done
		return c.ans, c.err
	}
	c := &call{done: make(chan struct{})}
	r.inflight[name] = c
	r.mu.Unlock()

	r.logf("fetch %q: querying upstream", name)
	r.queryMu.Lock()
	ans, err := r.upstream(name)
	r.queryMu.Unlock()

	r.mu.Lock()
	delete(r.inflight, name)
	r.mu.Unlock()
	c.ans, c.err = ans, err
	close(c.done)
	return ans, err
}

// commit stores staged records into the cache.
func (r *Resolver) commit(staged []record) {
	if len(staged) == 0 {
		return
	}
	now := r.now()
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, rec := range staged {
		r.storeLocked(now, rec)
	}
}

// storeLocked stores one record: expired entries are purged first; if
// the cache is still full, the entry with the earliest expiry is
// evicted (ties broken by the lexicographically smaller name).
func (r *Resolver) storeLocked(now time.Time, rec record) {
	if r.capacity <= 0 {
		return
	}
	for n, e := range r.cache {
		if !now.Before(e.expiresAt) {
			r.logf("store %q: purging expired entry %q", rec.name, n)
			delete(r.cache, n)
		}
	}
	if _, ok := r.cache[rec.name]; !ok && len(r.cache) >= r.capacity {
		victim := ""
		var victimExp time.Time
		first := true
		for n, e := range r.cache {
			if first || e.expiresAt.Before(victimExp) ||
				(e.expiresAt.Equal(victimExp) && n < victim) {
				victim, victimExp = n, e.expiresAt
				first = false
			}
		}
		r.logf("store %q: cache full, evicting %q", rec.name, victim)
		delete(r.cache, victim)
	}
	expiresAt := now.Add(rec.ttl)
	r.logf("store %q: ttl %s, expires at %s", rec.name, rec.ttl, expiresAt.Format(time.RFC3339Nano))
	r.cache[rec.name] = &entry{
		kind:      rec.kind,
		target:    rec.target,
		addrs:     rec.addrs,
		expiresAt: expiresAt,
	}
}

// recordFromAnswer converts an upstream answer into a record. Negative
// answers get TTL min(S, M); negative TTLs are clamped to zero.
func recordFromAnswer(name string, ans Answer) record {
	switch ans.Kind {
	case AnswerAlias:
		return record{name: name, kind: entryAlias, target: ans.Target, ttl: ans.TTL}
	case AnswerAddress:
		return record{name: name, kind: entryAddress, addrs: ans.Addrs, ttl: ans.TTL}
	default:
		ttl := ans.AuthorityTTL
		if ans.Minimum < ttl {
			ttl = ans.Minimum
		}
		if ttl < 0 {
			ttl = 0
		}
		return record{name: name, kind: entryNegative, ttl: ttl}
	}
}

func (r *Resolver) logf(format string, args ...any) {
	if r.logger != nil {
		r.logger.Printf(format, args...)
	}
}

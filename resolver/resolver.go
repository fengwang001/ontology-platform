// Package resolver implements a concurrent-safe caching resolver that
// follows alias chains, caches positive and negative answers, and only
// re-queries upstream for the hops that are missing or expired.
package resolver

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sync"
	"time"
)

// MaxAliasHops is the maximum number of alias records allowed in one chain.
const MaxAliasHops = 8

var (
	ErrEmptyName    = errors.New("resolver: empty name")
	ErrCycle        = errors.New("resolver: alias chain forms a cycle")
	ErrChainTooLong = errors.New("resolver: alias chain too long")
)

// Kind classifies an upstream answer.
type Kind int

const (
	// Alias redirects the resolution to another name.
	Alias Kind = iota
	// Address terminates the chain with a set of addresses.
	Address
	// Negative terminates the chain with a does-not-exist answer.
	Negative
)

// Response is the raw upstream answer for one name.
type Response struct {
	Kind    Kind
	Target  string        // Alias: target name.
	Addrs   []string      // Address: address set.
	TTL     time.Duration // Alias/Address time to live.
	SOA     time.Duration // Negative: authoritative record TTL (S).
	Minimum time.Duration // Negative: minimum (M).
}

// UpstreamFunc answers a query for a single name.
type UpstreamFunc func(ctx context.Context, name string) (Response, error)

// Record is the normalized, cacheable form of a Response.
type Record struct {
	Kind   Kind
	Target string
	Addrs  []string
	TTL    time.Duration // Effective TTL; for Negative already min(SOA, Minimum).
}

// Hop is one step of a resolved chain.
type Hop struct {
	Name      string
	Record    Record
	FromCache bool
}

// Result is the outcome of a successful Resolve call.
type Result struct {
	Chain    []Hop
	Addrs    []string // Non-empty for a positive (Address) answer.
	Negative bool     // True when the chain ends in a does-not-exist answer.
	TTL      time.Duration
}

type entry struct {
	rec    Record
	expiry time.Time
}

type stagedEntry struct {
	name string
	rec  Record
}

type call struct {
	done chan struct{}
	resp Response
	err  error
}

// Resolver resolves names along alias chains with a bounded cache.
type Resolver struct {
	capacity int
	upstream UpstreamFunc
	now      func() time.Time

	mu    sync.Mutex
	cache map[string]entry

	sfMu     sync.Mutex
	inflight map[string]*call
}

// Option customizes a Resolver.
type Option func(*Resolver)

// WithClock overrides the time source (mainly for tests).
func WithClock(clock func() time.Time) Option {
	return func(r *Resolver) { r.now = clock }
}

// New creates a Resolver with the given cache capacity and upstream.
func New(capacity int, upstream UpstreamFunc, opts ...Option) *Resolver {
	r := &Resolver{
		capacity: capacity,
		upstream: upstream,
		now:      time.Now,
		cache:    make(map[string]entry),
		inflight: make(map[string]*call),
	}
	for _, opt := range opts {
		opt(r)
	}
	return r
}

// Resolve follows the alias chain for name and returns the terminal
// answer with the minimum remaining TTL across the whole chain.
//
// Records fetched from upstream are staged locally and only committed to
// the cache when the whole chain resolves successfully, so a rejected
// resolution never mutates the cache.
func (r *Resolver) Resolve(ctx context.Context, name string) (Result, error) {
	if name == "" {
		return Result{}, ErrEmptyName
	}
	now := r.now()
	seen := make(map[string]struct{})
	var hops []Hop
	var staged []stagedEntry
	aliasCount := 0
	minTTL := time.Duration(math.MaxInt64)
	current := name
	for {
		if _, dup := seen[current]; dup {
			return Result{}, fmt.Errorf("%w: %q", ErrCycle, current)
		}
		if aliasCount > MaxAliasHops {
			return Result{}, fmt.Errorf("%w: more than %d alias records", ErrChainTooLong, MaxAliasHops)
		}
		seen[current] = struct{}{}
		rec, fromCache, remaining, err := r.lookup(ctx, current, now)
		if err != nil {
			return Result{}, err
		}
		if !fromCache && rec.TTL > 0 {
			staged = append(staged, stagedEntry{name: current, rec: rec})
		}
		hops = append(hops, Hop{Name: current, Record: rec, FromCache: fromCache})
		if remaining < minTTL {
			minTTL = remaining
		}
		switch rec.Kind {
		case Alias:
			aliasCount++
			current = rec.Target
		case Address:
			r.commit(staged)
			return Result{Chain: hops, Addrs: rec.Addrs, TTL: minTTL}, nil
		case Negative:
			r.commit(staged)
			return Result{Chain: hops, Negative: true, TTL: minTTL}, nil
		default:
			return Result{}, fmt.Errorf("resolver: upstream returned unknown record kind for %q", current)
		}
	}
}

// lookup returns the record for one hop: from the cache when a live entry
// exists, otherwise from upstream via singleflight.
func (r *Resolver) lookup(ctx context.Context, name string, now time.Time) (Record, bool, time.Duration, error) {
	r.mu.Lock()
	if e, ok := r.cache[name]; ok && now.Before(e.expiry) {
		remaining := e.expiry.Sub(now)
		r.mu.Unlock()
		return e.rec, true, remaining, nil
	}
	r.mu.Unlock()

	resp, err := r.fetch(ctx, name)
	if err != nil {
		return Record{}, false, 0, err
	}
	rec := normalize(resp)
	return rec, false, rec.TTL, nil
}

// fetch queries upstream, deduplicating concurrent queries for the same
// name so only one upstream call is in flight per name.
func (r *Resolver) fetch(ctx context.Context, name string) (Response, error) {
	r.sfMu.Lock()
	if c, ok := r.inflight[name]; ok {
		r.sfMu.Unlock()
		select {
		case <-c.done:
			return c.resp, c.err
		case <-ctx.Done():
			return Response{}, ctx.Err()
		}
	}
	c := &call{done: make(chan struct{})}
	r.inflight[name] = c
	r.sfMu.Unlock()

	resp, err := r.upstream(ctx, name)
	if err != nil {
		err = fmt.Errorf("resolver: upstream query %q: %w", name, err)
	}
	c.resp, c.err = resp, err
	close(c.done)

	r.sfMu.Lock()
	delete(r.inflight, name)
	r.sfMu.Unlock()
	return c.resp, c.err
}

// commit inserts the staged records into the cache in chain order.
func (r *Resolver) commit(staged []stagedEntry) {
	if len(staged) == 0 {
		return
	}
	now := r.now()
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, s := range staged {
		r.insertLocked(s.name, s.rec, now)
	}
}

// insertLocked stores one record, replacing any same-name entry. Expired
// entries are purged first; if the cache is still full, the entry with
// the earliest expiry is evicted (ties broken by the lexicographically
// smaller name).
func (r *Resolver) insertLocked(name string, rec Record, now time.Time) {
	if r.capacity <= 0 || rec.TTL <= 0 {
		return
	}
	for n, e := range r.cache {
		if !now.Before(e.expiry) {
			delete(r.cache, n)
		}
	}
	if _, ok := r.cache[name]; !ok {
		for len(r.cache) >= r.capacity {
			r.evictLocked()
		}
	}
	r.cache[name] = entry{rec: rec, expiry: now.Add(rec.TTL)}
}

func (r *Resolver) evictLocked() {
	victim := ""
	first := true
	for n, e := range r.cache {
		if first {
			victim, first = n, false
			continue
		}
		v := r.cache[victim]
		if e.expiry.Before(v.expiry) || (e.expiry.Equal(v.expiry) && n < victim) {
			victim = n
		}
	}
	delete(r.cache, victim)
}

// normalize converts a raw upstream response into its cacheable form.
func normalize(resp Response) Record {
	rec := Record{Kind: resp.Kind, Target: resp.Target, Addrs: resp.Addrs, TTL: resp.TTL}
	if resp.Kind == Negative {
		rec.TTL = min(resp.SOA, resp.Minimum)
		rec.Target = ""
		rec.Addrs = nil
	}
	return rec
}

// CacheSize reports the number of entries currently stored (including
// expired ones not yet purged by an insert).
func (r *Resolver) CacheSize() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.cache)
}

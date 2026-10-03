// Package cache implements authorization-aware lookup, single-flight
// coalescing, write invalidation and deterministic storage.
package cache

import (
	"errors"
	"strings"
	"sync"

	"ontology/key"
	"ontology/store"
)

// Visibility of a fetched response.
type Visibility int

const (
	Public Visibility = iota + 1
	Private
	NoStore
)

// Request is a gateway request.
type Request struct {
	Method  string
	Path    string
	Subject string
	Scopes  []string
	Headers map[string]string
}

// FetchResult is what an origin callback returns.
type FetchResult struct {
	Status int
	Vis    Visibility
	TTL    int64
	Vary   []string
	Size   int64
	Body   []byte
}

// FetchFunc is the origin callback.
type FetchFunc func(req Request) (*FetchResult, error)

// Source labels where a response came from.
type Source int

const (
	Hit Source = iota + 1
	Miss
	Shared
	Bypass
)

func (s Source) String() string {
	switch s {
	case Hit:
		return "Hit"
	case Miss:
		return "Miss"
	case Shared:
		return "Shared"
	case Bypass:
		return "Bypass"
	default:
		return "Unknown"
	}
}

// Result is a Get outcome delivered to the caller.
type Result struct {
	Status int
	Body   []byte
	Source Source
}

// EvictionEvent records one deterministic eviction for replay logs.
type EvictionEvent struct {
	Path string
	Seq  uint64
	Size int64
	Why  string
}

// Stats are observable counters.
type Stats struct {
	Fetches     int64
	Hits        int64
	Misses      int64
	Shared      int64
	Bypasses    int64
	Invalidated int64
	Evictions   []EvictionEvent
}

var (
	errInvalidArg   = errors.New("invalid argument")
	errInvalidTime  = errors.New("invalid time")
	errClockSkew    = errors.New("clock moved backwards")
	errForbidden    = errors.New("forbidden")
	errFetchInvalid = errors.New("invalid fetch response")
)

type policy struct {
	prefix string
	scope  string
}

type group struct {
	done    chan struct{}
	waiters int
	res     *FetchResult
	err     error
}

// Cache is the auth-aware shared response cache.
type Cache struct {
	cap int64
	v   int

	mu       sync.Mutex
	policies []policy
	maxNow   int64
	groups   map[string]*group
	stats    Stats
	store    *store.Store
}

// New creates a cache with byte capacity Cap (1..1e12) and per-path variant
// limit V (1..64).
func New(capBytes int64, maxVariantsPerPath int) *Cache {
	if capBytes < 1 || capBytes > 1e12 || maxVariantsPerPath < 1 || maxVariantsPerPath > 64 {
		panic("invalid cache parameters")
	}
	return &Cache{
		cap:    capBytes,
		v:      maxVariantsPerPath,
		groups: make(map[string]*group),
		store:  store.New(capBytes, maxVariantsPerPath),
	}
}

// AddPolicy registers a required scope for a path prefix. prefix must start
// with "/" and match on segment boundaries; scope must be non-empty.
func (c *Cache) AddPolicy(prefix, scope string) error {
	if !strings.HasPrefix(prefix, "/") || scope == "" {
		return errInvalidArg
	}
	c.mu.Lock()
	c.policies = append(c.policies, policy{prefix: prefix, scope: scope})
	c.mu.Unlock()
	return nil
}

// Get validates, authorizes, looks up, fetches/coalesces and stores.
func (c *Cache) Get(req Request, now int64, fetch FetchFunc) (*Result, error) {
	if err := validateRequest(req); err != nil {
		return nil, err
	}
	if now < 0 || now > 1e15 {
		return nil, errInvalidTime
	}

	c.mu.Lock()
	if now < c.maxNow {
		c.mu.Unlock()
		return nil, errClockSkew
	}
	required := c.requiredScopeLocked(req.Path)
	if required != "" && !contains(req.Scopes, required) {
		c.mu.Unlock()
		return nil, errForbidden
	}
	c.maxNow = now
	isGet := req.Method == "GET"
	if !isGet {
		c.mu.Unlock()
		return c.bypass(req, fetch)
	}

	ck := key.CoalescingKey(req.Path, req.Headers).Encode()
	if grp, ok := c.groups[ck]; ok {
		grp.waiters++
		c.mu.Unlock()
		return c.waitFollower(req, fetch, grp, now)
	}

	if hit := c.store.FindHit(req.Path, req.Subject, req.Headers, now); hit != nil {
		c.stats.Hits++
		c.mu.Unlock()
		return &Result{Status: hit.Status, Body: hit.Body, Source: Hit}, nil
	}

	grp := &group{done: make(chan struct{})}
	c.groups[ck] = grp
	c.mu.Unlock()

	return c.runLeader(req, fetch, grp, ck, now)
}

func (c *Cache) bypass(req Request, fetch FetchFunc) (*Result, error) {
	res, err := fetch(req)
	c.mu.Lock()
	c.stats.Fetches++
	if err == nil {
		if res == nil || res.TTL < 0 || res.Size < 0 {
			err = errFetchInvalid
		} else if res.Status >= 200 && res.Status <= 399 {
			c.invalidatePathLocked(req.Path)
		}
		c.stats.Bypasses++
	}
	c.mu.Unlock()
	if err != nil {
		return nil, err
	}
	return &Result{Status: res.Status, Body: res.Body, Source: Bypass}, nil
}

func (c *Cache) runLeader(req Request, fetch FetchFunc, grp *group, ck string, now int64) (*Result, error) {
	res, err := fetch(req)

	c.mu.Lock()
	c.stats.Fetches++
	if err != nil {
		grp.err = err
		close(grp.done)
		delete(c.groups, ck)
		c.mu.Unlock()
		return nil, err
	}
	if res == nil || res.TTL < 0 || res.Size < 0 ||
		(res.Vis != Public && res.Vis != Private && res.Vis != NoStore) {
		grp.err = errFetchInvalid
		close(grp.done)
		delete(c.groups, ck)
		c.mu.Unlock()
		return nil, errFetchInvalid
	}

	c.stats.Misses++
	grp.res = res
	close(grp.done)
	delete(c.groups, ck)
	c.storeIfAllowedLocked(req, res, now)
	c.mu.Unlock()

	return &Result{Status: res.Status, Body: res.Body, Source: Miss}, nil
}

func (c *Cache) waitFollower(req Request, fetch FetchFunc, grp *group, now int64) (*Result, error) {
	<-grp.done
	if grp.err != nil {
		return nil, grp.err
	}
	res := grp.res
	if res.Vis == Public {
		c.mu.Lock()
		c.stats.Shared++
		c.mu.Unlock()
		return &Result{Status: res.Status, Body: res.Body, Source: Shared}, nil
	}

	owned, err := fetch(req)
	c.mu.Lock()
	c.stats.Fetches++
	if err != nil {
		c.mu.Unlock()
		return nil, err
	}
	if owned == nil || owned.TTL < 0 || owned.Size < 0 {
		c.mu.Unlock()
		return nil, errFetchInvalid
	}
	c.stats.Misses++
	c.storeIfAllowedLocked(req, owned, now)
	c.mu.Unlock()
	return &Result{Status: owned.Status, Body: owned.Body, Source: Miss}, nil
}

func (c *Cache) storeIfAllowedLocked(req Request, res *FetchResult, now int64) {
	if res.Status < 200 || res.Status > 299 {
		return
	}
	if res.TTL <= 0 || res.Size > c.cap || containsStar(res.Vary) {
		return
	}
	var owner key.Scope
	switch res.Vis {
	case Public:
		owner = key.PublicScope
	case Private:
		if req.Subject == "" {
			return
		}
		owner = key.Scope(req.Subject)
	default:
		return
	}
	id := key.VariantIdentity(owner, res.Vary, req.Headers)
	entry := &store.Entry{
		Path:     req.Path,
		ID:       id,
		Status:   res.Status,
		Body:     res.Body,
		Size:     res.Size,
		StoredAt: now,
		TTL:      res.TTL,
		Seq:      c.store.NextSeq(),
	}
	for _, ev := range c.store.Put(req.Path, entry) {
		if ev.Why == "replace" {
			continue
		}
		c.stats.Evictions = append(c.stats.Evictions, EvictionEvent{
			Path: ev.Entry.Path,
			Seq:  ev.Entry.Seq,
			Size: ev.Entry.Size,
			Why:  ev.Why,
		})
	}
}

func (c *Cache) invalidatePathLocked(path string) {
	removed := c.store.InvalidatePath(path)
	if len(removed) > 0 {
		c.stats.Invalidated += int64(len(removed))
	}
}

// Stats returns a snapshot of counters and the eviction log.
func (c *Cache) Stats() Stats {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := c.stats
	out.Evictions = append([]EvictionEvent(nil), c.stats.Evictions...)
	return out
}

// Bytes returns the current total byte usage.
func (c *Cache) Bytes() int64 {
	return c.store.Bytes()
}

func (c *Cache) requiredScopeLocked(path string) string {
	best := ""
	for _, p := range c.policies {
		if segmentMatch(path, p.prefix) && len(p.prefix) > len(best) {
			best = p.prefix
		}
	}
	if best == "" {
		return ""
	}
	for _, p := range c.policies {
		if p.prefix == best {
			return p.scope
		}
	}
	return ""
}

func validateRequest(req Request) error {
	switch req.Method {
	case "GET", "POST", "PUT", "PATCH", "DELETE":
	default:
		return errInvalidArg
	}
	if !strings.HasPrefix(req.Path, "/") || strings.ContainsRune(req.Path, '?') {
		return errInvalidArg
	}
	if !key.ValidateHeaders(req.Headers) {
		return errInvalidArg
	}
	return nil
}

func segmentMatch(path, prefix string) bool {
	if !strings.HasPrefix(path, prefix) {
		return false
	}
	if len(path) == len(prefix) {
		return true
	}
	return path[len(prefix)] == '/'
}

func contains(list []string, target string) bool {
	for _, item := range list {
		if item == target {
			return true
		}
	}
	return false
}

func containsStar(vary []string) bool {
	for _, name := range vary {
		if name == "*" {
			return true
		}
	}
	return false
}

// Exported sentinel errors for tests.
var (
	ErrInvalidArg   = errInvalidArg
	ErrInvalidTime  = errInvalidTime
	ErrClockSkew    = errClockSkew
	ErrForbidden    = errForbidden
	ErrFetchInvalid = errFetchInvalid
)

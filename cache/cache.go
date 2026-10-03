// Package cache is the auth-aware orchestration layer over key and store.
package cache

import (
	"context"
	"sort"
	"strings"
	"sync"

	"ontology/key"
	"ontology/store"
)

// Source identifies where a Get response came from.
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
		return "None"
	}
}

// Stats holds reproducible counters for verification.
type Stats struct {
	Hits     int
	Misses   int
	Shared   int
	Bypasses int
	Fetches  int
	Evicted  int
}

// Cache is the auth-aware shared response cache.
type Cache struct {
	capBytes    int64
	maxVariants int

	mu         sync.Mutex
	policies   []policy
	maxNow     int64
	store      *store.Store
	groups     map[string]*flight
	joinSignal chan string

	stats Stats
}

type policy struct {
	prefix string
	scope  string
}

type flight struct {
	done chan struct{}
	res  FetchResult
	err  error
	ok   bool
}

// New creates a Cache. capBytes is 1..1e12, maxVariants is 1..64 per path.
func New(capBytes int64, maxVariants int) *Cache {
	return &Cache{
		capBytes:    capBytes,
		maxVariants: maxVariants,
		store:       store.New(capBytes, maxVariants),
		groups:      map[string]*flight{},
		joinSignal:  make(chan string, 1024),
	}
}

// AddPolicy registers a required scope for all paths at or below prefix.
func (c *Cache) AddPolicy(prefix, sc string) error {
	if !strings.HasPrefix(prefix, "/") || sc == "" {
		return errInvalidArgument
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for i := range c.policies {
		if c.policies[i].prefix == prefix {
			c.policies[i].scope = sc
			return nil
		}
	}
	c.policies = append(c.policies, policy{prefix: prefix, scope: sc})
	return nil
}

// segmentMatch reports whether path equals prefix or extends it at a
// segment boundary (prefix ends in "/" or path has '/' next).
func segmentMatch(path, prefix string) bool {
	if !strings.HasPrefix(path, prefix) {
		return false
	}
	if len(path) == len(prefix) || strings.HasSuffix(prefix, "/") {
		return true
	}
	return path[len(prefix)] == '/'
}

var validMethods = map[string]bool{
	"GET": true, "POST": true, "PUT": true, "PATCH": true, "DELETE": true,
}

func validateRequest(req Request) error {
	if !validMethods[req.Method] {
		return errInvalidArgument
	}
	if !strings.HasPrefix(req.Path, "/") || strings.Contains(req.Path, "?") {
		return errInvalidArgument
	}
	for name := range req.Headers {
		if name == "" {
			return errInvalidArgument
		}
	}
	return nil
}

// Get validates, authorizes, looks up, fetches and stores per the spec.
func (c *Cache) Get(ctx context.Context, req Request, now int64, fetch FetchFunc) (Response, Source, error) {
	if err := validateRequest(req); err != nil {
		return Response{}, 0, err
	}
	if now < 0 || now > 1_000_000_000_000_000 {
		return Response{}, 0, errInvalidTime
	}

	c.mu.Lock()
	if now < c.maxNow {
		c.mu.Unlock()
		return Response{}, 0, errClockSkew
	}
	if need := c.requiredScopeLocked(req.Path); need != "" && !containsScope(req.Scopes, need) {
		c.mu.Unlock()
		return Response{}, 0, errForbidden
	}
	c.maxNow = now
	c.mu.Unlock()

	if req.Method != "GET" {
		return c.bypass(ctx, req, fetch)
	}
	return c.getGET(ctx, req, now, fetch)
}

func (c *Cache) bypass(ctx context.Context, req Request, fetch FetchFunc) (Response, Source, error) {
	res, err := c.runFetch(ctx, req, fetch)
	if err != nil {
		return Response{}, 0, err
	}
	c.mu.Lock()
	c.stats.Bypasses++
	if res.Status >= 200 && res.Status <= 399 {
		c.store.InvalidatePath(req.Path)
	}
	c.mu.Unlock()
	return Response{Status: res.Status, Body: res.Body}, Bypass, nil
}

func (c *Cache) getGET(ctx context.Context, req Request, now int64, fetch FetchFunc) (Response, Source, error) {
	if e := c.lookup(req, now); e != nil {
		c.bump(func(st *Stats) { st.Hits++ })
		return Response{Status: e.Status, Body: e.Body}, Hit, nil
	}

	gkey := groupKey(req.Path, req.Headers)

	c.mu.Lock()
	if f, ok := c.groups[gkey]; ok {
		if c.joinSignal != nil {
			c.joinSignal <- gkey
		}
		c.mu.Unlock()
		return c.joinFlight(ctx, req, now, fetch, f)
	}
	f := &flight{done: make(chan struct{})}
	c.groups[gkey] = f
	if c.joinSignal != nil {
		c.joinSignal <- gkey
	}
	c.mu.Unlock()

	res, err := c.runFetch(ctx, req, fetch)
	f.res, f.err, f.ok = res, err, err == nil
	close(f.done)

	c.mu.Lock()
	delete(c.groups, gkey)
	var pr store.PutResult
	if err == nil {
		pr = c.maybeStore(req, res, now)
		c.stats.Misses++
	}
	c.mu.Unlock()
	c.countPut(pr)

	if err != nil {
		return Response{}, 0, err
	}
	return Response{Status: res.Status, Body: res.Body}, Miss, nil
}

func (c *Cache) joinFlight(ctx context.Context, req Request, now int64, fetch FetchFunc, f *flight) (Response, Source, error) {
	<-f.done
	if f.err != nil {
		return Response{}, 0, f.err
	}
	if f.ok && f.res.Visibility == Public {
		c.bump(func(st *Stats) { st.Shared++ })
		return Response{Status: f.res.Status, Body: f.res.Body}, Shared, nil
	}
	res, err := c.runFetch(ctx, req, fetch)
	if err != nil {
		return Response{}, 0, err
	}
	c.mu.Lock()
	pr := c.maybeStore(req, res, now)
	c.stats.Misses++
	c.mu.Unlock()
	c.countPut(pr)
	return Response{Status: res.Status, Body: res.Body}, Miss, nil
}

// lookup picks the best fresh matching variant: private for the subject
// beats public; ties prefer later StoredAt, then larger Seq.
func (c *Cache) lookup(req Request, now int64) *store.Entry {
	c.mu.Lock()
	entries := c.store.PathEntries(req.Path)
	c.mu.Unlock()

	var best *store.Entry
	for _, e := range entries {
		if now >= e.ExpireAt {
			continue
		}
		if e.ID.Owner != "" && (req.Subject == "" || e.ID.Owner != req.Subject) {
			continue
		}
		if !e.ID.Matches(req.Headers) {
			continue
		}
		if betterVariant(e, best) {
			best = e
		}
	}
	return best
}

func betterVariant(cand, best *store.Entry) bool {
	if best == nil {
		return true
	}
	candPrivate := cand.ID.Owner != ""
	bestPrivate := best.ID.Owner != ""
	if candPrivate != bestPrivate {
		return candPrivate
	}
	if cand.StoredAt != best.StoredAt {
		return cand.StoredAt > best.StoredAt
	}
	return cand.Seq > best.Seq
}

func (c *Cache) maybeStore(req Request, res FetchResult, now int64) store.PutResult {
	if res.Visibility != Public && !(res.Visibility == Private && req.Subject != "") {
		return store.PutResult{}
	}
	if res.Status < 200 || res.Status > 299 || res.TTLMillis <= 0 {
		return store.PutResult{}
	}
	if res.Size > c.capBytes || hasStarVary(res.Vary) {
		return store.PutResult{}
	}
	owner := ""
	if res.Visibility == Private {
		owner = req.Subject
	}
	id := key.Build(owner, res.Vary, req.Headers)
	e := &store.Entry{
		Path:     req.Path,
		ID:       id,
		Status:   res.Status,
		Body:     res.Body,
		Size:     res.Size,
		StoredAt: now,
		TTL:      res.TTLMillis,
		ExpireAt: now + res.TTLMillis,
		Public:   res.Visibility == Public,
	}
	return c.store.Put(e)
}

func (c *Cache) countPut(pr store.PutResult) {
	c.mu.Lock()
	c.stats.Evicted += len(pr.Evicted)
	c.mu.Unlock()
}

func (c *Cache) runFetch(ctx context.Context, req Request, fetch FetchFunc) (FetchResult, error) {
	res, err := fetch(ctx, req)
	c.mu.Lock()
	c.stats.Fetches++
	c.mu.Unlock()
	if err != nil {
		return res, err
	}
	if res.Visibility < Public || res.Visibility > NoStore || res.TTLMillis < 0 || res.Size < 0 {
		return res, errInvalidResponse
	}
	return res, nil
}

func (c *Cache) requiredScopeLocked(path string) string {
	best := ""
	for _, p := range c.policies {
		if len(p.prefix) > len(best) && segmentMatch(path, p.prefix) {
			best = p.prefix
		}
	}
	for _, p := range c.policies {
		if p.prefix == best {
			return p.scope
		}
	}
	return ""
}

func (c *Cache) bump(fn func(*Stats)) {
	c.mu.Lock()
	fn(&c.stats)
	c.mu.Unlock()
}

func containsScope(scopes []string, want string) bool {
	for _, s := range scopes {
		if s == want {
			return true
		}
	}
	return false
}

func hasStarVary(vary []string) bool {
	for _, v := range vary {
		if v == "*" {
			return true
		}
	}
	return false
}

// groupKey is path plus the header map with authorization and cookie
// removed, serialized deterministically.
func groupKey(path string, headers map[string]string) string {
	names := make([]string, 0, len(headers))
	for name := range headers {
		if name == "authorization" || name == "cookie" {
			continue
		}
		names = append(names, name)
	}
	sort.Strings(names)
	var b strings.Builder
	b.WriteString(path)
	b.WriteByte(0)
	for _, name := range names {
		b.WriteString(name)
		b.WriteByte('=')
		b.WriteString(headers[name])
		b.WriteByte(0)
	}
	return b.String()
}

// StatsSnapshot returns a copy of the current counters.
func (c *Cache) StatsSnapshot() Stats {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.stats
}

// Bytes returns total stored bytes.
func (c *Cache) Bytes() int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.store.Bytes()
}

// PathVariants returns the number of stored variants on path.
func (c *Cache) PathVariants(path string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.store.PathEntries(path))
}

// SnapshotEntry describes a stored variant for deterministic inspection.
type SnapshotEntry struct {
	Path     string
	Owner    string
	Names    []string
	Vals     []string
	Public   bool
	StoredAt int64
	ExpireAt int64
	Size     int64
	Seq      int64
}

// Entries returns a deterministic (Seq-ordered) snapshot of all variants.
func (c *Cache) Entries() []SnapshotEntry {
	c.mu.Lock()
	es := c.store.AllEntries()
	c.mu.Unlock()
	out := make([]SnapshotEntry, len(es))
	for i, e := range es {
		out[i] = SnapshotEntry{
			Path: e.Path, Owner: e.ID.Owner, Names: append([]string(nil), e.ID.Names...),
			Vals: append([]string(nil), e.ID.Vals...), Public: e.Public,
			StoredAt: e.StoredAt, ExpireAt: e.ExpireAt, Size: e.Size, Seq: e.Seq,
		}
	}
	return out
}

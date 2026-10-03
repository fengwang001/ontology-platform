package cache

// Naive, direct implementation of the spec rules, written independently of
// the production cache. The randomized differential test replays identical
// operation sequences on both and compares every observable outcome.

import "ontology/key"

type modelEntry struct {
	path     string
	id       key.Identity
	status   int
	body     []byte
	size     int64
	storedAt int64
	ttl      int64
	seq      uint64
}

type modelCache struct {
	cap      int64
	v        int
	policies []policy
	maxNow   int64
	seq      uint64
	bytes    int64
	fetches  int64
	hits     int64
	misses   int64
	bypasses int64
	inval    int64
	evicts   []EvictionEvent
	variants map[string]map[string]*modelEntry // path -> encoded identity
}

func newModel(capBytes int64, v int) *modelCache {
	return &modelCache{
		cap:      capBytes,
		v:        v,
		variants: make(map[string]map[string]*modelEntry),
	}
}

func (m *modelCache) addPolicy(prefix, scope string) bool {
	if len(prefix) == 0 || prefix[0] != '/' || scope == "" {
		return false
	}
	m.policies = append(m.policies, policy{prefix: prefix, scope: scope})
	return true
}

type modelOutcome struct {
	status int
	body   []byte
	source Source
	err    error
}

func (m *modelCache) requiredScope(path string) string {
	bestLen := -1
	scope := ""
	for _, p := range m.policies {
		if modelSegmentMatch(path, p.prefix) && len(p.prefix) > bestLen {
			bestLen = len(p.prefix)
			scope = p.scope
		}
	}
	return scope
}

func modelSegmentMatch(path, prefix string) bool {
	if len(path) < len(prefix) || path[:len(prefix)] != prefix {
		return false
	}
	return len(path) == len(prefix) || path[len(prefix)] == '/'
}

func (m *modelCache) get(req Request, now int64, fetch FetchFunc) modelOutcome {
	if err := validateRequest(req); err != nil {
		return modelOutcome{err: err}
	}
	if now < 0 || now > 1e15 {
		return modelOutcome{err: errInvalidTime}
	}
	if now < m.maxNow {
		return modelOutcome{err: errClockSkew}
	}
	if need := m.requiredScope(req.Path); need != "" && !contains(req.Scopes, need) {
		return modelOutcome{err: errForbidden}
	}
	m.maxNow = now

	if req.Method != "GET" {
		res, err := fetch(req)
		m.fetches++
		if err != nil {
			return modelOutcome{err: err}
		}
		if res == nil || res.TTL < 0 || res.Size < 0 {
			return modelOutcome{err: errFetchInvalid}
		}
		m.bypasses++
		if res.Status >= 200 && res.Status <= 399 {
			m.invalidatePath(req.Path)
		}
		return modelOutcome{status: res.Status, body: res.Body, source: Bypass}
	}

	if e := m.findHit(req.Path, req.Subject, req.Headers, now); e != nil {
		m.hits++
		return modelOutcome{status: e.status, body: e.body, source: Hit}
	}

	res, err := fetch(req)
	m.fetches++
	if err != nil {
		return modelOutcome{err: err}
	}
	if res == nil || res.TTL < 0 || res.Size < 0 ||
		(res.Vis != Public && res.Vis != Private && res.Vis != NoStore) {
		return modelOutcome{err: errFetchInvalid}
	}
	m.misses++
	m.maybeStore(req, res, now)
	return modelOutcome{status: res.Status, body: res.Body, source: Miss}
}

func (m *modelCache) invalidatePath(path string) {
	for _, e := range m.variants[path] {
		m.bytes -= e.size
		m.inval++
	}
	delete(m.variants, path)
}

func (m *modelCache) findHit(path, subject string, head map[string]string, now int64) *modelEntry {
	var bestPriv, bestPub *modelEntry
	for _, e := range m.variants[path] {
		if !(now < e.storedAt+e.ttl) {
			continue
		}
		if !key.VaryMatches(e.id, head) {
			continue
		}
		if e.id.Owner != key.PublicScope {
			if subject == "" || string(e.id.Owner) != subject {
				continue
			}
			if later(e, bestPriv) {
				bestPriv = e
			}
		} else if later(e, bestPub) {
			bestPub = e
		}
	}
	if bestPriv != nil {
		return bestPriv
	}
	return bestPub
}

func later(a, b *modelEntry) bool {
	return b == nil || a.storedAt > b.storedAt ||
		(a.storedAt == b.storedAt && a.seq > b.seq)
}

func (m *modelCache) maybeStore(req Request, res *FetchResult, now int64) {
	if res.Status < 200 || res.Status > 299 || res.TTL <= 0 || res.Size > m.cap {
		return
	}
	hasStar := false
	for _, name := range res.Vary {
		if name == "*" {
			hasStar = true
		}
	}
	if hasStar {
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
	enc := id.Encode()
	variants := m.variants[req.Path]
	if variants == nil {
		variants = map[string]*modelEntry{}
		m.variants[req.Path] = variants
	}
	if old := variants[enc]; old != nil {
		m.bytes -= old.size
		delete(variants, enc)
	}

	for len(variants) >= m.v {
		victim := modelEarliestInPath(variants)
		m.bytes -= victim.size
		delete(variants, victim.id.Encode())
		if len(variants) == 0 {
			delete(m.variants, req.Path)
		}
		m.evicts = append(m.evicts, EvictionEvent{
			Path: req.Path, Seq: victim.seq, Size: victim.size, Why: "path-limit",
		})
	}

	for m.bytes+res.Size > m.cap {
		victim := modelGlobalExpireFirst(m.variants)
		m.bytes -= victim.size
		delete(m.variants[victim.path], victim.id.Encode())
		if len(m.variants[victim.path]) == 0 {
			delete(m.variants, victim.path)
		}
		m.evicts = append(m.evicts, EvictionEvent{
			Path: victim.path, Seq: victim.seq, Size: victim.size, Why: "capacity",
		})
	}

	m.seq++
	incoming := &modelEntry{
		path: req.Path, id: id, status: res.Status, body: res.Body, size: res.Size,
		storedAt: now, ttl: res.TTL, seq: m.seq,
	}
	variants = m.variants[req.Path]
	if variants == nil {
		variants = map[string]*modelEntry{}
		m.variants[req.Path] = variants
	}
	variants[enc] = incoming
	m.bytes += res.Size
}

func modelEarliestInPath(variants map[string]*modelEntry) *modelEntry {
	var victim *modelEntry
	for _, e := range variants {
		if victim == nil || e.storedAt < victim.storedAt ||
			(e.storedAt == victim.storedAt && e.seq < victim.seq) {
			victim = e
		}
	}
	return victim
}

func modelGlobalExpireFirst(paths map[string]map[string]*modelEntry) *modelEntry {
	var victim *modelEntry
	for _, variants := range paths {
		for _, e := range variants {
			if victim == nil || e.storedAt+e.ttl < victim.storedAt+victim.ttl ||
				(e.storedAt+e.ttl == victim.storedAt+victim.ttl && e.seq < victim.seq) {
				victim = e
			}
		}
	}
	return victim
}

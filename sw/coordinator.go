package sw

import (
	"crypto/sha256"
	"encoding/hex"
	"net/url"
	"strings"
	"sync"
	"time"
)

type Config struct {
	MinUpdateInterval time.Duration
}

type Source int

const (
	SourceNetwork Source = iota
	SourceCache
)

type Answer struct {
	Source Source
	Digest string
}

type InstallOpts struct {
	SkipWaiting     bool
	ClaimClients    bool
	InheritManifest bool
}

type Coordinator struct {
	mu                 sync.Mutex
	cfg                Config
	lastTime           time.Time
	hasTime            bool
	regs               map[string]*Registration
	trie               *scopeTrie
	versions           map[uint64]*Version
	clients            map[string]*Client
	nextVersionID      uint64
	lastMatchSteps     int
	lastManifestProbes int
}

func NewCoordinator(cfg Config) *Coordinator {
	return &Coordinator{
		cfg:      cfg,
		regs:     make(map[string]*Registration),
		trie:     newScopeTrie(),
		versions: make(map[uint64]*Version),
		clients:  make(map[string]*Client),
	}
}

func digestOf(content string) string {
	sum := sha256.Sum256([]byte(content))
	return hex.EncodeToString(sum[:])
}

func pathOf(raw string) string {
	if strings.Contains(raw, "://") {
		if u, err := url.Parse(raw); err == nil && u.Path != "" {
			return u.Path
		}
	}
	return raw
}

func validScope(scope string) bool {
	return scope != "" && strings.HasSuffix(scope, "/")
}

func (c *Coordinator) checkClock(now time.Time) error {
	if c.hasTime && now.Before(c.lastTime) {
		return ErrClockRollback
	}
	return nil
}

func (c *Coordinator) advanceClock(now time.Time) {
	c.lastTime = now
	c.hasTime = true
}

func (c *Coordinator) findVersion(r *Registration, id uint64) *Version {
	v := c.versions[id]
	if v == nil || v.reg != r {
		return nil
	}
	return v
}

func (c *Coordinator) checkUpdateLocked(r *Registration, content string, now time.Time, bypassInterval bool) (bool, error) {
	if r.pendingRemoval {
		return false, ErrStateNotAllowed
	}
	if !bypassInterval && r.hasCheck && now.Sub(r.lastCheck) < c.cfg.MinUpdateInterval {
		return false, ErrTooFrequent
	}
	digest := digestOf(content)
	r.lastCheck = now
	r.hasCheck = true
	current := r.active
	if current == nil {
		current = r.waiting
	}
	if current == nil {
		current = r.installing
	}
	if current != nil && current.digest == digest {
		return false, nil
	}
	c.nextVersionID++
	v := &Version{
		id:       c.nextVersionID,
		state:    StateInstalling,
		digest:   digest,
		manifest: make(map[string]string),
		clients:  make(map[string]struct{}),
		reg:      r,
	}
	if r.installing != nil {
		r.installing.state = StateRedundant
	}
	r.installing = v
	r.versions = append(r.versions, v)
	c.versions[v.id] = v
	return true, nil
}

func (c *Coordinator) takeoverLocked(r *Registration) {
	w := r.waiting
	if w == nil {
		return
	}
	a := r.active
	r.waiting = nil
	if a != nil {
		a.state = StateRedundant
		if w.inheritManifest {
			for u, d := range a.manifest {
				if _, ok := w.manifest[u]; !ok {
					w.manifest[u] = d
				}
			}
		}
	}
	w.state = StateActive
	r.active = w
	if w.claimClients {
		for _, v := range r.versions {
			if v == w {
				continue
			}
			for id := range v.clients {
				delete(v.clients, id)
				w.clients[id] = struct{}{}
				if cl := c.clients[id]; cl != nil {
					cl.controlledBy = w
				}
			}
		}
	}
}

func (c *Coordinator) releaseLocked(cl *Client) {
	v := cl.controlledBy
	if v == nil {
		return
	}
	delete(v.clients, cl.id)
	v.reg.controlled--
	cl.controlledBy = nil
	r := v.reg
	if r.active != nil && len(r.active.clients) == 0 && r.waiting != nil {
		c.takeoverLocked(r)
	}
	if r.pendingRemoval && r.controlled == 0 {
		c.finalizeLocked(r)
	}
}

func (c *Coordinator) finalizeLocked(r *Registration) {
	for _, v := range r.versions {
		v.state = StateRedundant
	}
	r.installing = nil
	r.waiting = nil
	r.active = nil
	delete(c.regs, r.scope)
	c.trie.remove(r.scope)
}

func (c *Coordinator) Register(scope, scriptURL, content string, now time.Time) (*Registration, error) {
	if !validScope(scope) || scriptURL == "" {
		return nil, ErrInvalidArgument
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.checkClock(now); err != nil {
		return nil, err
	}
	r := c.regs[scope]
	if r != nil {
		r.pendingRemoval = false
		if r.scriptURL == scriptURL {
			c.advanceClock(now)
			return r, nil
		}
		r.scriptURL = scriptURL
		if _, err := c.checkUpdateLocked(r, content, now, true); err != nil {
			return nil, err
		}
		c.advanceClock(now)
		return r, nil
	}
	r = &Registration{scope: scope, scriptURL: scriptURL}
	c.regs[scope] = r
	c.trie.insert(scope, r)
	c.advanceClock(now)
	return r, nil
}

func (c *Coordinator) CheckUpdate(scope, content string, now time.Time) (bool, error) {
	if !validScope(scope) {
		return false, ErrInvalidArgument
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.checkClock(now); err != nil {
		return false, err
	}
	r := c.regs[scope]
	if r == nil {
		return false, ErrRegistrationNotFound
	}
	created, err := c.checkUpdateLocked(r, content, now, false)
	if err != nil {
		return false, err
	}
	c.advanceClock(now)
	return created, nil
}

func (c *Coordinator) InstallSucceeded(scope string, versionID uint64, opts InstallOpts, now time.Time) error {
	if !validScope(scope) {
		return ErrInvalidArgument
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.checkClock(now); err != nil {
		return err
	}
	r := c.regs[scope]
	if r == nil {
		return ErrRegistrationNotFound
	}
	v := c.findVersion(r, versionID)
	if v == nil {
		return ErrVersionNotFound
	}
	if v.state != StateInstalling {
		return ErrStateNotAllowed
	}
	v.skipWaiting = v.skipWaiting || opts.SkipWaiting
	v.claimClients = opts.ClaimClients
	v.inheritManifest = opts.InheritManifest
	r.installing = nil
	if r.waiting != nil {
		r.waiting.state = StateRedundant
	}
	r.waiting = v
	v.state = StateWaiting
	if v.skipWaiting || r.active == nil || len(r.active.clients) == 0 {
		c.takeoverLocked(r)
	}
	c.advanceClock(now)
	return nil
}

func (c *Coordinator) InstallFailed(scope string, versionID uint64, now time.Time) error {
	if !validScope(scope) {
		return ErrInvalidArgument
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.checkClock(now); err != nil {
		return err
	}
	r := c.regs[scope]
	if r == nil {
		return ErrRegistrationNotFound
	}
	v := c.findVersion(r, versionID)
	if v == nil {
		return ErrVersionNotFound
	}
	if v.state != StateInstalling {
		return ErrStateNotAllowed
	}
	v.state = StateRedundant
	r.installing = nil
	c.advanceClock(now)
	return nil
}

func (c *Coordinator) SkipWaiting(scope string, versionID uint64, now time.Time) error {
	if !validScope(scope) {
		return ErrInvalidArgument
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.checkClock(now); err != nil {
		return err
	}
	r := c.regs[scope]
	if r == nil {
		return ErrRegistrationNotFound
	}
	v := c.findVersion(r, versionID)
	if v == nil {
		return ErrVersionNotFound
	}
	switch v.state {
	case StateInstalling:
		v.skipWaiting = true
	case StateWaiting:
		c.takeoverLocked(r)
	case StateActive:
	default:
		return ErrStateNotAllowed
	}
	c.advanceClock(now)
	return nil
}

func (c *Coordinator) PutManifest(scope string, versionID uint64, entryURL, digest string, now time.Time) error {
	if !validScope(scope) || entryURL == "" {
		return ErrInvalidArgument
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.checkClock(now); err != nil {
		return err
	}
	r := c.regs[scope]
	if r == nil {
		return ErrRegistrationNotFound
	}
	v := c.findVersion(r, versionID)
	if v == nil {
		return ErrVersionNotFound
	}
	if v.state == StateRedundant {
		return ErrStateNotAllowed
	}
	v.manifest[entryURL] = digest
	c.advanceClock(now)
	return nil
}

func (c *Coordinator) Navigate(clientID, rawURL string, now time.Time) error {
	if clientID == "" || rawURL == "" {
		return ErrInvalidArgument
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.checkClock(now); err != nil {
		return err
	}
	cl := c.clients[clientID]
	if cl == nil {
		cl = &Client{id: clientID}
		c.clients[clientID] = cl
	}
	c.releaseLocked(cl)
	path := pathOf(rawURL)
	reg, steps := c.trie.match(path)
	c.lastMatchSteps = steps
	if reg != nil && reg.active != nil {
		reg.active.clients[clientID] = struct{}{}
		reg.controlled++
		cl.controlledBy = reg.active
	}
	c.advanceClock(now)
	return nil
}

func (c *Coordinator) NavigateAway(clientID string, now time.Time) error {
	if clientID == "" {
		return ErrInvalidArgument
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.checkClock(now); err != nil {
		return err
	}
	if cl := c.clients[clientID]; cl != nil {
		c.releaseLocked(cl)
	}
	c.advanceClock(now)
	return nil
}

func (c *Coordinator) Unregister(scope string, now time.Time) error {
	if !validScope(scope) {
		return ErrInvalidArgument
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.checkClock(now); err != nil {
		return err
	}
	r := c.regs[scope]
	if r == nil {
		return ErrRegistrationNotFound
	}
	if r.pendingRemoval {
		return ErrStateNotAllowed
	}
	r.pendingRemoval = true
	if r.controlled == 0 {
		c.finalizeLocked(r)
	}
	c.advanceClock(now)
	return nil
}

func (c *Coordinator) Request(clientID, reqURL string) (Answer, error) {
	if clientID == "" {
		return Answer{Source: SourceNetwork}, ErrInvalidArgument
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.lastManifestProbes = 0
	cl := c.clients[clientID]
	if cl == nil || cl.controlledBy == nil {
		return Answer{Source: SourceNetwork}, nil
	}
	v := cl.controlledBy
	if v.state != StateActive {
		return Answer{Source: SourceNetwork}, nil
	}
	c.lastManifestProbes = 1
	if digest, ok := v.manifest[reqURL]; ok {
		return Answer{Source: SourceCache, Digest: digest}, nil
	}
	return Answer{Source: SourceNetwork}, nil
}

func (c *Coordinator) LastMatchSteps() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.lastMatchSteps
}

func (c *Coordinator) LastManifestProbes() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.lastManifestProbes
}

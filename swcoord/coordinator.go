package swcoord

import (
	"fmt"
	"strings"
	"sync"
	"time"
)

// Options configures a Coordinator.
type Options struct {
	// MinUpdateInterval is the minimum spacing between two successful
	// update checks on the same registration. Checks triggered by a
	// script URL change ignore it.
	MinUpdateInterval time.Duration
	// Now supplies the clock; defaults to time.Now. A time value
	// earlier than the last successful operation rejects the
	// operation with ErrClockRollback.
	Now func() time.Time
	// ScriptDigest returns the content digest of a script URL.
	ScriptDigest func(scriptURL string) (string, error)
}

// Coordinator serializes all operations behind one mutex, so any
// concurrent use is equivalent to some serial order.
type Coordinator struct {
	mu            sync.Mutex
	minInterval   time.Duration
	now           func() time.Time
	scriptDigest  func(string) (string, error)
	lastTime      time.Time
	byScope       map[string]*Registration
	trie          *scopeTrie
	versions      map[uint64]*Version
	clients       map[string]*Client
	nextVersionID uint64
}

func New(opts Options) *Coordinator {
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	digest := opts.ScriptDigest
	if digest == nil {
		digest = func(string) (string, error) {
			return "", fmt.Errorf("swcoord: no script digest source configured")
		}
	}
	return &Coordinator{
		minInterval:  opts.MinUpdateInterval,
		now:          now,
		scriptDigest: digest,
		byScope:      make(map[string]*Registration),
		trie:         newScopeTrie(),
		versions:     make(map[uint64]*Version),
		clients:      make(map[string]*Client),
	}
}

// checkClock rejects the operation when the clock moved backwards.
// The returned time becomes lastTime only if the operation succeeds;
// rejected operations never touch the clock.
func (c *Coordinator) checkClock() (time.Time, error) {
	now := c.now()
	if now.Before(c.lastTime) {
		return now, fmt.Errorf("%w: now=%s last=%s", ErrClockRollback, now, c.lastTime)
	}
	return now, nil
}

func validScope(scope string) bool {
	return scope != "" && strings.HasSuffix(scope, "/")
}

// Register registers scope for scriptURL. It returns existed=true when
// a registration already lived at scope: same script URL is a no-op, a
// different one triggers an update check (interval-free) instead of
// creating a new registration. A pending-removal registration is
// revived with its version relations untouched.
func (c *Coordinator) Register(scope, scriptURL string) (bool, error) {
	if !validScope(scope) {
		return false, invalidArgf("scope %q must end with '/'", scope)
	}
	if scriptURL == "" {
		return false, invalidArgf("script URL must not be empty")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	now, err := c.checkClock()
	if err != nil {
		return false, err
	}
	reg, existed := c.byScope[scope]
	if existed {
		if reg.scriptURL == scriptURL {
			reg.pendingRemoval = false
			c.lastTime = now
			return true, nil
		}
		// Fetch before mutating so a source failure changes nothing.
		digest, err := c.scriptDigest(scriptURL)
		if err != nil {
			return true, err
		}
		reg.pendingRemoval = false
		reg.scriptURL = scriptURL
		c.applyUpdateCheck(reg, digest, now)
		c.lastTime = now
		return true, nil
	}
	reg = newRegistration(scope, scriptURL)
	c.byScope[scope] = reg
	c.trie.insert(scope, reg)
	c.lastTime = now
	return false, nil
}

// Unregister marks the registration pending removal. It stops
// accepting new clients and update checks; controlled clients stay
// controlled until they navigate away, and the registration with all
// its versions becomes redundant once the last client leaves.
func (c *Coordinator) Unregister(scope string) error {
	if !validScope(scope) {
		return invalidArgf("scope %q must end with '/'", scope)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	now, err := c.checkClock()
	if err != nil {
		return err
	}
	reg, ok := c.byScope[scope]
	if !ok {
		return fmt.Errorf("%w: scope %q", ErrRegistrationNotFound, scope)
	}
	if reg.pendingRemoval {
		return stateNotAllowedf("scope %q already pending removal", scope)
	}
	reg.pendingRemoval = true
	if len(reg.clients) == 0 {
		c.finalizeRemoval(reg)
	}
	c.lastTime = now
	return nil
}

// CheckUpdate fetches the script digest and compares it with the
// active (else waiting, else installing) version. An equal digest is
// no update; a different one spawns a new installing version,
// redundanting any previous installing version. Returns the new
// version ID and whether a version was created.
func (c *Coordinator) CheckUpdate(scope string) (uint64, bool, error) {
	if !validScope(scope) {
		return 0, false, invalidArgf("scope %q must end with '/'", scope)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	now, err := c.checkClock()
	if err != nil {
		return 0, false, err
	}
	reg, ok := c.byScope[scope]
	if !ok {
		return 0, false, fmt.Errorf("%w: scope %q", ErrRegistrationNotFound, scope)
	}
	if reg.pendingRemoval {
		return 0, false, stateNotAllowedf("scope %q pending removal", scope)
	}
	if reg.hasChecked && now.Sub(reg.lastCheck) < c.minInterval {
		return 0, false, fmt.Errorf("%w: scope %q last check %s ago",
			ErrTooFrequent, scope, now.Sub(reg.lastCheck))
	}
	digest, err := c.scriptDigest(reg.scriptURL)
	if err != nil {
		return 0, false, err
	}
	id, created := c.applyUpdateCheck(reg, digest, now)
	c.lastTime = now
	return id, created, nil
}

// applyUpdateCheck records a successful check and spawns a version on
// digest change. Caller holds the lock.
func (c *Coordinator) applyUpdateCheck(reg *Registration, digest string, now time.Time) (uint64, bool) {
	reg.lastCheck = now
	reg.hasChecked = true
	if cur := reg.comparisonVersion(); cur != nil && cur.digest == digest {
		return 0, false
	}
	c.nextVersionID++
	v := &Version{
		id:        c.nextVersionID,
		scriptURL: reg.scriptURL,
		digest:    digest,
		state:     StateInstalling,
		manifest:  make(map[string]string),
		reg:       reg,
	}
	if reg.installing != nil {
		reg.installing.state = StateRedundant
	}
	reg.installing = v
	c.versions[v.id] = v
	return v.id, true
}

// InstallSuccess moves an installing version to the waiting slot,
// redundanting any previous waiting version, and promotes it if the
// active slot is empty, client-free, or skipped by declaration.
func (c *Coordinator) InstallSuccess(versionID uint64, manifest map[string]string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	now, err := c.checkClock()
	if err != nil {
		return err
	}
	v, ok := c.versions[versionID]
	if !ok {
		return fmt.Errorf("%w: id %d", ErrVersionNotFound, versionID)
	}
	if v.state != StateInstalling {
		return stateNotAllowedf("version %d is %s", versionID, v.state)
	}
	v.manifest = make(map[string]string, len(manifest))
	for url, digest := range manifest {
		v.manifest[url] = digest
	}
	v.state = StateWaiting
	reg := v.reg
	if reg.installing == v {
		reg.installing = nil
	}
	if reg.waiting != nil {
		reg.waiting.state = StateRedundant
	}
	reg.waiting = v
	reg.maybePromote()
	c.lastTime = now
	return nil
}

// InstallFail redundants an installing version without touching the
// waiting or active slots.
func (c *Coordinator) InstallFail(versionID uint64) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	now, err := c.checkClock()
	if err != nil {
		return err
	}
	v, ok := c.versions[versionID]
	if !ok {
		return fmt.Errorf("%w: id %d", ErrVersionNotFound, versionID)
	}
	if v.state != StateInstalling {
		return stateNotAllowedf("version %d is %s", versionID, v.state)
	}
	v.state = StateRedundant
	if v.reg.installing == v {
		v.reg.installing = nil
	}
	c.lastTime = now
	return nil
}

// Declare sets the takeover options of an installing or waiting
// version. If skip-waiting is declared on a waiting version, the
// takeover happens immediately.
func (c *Coordinator) Declare(versionID uint64, opts TakeoverOptions) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	now, err := c.checkClock()
	if err != nil {
		return err
	}
	v, ok := c.versions[versionID]
	if !ok {
		return fmt.Errorf("%w: id %d", ErrVersionNotFound, versionID)
	}
	if v.state != StateInstalling && v.state != StateWaiting {
		return stateNotAllowedf("version %d is %s", versionID, v.state)
	}
	v.opts = opts
	if v.state == StateWaiting {
		v.reg.maybePromote()
	}
	c.lastTime = now
	return nil
}

// SetManifestEntry adds or replaces one manifest entry. Redundant
// versions reject it, which keeps a post-takeover inheritance snapshot
// immune to later writes on the old manifest.
func (c *Coordinator) SetManifestEntry(versionID uint64, url, digest string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	now, err := c.checkClock()
	if err != nil {
		return err
	}
	v, ok := c.versions[versionID]
	if !ok {
		return fmt.Errorf("%w: id %d", ErrVersionNotFound, versionID)
	}
	if v.state == StateRedundant {
		return stateNotAllowedf("version %d is redundant", versionID)
	}
	if v.manifest == nil {
		v.manifest = make(map[string]string)
	}
	v.manifest[url] = digest
	c.lastTime = now
	return nil
}

// Navigate re-computes the client's ownership: any previous control is
// released, then the longest-prefix matching registration controls the
// client through its active version. An empty url means the client
// navigated away from every controlled page.
func (c *Coordinator) Navigate(clientID, url string) error {
	if clientID == "" {
		return invalidArgf("client ID must not be empty")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	now, err := c.checkClock()
	if err != nil {
		return err
	}
	cl, ok := c.clients[clientID]
	if !ok {
		cl = &Client{id: clientID}
		c.clients[clientID] = cl
	}
	c.releaseClient(cl)
	if url != "" {
		if reg, _ := c.trie.match(url); reg != nil && !reg.pendingRemoval && reg.active != nil {
			cl.reg = reg
			cl.version = reg.active
			reg.clients[clientID] = cl
			reg.active.clientCount++
		}
	}
	c.lastTime = now
	return nil
}

// Unload ends the client's page lifetime, releasing its control.
func (c *Coordinator) Unload(clientID string) error {
	return c.Navigate(clientID, "")
}

// Fetch answers a resource request of a client from its controlling
// version's manifest. Uncontrolled clients, redundant versions and
// cache misses all yield the network marker.
func (c *Coordinator) Fetch(clientID, url string) (FetchResult, error) {
	if clientID == "" {
		return FetchResult{}, invalidArgf("client ID must not be empty")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, err := c.checkClock(); err != nil {
		return FetchResult{}, err
	}
	cl, ok := c.clients[clientID]
	if !ok || cl.version == nil || cl.version.state == StateRedundant {
		return FetchResult{Network: true}, nil
	}
	if digest, hit := cl.version.manifest[url]; hit {
		return FetchResult{Digest: digest}, nil
	}
	return FetchResult{Network: true}, nil
}

// finalizeRemoval redundants every version of a pending-removal
// registration and drops the registration from all indexes.
func (c *Coordinator) finalizeRemoval(reg *Registration) {
	reg.redundantAll()
	delete(c.byScope, reg.scope)
	c.trie.remove(reg.scope)
}

// RegistrationInfo is a point-in-time view of a registration.
type RegistrationInfo struct {
	Scope          string
	ScriptURL      string
	PendingRemoval bool
	Installing     uint64
	Waiting        uint64
	Active         uint64
	ClientCount    int
	LastCheck      time.Time
	HasChecked     bool
}

func (c *Coordinator) RegistrationInfo(scope string) (RegistrationInfo, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	reg, ok := c.byScope[scope]
	if !ok {
		return RegistrationInfo{}, false
	}
	info := RegistrationInfo{
		Scope:          reg.scope,
		ScriptURL:      reg.scriptURL,
		PendingRemoval: reg.pendingRemoval,
		ClientCount:    len(reg.clients),
		LastCheck:      reg.lastCheck,
		HasChecked:     reg.hasChecked,
	}
	if reg.installing != nil {
		info.Installing = reg.installing.id
	}
	if reg.waiting != nil {
		info.Waiting = reg.waiting.id
	}
	if reg.active != nil {
		info.Active = reg.active.id
	}
	return info, true
}

// VersionInfo is a point-in-time view of a version.
type VersionInfo struct {
	ID          uint64
	State       VersionState
	ScriptURL   string
	Digest      string
	ClientCount int
	Options     TakeoverOptions
	Manifest    map[string]string
}

func (c *Coordinator) VersionInfo(id uint64) (VersionInfo, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	v, ok := c.versions[id]
	if !ok {
		return VersionInfo{}, false
	}
	manifest := make(map[string]string, len(v.manifest))
	for url, digest := range v.manifest {
		manifest[url] = digest
	}
	return VersionInfo{
		ID:          v.id,
		State:       v.state,
		ScriptURL:   v.scriptURL,
		Digest:      v.digest,
		ClientCount: v.clientCount,
		Options:     v.opts,
		Manifest:    manifest,
	}, true
}

// ClientInfo reports which version controls a client, if any.
func (c *Coordinator) ClientInfo(clientID string) (uint64, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	cl, ok := c.clients[clientID]
	if !ok || cl.version == nil {
		return 0, false
	}
	return cl.version.id, true
}

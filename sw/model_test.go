package sw

import (
	"errors"
	"fmt"
	"math/rand"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"
)

// mVersion/mReg/model are an independent, deliberately naive implementation
// of the same spec: linear scans, per-version state fields, no slots.
type mVersion struct {
	id              uint64
	state           VersionState
	digest          string
	manifest        map[string]string
	clients         map[string]bool
	skipWaiting     bool
	claimClients    bool
	inheritManifest bool
	reg             *mReg
}

type mReg struct {
	scope     string
	script    string
	pending   bool
	versions  []*mVersion
	lastCheck time.Time
	hasCheck  bool
}

type model struct {
	minInterval time.Duration
	regs        []*mReg
	clients     map[string]*mVersion
	last        time.Time
	hasLast     bool
	nextID      uint64
}

func newModel(minInterval time.Duration) *model {
	return &model{minInterval: minInterval, clients: make(map[string]*mVersion)}
}

func (m *model) findReg(scope string) *mReg {
	for _, r := range m.regs {
		if r.scope == scope {
			return r
		}
	}
	return nil
}

func (m *model) findVersion(r *mReg, id uint64) *mVersion {
	for _, v := range r.versions {
		if v.id == id {
			return v
		}
	}
	return nil
}

func slotOf(r *mReg, s VersionState) *mVersion {
	for _, v := range r.versions {
		if v.state == s {
			return v
		}
	}
	return nil
}

func (m *model) checkClock(now time.Time) error {
	if m.hasLast && now.Before(m.last) {
		return ErrClockRollback
	}
	return nil
}

func (m *model) totalClients(r *mReg) int {
	n := 0
	for _, v := range r.versions {
		n += len(v.clients)
	}
	return n
}

func (m *model) checkLocked(r *mReg, content string, now time.Time, bypass bool) (bool, error) {
	if r.pending {
		return false, ErrStateNotAllowed
	}
	if !bypass && r.hasCheck && now.Sub(r.lastCheck) < m.minInterval {
		return false, ErrTooFrequent
	}
	digest := digestOf(content)
	r.lastCheck = now
	r.hasCheck = true
	current := slotOf(r, StateActive)
	if current == nil {
		current = slotOf(r, StateWaiting)
	}
	if current == nil {
		current = slotOf(r, StateInstalling)
	}
	if current != nil && current.digest == digest {
		return false, nil
	}
	m.nextID++
	v := &mVersion{
		id:       m.nextID,
		state:    StateInstalling,
		digest:   digest,
		manifest: make(map[string]string),
		clients:  make(map[string]bool),
		reg:      r,
	}
	if old := slotOf(r, StateInstalling); old != nil {
		old.state = StateRedundant
	}
	r.versions = append(r.versions, v)
	return true, nil
}

func (m *model) takeover(r *mReg) {
	w := slotOf(r, StateWaiting)
	if w == nil {
		return
	}
	a := slotOf(r, StateActive)
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
	if w.claimClients {
		for _, v := range r.versions {
			if v == w {
				continue
			}
			for id := range v.clients {
				delete(v.clients, id)
				w.clients[id] = true
				m.clients[id] = w
			}
		}
	}
}

func (m *model) release(clientID string) {
	v := m.clients[clientID]
	if v == nil {
		return
	}
	delete(v.clients, clientID)
	delete(m.clients, clientID)
	r := v.reg
	if a := slotOf(r, StateActive); a != nil && len(a.clients) == 0 && slotOf(r, StateWaiting) != nil {
		m.takeover(r)
	}
	if r.pending && m.totalClients(r) == 0 {
		m.finalize(r)
	}
}

func (m *model) finalize(r *mReg) {
	for _, v := range r.versions {
		v.state = StateRedundant
	}
	out := m.regs[:0]
	for _, x := range m.regs {
		if x != r {
			out = append(out, x)
		}
	}
	m.regs = out
}

func (m *model) register(scope, script, content string, now time.Time) error {
	if !validScope(scope) || script == "" {
		return ErrInvalidArgument
	}
	if err := m.checkClock(now); err != nil {
		return err
	}
	r := m.findReg(scope)
	if r != nil {
		r.pending = false
		if r.script == script {
			m.last, m.hasLast = now, true
			return nil
		}
		r.script = script
		if _, err := m.checkLocked(r, content, now, true); err != nil {
			return err
		}
		m.last, m.hasLast = now, true
		return nil
	}
	m.regs = append(m.regs, &mReg{scope: scope, script: script})
	m.last, m.hasLast = now, true
	return nil
}

func (m *model) checkUpdate(scope, content string, now time.Time) (bool, error) {
	if !validScope(scope) {
		return false, ErrInvalidArgument
	}
	if err := m.checkClock(now); err != nil {
		return false, err
	}
	r := m.findReg(scope)
	if r == nil {
		return false, ErrRegistrationNotFound
	}
	created, err := m.checkLocked(r, content, now, false)
	if err != nil {
		return false, err
	}
	m.last, m.hasLast = now, true
	return created, nil
}

func (m *model) installSucceeded(scope string, id uint64, opts InstallOpts, now time.Time) error {
	if !validScope(scope) {
		return ErrInvalidArgument
	}
	if err := m.checkClock(now); err != nil {
		return err
	}
	r := m.findReg(scope)
	if r == nil {
		return ErrRegistrationNotFound
	}
	v := m.findVersion(r, id)
	if v == nil {
		return ErrVersionNotFound
	}
	if v.state != StateInstalling {
		return ErrStateNotAllowed
	}
	v.skipWaiting = v.skipWaiting || opts.SkipWaiting
	v.claimClients = opts.ClaimClients
	v.inheritManifest = opts.InheritManifest
	if old := slotOf(r, StateWaiting); old != nil {
		old.state = StateRedundant
	}
	v.state = StateWaiting
	a := slotOf(r, StateActive)
	if v.skipWaiting || a == nil || len(a.clients) == 0 {
		m.takeover(r)
	}
	m.last, m.hasLast = now, true
	return nil
}

func (m *model) installFailed(scope string, id uint64, now time.Time) error {
	if !validScope(scope) {
		return ErrInvalidArgument
	}
	if err := m.checkClock(now); err != nil {
		return err
	}
	r := m.findReg(scope)
	if r == nil {
		return ErrRegistrationNotFound
	}
	v := m.findVersion(r, id)
	if v == nil {
		return ErrVersionNotFound
	}
	if v.state != StateInstalling {
		return ErrStateNotAllowed
	}
	v.state = StateRedundant
	m.last, m.hasLast = now, true
	return nil
}

func (m *model) skipWaiting(scope string, id uint64, now time.Time) error {
	if !validScope(scope) {
		return ErrInvalidArgument
	}
	if err := m.checkClock(now); err != nil {
		return err
	}
	r := m.findReg(scope)
	if r == nil {
		return ErrRegistrationNotFound
	}
	v := m.findVersion(r, id)
	if v == nil {
		return ErrVersionNotFound
	}
	switch v.state {
	case StateInstalling:
		v.skipWaiting = true
	case StateWaiting:
		m.takeover(r)
	case StateActive:
	default:
		return ErrStateNotAllowed
	}
	m.last, m.hasLast = now, true
	return nil
}

func (m *model) putManifest(scope string, id uint64, url, digest string, now time.Time) error {
	if !validScope(scope) || url == "" {
		return ErrInvalidArgument
	}
	if err := m.checkClock(now); err != nil {
		return err
	}
	r := m.findReg(scope)
	if r == nil {
		return ErrRegistrationNotFound
	}
	v := m.findVersion(r, id)
	if v == nil {
		return ErrVersionNotFound
	}
	if v.state == StateRedundant {
		return ErrStateNotAllowed
	}
	v.manifest[url] = digest
	m.last, m.hasLast = now, true
	return nil
}

func (m *model) navigate(clientID, rawURL string, now time.Time) error {
	if clientID == "" || rawURL == "" {
		return ErrInvalidArgument
	}
	if err := m.checkClock(now); err != nil {
		return err
	}
	m.release(clientID)
	path := pathOf(rawURL)
	var best *mReg
	for _, r := range m.regs {
		if r.pending || !strings.HasPrefix(path, r.scope) {
			continue
		}
		if best == nil || len(r.scope) > len(best.scope) {
			best = r
		}
	}
	if best != nil {
		if a := slotOf(best, StateActive); a != nil {
			a.clients[clientID] = true
			m.clients[clientID] = a
		}
	}
	m.last, m.hasLast = now, true
	return nil
}

func (m *model) navigateAway(clientID string, now time.Time) error {
	if clientID == "" {
		return ErrInvalidArgument
	}
	if err := m.checkClock(now); err != nil {
		return err
	}
	m.release(clientID)
	m.last, m.hasLast = now, true
	return nil
}

func (m *model) unregister(scope string, now time.Time) error {
	if !validScope(scope) {
		return ErrInvalidArgument
	}
	if err := m.checkClock(now); err != nil {
		return err
	}
	r := m.findReg(scope)
	if r == nil {
		return ErrRegistrationNotFound
	}
	if r.pending {
		return ErrStateNotAllowed
	}
	r.pending = true
	if m.totalClients(r) == 0 {
		m.finalize(r)
	}
	m.last, m.hasLast = now, true
	return nil
}

func (m *model) request(clientID, url string) (Answer, error) {
	if clientID == "" {
		return Answer{Source: SourceNetwork}, ErrInvalidArgument
	}
	v := m.clients[clientID]
	if v == nil || v.state != StateActive {
		return Answer{Source: SourceNetwork}, nil
	}
	if d, ok := v.manifest[url]; ok {
		return Answer{Source: SourceCache, Digest: d}, nil
	}
	return Answer{Source: SourceNetwork}, nil
}

type verSnap struct {
	State    VersionState
	Digest   string
	Manifest map[string]string
	Clients  map[string]bool
}

type regSnap struct {
	Script     string
	Pending    bool
	Installing uint64
	Waiting    uint64
	Active     uint64
	Versions   map[uint64]verSnap
}

type snapshot struct {
	Regs    map[string]regSnap
	Clients map[string]uint64
}

func snapCoordinator(c *Coordinator) snapshot {
	c.mu.Lock()
	defer c.mu.Unlock()
	s := snapshot{Regs: map[string]regSnap{}, Clients: map[string]uint64{}}
	for scope, r := range c.regs {
		rs := regSnap{Script: r.scriptURL, Pending: r.pendingRemoval, Versions: map[uint64]verSnap{}}
		if r.installing != nil {
			rs.Installing = r.installing.id
		}
		if r.waiting != nil {
			rs.Waiting = r.waiting.id
		}
		if r.active != nil {
			rs.Active = r.active.id
		}
		for _, v := range r.versions {
			cl := map[string]bool{}
			for id := range v.clients {
				cl[id] = true
			}
			rs.Versions[v.id] = verSnap{State: v.state, Digest: v.digest, Manifest: v.Manifest(), Clients: cl}
		}
		s.Regs[scope] = rs
	}
	for id, cl := range c.clients {
		if cl.controlledBy != nil {
			s.Clients[id] = cl.controlledBy.id
		}
	}
	return s
}

func snapModel(m *model) snapshot {
	s := snapshot{Regs: map[string]regSnap{}, Clients: map[string]uint64{}}
	for _, r := range m.regs {
		rs := regSnap{Script: r.script, Pending: r.pending, Versions: map[uint64]verSnap{}}
		if v := slotOf(r, StateInstalling); v != nil {
			rs.Installing = v.id
		}
		if v := slotOf(r, StateWaiting); v != nil {
			rs.Waiting = v.id
		}
		if v := slotOf(r, StateActive); v != nil {
			rs.Active = v.id
		}
		for _, v := range r.versions {
			cl := map[string]bool{}
			for id := range v.clients {
				cl[id] = true
			}
			manifest := make(map[string]string, len(v.manifest))
			for k, d := range v.manifest {
				manifest[k] = d
			}
			rs.Versions[v.id] = verSnap{State: v.state, Digest: v.digest, Manifest: manifest, Clients: cl}
		}
		s.Regs[r.scope] = rs
	}
	for id, v := range m.clients {
		s.Clients[id] = v.id
	}
	return s
}

func checkModelInvariants(t *testing.T, m *model) {
	t.Helper()
	for _, r := range m.regs {
		counts := map[VersionState]int{}
		for _, v := range r.versions {
			counts[v.state]++
		}
		for _, s := range []VersionState{StateInstalling, StateWaiting, StateActive} {
			if counts[s] > 1 {
				t.Fatalf("model reg %q: %d versions in state %v", r.scope, counts[s], s)
			}
		}
	}
	for id, v := range m.clients {
		if v.state != StateActive && v.state != StateRedundant {
			t.Fatalf("model client %q controlled by version in state %v", id, v.state)
		}
	}
}

func TestModelDifferentialRandomOps(t *testing.T) {
	scopes := []string{"/", "/a/", "/a/b/", "/b/", "/app/", "/app/x/"}
	scripts := []string{"sw1.js", "sw2.js"}
	contents := []string{"k1", "k2", "k3", "k4"}
	clientIDs := []string{"c0", "c1", "c2", "c3", "c4", "c5"}
	urls := []string{"/a/b/p", "/a/p", "/b/p", "/app/x/p", "/app/p", "/other", "/"}
	manifestURLs := []string{"/r1", "/r2", "/a/b/p", "/app/p"}

	for seed := int64(1); seed <= 8; seed++ {
		t.Run(fmt.Sprintf("seed-%d", seed), func(t *testing.T) {
			rng := rand.New(rand.NewSource(seed))
			c := NewCoordinator(Config{MinUpdateInterval: 10 * time.Second})
			m := newModel(10 * time.Second)
			var clock int64 = 1000

			pick := func(pool []string) string { return pool[rng.Intn(len(pool))] }
			nextTime := func() time.Time {
				clock += rng.Int63n(4)
				if rng.Intn(20) == 0 {
					clock -= 1 + rng.Int63n(6)
				}
				return at(clock)
			}

			for i := 0; i < 1500; i++ {
				now := nextTime()
				var coordErr, modelErr error
				opName := ""
				switch rng.Intn(100) {
				case 0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11:
					scope, script, content := pick(scopes), pick(scripts), pick(contents)
					if rng.Intn(30) == 0 {
						scope = "no-trailing-slash"
					}
					if rng.Intn(30) == 0 {
						script = ""
					}
					opName = fmt.Sprintf("Register(%q,%q,%q)", scope, script, content)
					_, coordErr = c.Register(scope, script, content, now)
					modelErr = m.register(scope, script, content, now)
				case 12, 13, 14, 15, 16, 17, 18, 19, 20, 21, 22, 23, 24, 25, 26:
					scope, content := pick(scopes), pick(contents)
					opName = fmt.Sprintf("CheckUpdate(%q,%q)", scope, content)
					var coordCreated, modelCreated bool
					coordCreated, coordErr = c.CheckUpdate(scope, content, now)
					modelCreated, modelErr = m.checkUpdate(scope, content, now)
					if coordErr == nil && modelErr == nil && coordCreated != modelCreated {
						t.Fatalf("op %d %s: created mismatch coord=%v model=%v", i, opName, coordCreated, modelCreated)
					}
				case 27, 28, 29, 30, 31, 32, 33, 34, 35, 36:
					scope := pick(scopes)
					vid := uint64(1 + rng.Intn(int(c.nextVersionID)+2))
					opts := InstallOpts{SkipWaiting: rng.Intn(2) == 0, ClaimClients: rng.Intn(2) == 0, InheritManifest: rng.Intn(2) == 0}
					opName = fmt.Sprintf("InstallSucceeded(%q,v%d,%+v)", scope, vid, opts)
					coordErr = c.InstallSucceeded(scope, vid, opts, now)
					modelErr = m.installSucceeded(scope, vid, opts, now)
				case 37, 38, 39, 40, 41:
					scope := pick(scopes)
					vid := uint64(1 + rng.Intn(int(c.nextVersionID)+2))
					opName = fmt.Sprintf("InstallFailed(%q,v%d)", scope, vid)
					coordErr = c.InstallFailed(scope, vid, now)
					modelErr = m.installFailed(scope, vid, now)
				case 42, 43, 44, 45, 46, 47:
					scope := pick(scopes)
					vid := uint64(1 + rng.Intn(int(c.nextVersionID)+2))
					opName = fmt.Sprintf("SkipWaiting(%q,v%d)", scope, vid)
					coordErr = c.SkipWaiting(scope, vid, now)
					modelErr = m.skipWaiting(scope, vid, now)
				case 48, 49, 50, 51, 52, 53, 54, 55:
					scope := pick(scopes)
					vid := uint64(1 + rng.Intn(int(c.nextVersionID)+2))
					u, d := pick(manifestURLs), fmt.Sprintf("d%d", rng.Intn(4))
					opName = fmt.Sprintf("PutManifest(%q,v%d,%q,%q)", scope, vid, u, d)
					coordErr = c.PutManifest(scope, vid, u, d, now)
					modelErr = m.putManifest(scope, vid, u, d, now)
				case 56, 57, 58, 59, 60, 61, 62, 63, 64, 65, 66, 67, 68, 69, 70, 71, 72, 73:
					client, u := pick(clientIDs), pick(urls)
					opName = fmt.Sprintf("Navigate(%q,%q)", client, u)
					coordErr = c.Navigate(client, u, now)
					modelErr = m.navigate(client, u, now)
				case 74, 75, 76, 77, 78, 79:
					client := pick(clientIDs)
					opName = fmt.Sprintf("NavigateAway(%q)", client)
					coordErr = c.NavigateAway(client, now)
					modelErr = m.navigateAway(client, now)
				case 80, 81, 82, 83, 84:
					scope := pick(scopes)
					opName = fmt.Sprintf("Unregister(%q)", scope)
					coordErr = c.Unregister(scope, now)
					modelErr = m.unregister(scope, now)
				default:
					client, u := pick(clientIDs), pick(manifestURLs)
					opName = fmt.Sprintf("Request(%q,%q)", client, u)
					coordAns, coordErr := c.Request(client, u)
					modelAns, modelErr := m.request(client, u)
					if (coordErr == nil) != (modelErr == nil) || (coordErr == nil && coordAns != modelAns) {
						t.Fatalf("op %d %s: coord=(%+v,%v) model=(%+v,%v)", i, opName, coordAns, coordErr, modelAns, modelErr)
					}
					t.Logf("op %04d %s -> ans=%+v err=%v", i, opName, coordAns, coordErr)
					checkInvariants(t, c)
					checkModelInvariants(t, m)
					continue
				}
				if (coordErr == nil) != (modelErr == nil) {
					t.Fatalf("op %d %s: coord err=%v model err=%v", i, opName, coordErr, modelErr)
				}
				if coordErr != nil && !errorSameCategory(coordErr, modelErr) {
					t.Fatalf("op %d %s: coord err=%v model err=%v (category mismatch)", i, opName, coordErr, modelErr)
				}
				t.Logf("op %04d %s -> err=%v", i, opName, coordErr)
				checkInvariants(t, c)
				checkModelInvariants(t, m)
			}

			cs, ms := snapCoordinator(c), snapModel(m)
			if !reflect.DeepEqual(cs, ms) {
				t.Fatalf("final state divergence:\ncoord: %s\nmodel: %s", dumpSnap(cs), dumpSnap(ms))
			}
			t.Logf("final state matches model: %d registrations, %d controlled clients", len(cs.Regs), len(cs.Clients))
		})
	}
}

func errorSameCategory(a, b error) bool {
	for _, sentinel := range []error{
		ErrInvalidArgument, ErrClockRollback, ErrRegistrationNotFound,
		ErrVersionNotFound, ErrStateNotAllowed, ErrTooFrequent,
	} {
		if errors.Is(a, sentinel) || errors.Is(b, sentinel) {
			return errors.Is(a, sentinel) && errors.Is(b, sentinel)
		}
	}
	return a == b
}

func dumpSnap(s snapshot) string {
	var b strings.Builder
	scopes := make([]string, 0, len(s.Regs))
	for scope := range s.Regs {
		scopes = append(scopes, scope)
	}
	sort.Strings(scopes)
	for _, scope := range scopes {
		r := s.Regs[scope]
		fmt.Fprintf(&b, "reg %q script=%q pending=%v slots(i/w/a)=%d/%d/%d\n", scope, r.Script, r.Pending, r.Installing, r.Waiting, r.Active)
		ids := make([]int, 0, len(r.Versions))
		for id := range r.Versions {
			ids = append(ids, int(id))
		}
		sort.Ints(ids)
		for _, id := range ids {
			v := r.Versions[uint64(id)]
			fmt.Fprintf(&b, "  v%d state=%v clients=%v manifest=%v\n", id, v.State, v.Clients, v.Manifest)
		}
	}
	fmt.Fprintf(&b, "clients=%v\n", s.Clients)
	return b.String()
}

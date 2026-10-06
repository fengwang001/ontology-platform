package swcoord

// This file holds an independently written naive model of the
// coordinator semantics: linear scans instead of the scope trie,
// per-version client sets instead of counters, slots derived by
// scanning instead of cached pointers. Randomized tests replay
// identical operation sequences against both and demand identical
// outputs and identical observable state.
import (
	"errors"
	"fmt"
	"math/rand"
	"sort"
	"strings"
	"testing"
	"time"
)

type naiveVersion struct {
	id       uint64
	script   string
	digest   string
	state    VersionState
	opts     TakeoverOptions
	manifest map[string]string
	clients  map[string]bool
}

type naiveReg struct {
	scope    string
	script   string
	versions []*naiveVersion
	clients  map[string]bool
	pending  bool
	last     time.Time
	checked  bool
}

func (r *naiveReg) slot(state VersionState) *naiveVersion {
	var found *naiveVersion
	for _, v := range r.versions {
		if v.state == state {
			found = v
		}
	}
	return found
}

type naiveClient struct {
	id        string
	reg       *naiveReg
	versionID uint64
}

type naiveModel struct {
	regs     []*naiveReg
	versions map[uint64]*naiveVersion
	clients  map[string]*naiveClient
	min      time.Duration
	now      func() time.Time
	digest   func(string) (string, error)
	lastTime time.Time
	nextID   uint64
}

func newNaiveModel(min time.Duration, now func() time.Time, digest func(string) (string, error)) *naiveModel {
	return &naiveModel{
		versions: make(map[uint64]*naiveVersion),
		clients:  make(map[string]*naiveClient),
		min:      min,
		now:      now,
		digest:   digest,
	}
}

func (m *naiveModel) clock() (time.Time, error) {
	now := m.now()
	if now.Before(m.lastTime) {
		return now, fmt.Errorf("%w: rollback", ErrClockRollback)
	}
	return now, nil
}

func (m *naiveModel) findReg(scope string) *naiveReg {
	for _, r := range m.regs {
		if r.scope == scope {
			return r
		}
	}
	return nil
}

func (m *naiveModel) match(url string) *naiveReg {
	var best *naiveReg
	for _, r := range m.regs {
		if strings.HasPrefix(url, r.scope) {
			if best == nil || len(r.scope) > len(best.scope) {
				best = r
			}
		}
	}
	return best
}

func (m *naiveModel) takeover(reg *naiveReg) {
	next := reg.slot(StateWaiting)
	old := reg.slot(StateActive)
	if old != nil {
		if next.opts.InheritManifest {
			for url, digest := range old.manifest {
				if _, declared := next.manifest[url]; !declared {
					next.manifest[url] = digest
				}
			}
		}
		old.state = StateRedundant
		if next.opts.ClaimClients {
			for _, cl := range m.clients {
				if cl.versionID == old.id {
					cl.versionID = next.id
					delete(old.clients, cl.id)
					next.clients[cl.id] = true
				}
			}
		}
	}
	next.state = StateActive
}

func (m *naiveModel) maybePromote(reg *naiveReg) {
	waiting := reg.slot(StateWaiting)
	if waiting == nil {
		return
	}
	active := reg.slot(StateActive)
	if active == nil || len(active.clients) == 0 || waiting.opts.SkipWaiting {
		m.takeover(reg)
	}
}

func (m *naiveModel) finalize(reg *naiveReg) {
	for _, v := range reg.versions {
		if v.state != StateRedundant {
			v.state = StateRedundant
		}
	}
	kept := m.regs[:0]
	for _, r := range m.regs {
		if r != reg {
			kept = append(kept, r)
		}
	}
	m.regs = kept
}

func (m *naiveModel) release(cl *naiveClient) {
	if cl.versionID != 0 {
		delete(m.versions[cl.versionID].clients, cl.id)
		cl.versionID = 0
	}
	reg := cl.reg
	if reg == nil {
		return
	}
	delete(reg.clients, cl.id)
	cl.reg = nil
	if reg.pending {
		if len(reg.clients) == 0 {
			m.finalize(reg)
		}
		return
	}
	m.maybePromote(reg)
}

func (m *naiveModel) applyCheck(reg *naiveReg, digest string, now time.Time) (uint64, bool) {
	reg.last = now
	reg.checked = true
	cur := reg.slot(StateActive)
	if cur == nil {
		cur = reg.slot(StateWaiting)
	}
	if cur == nil {
		cur = reg.slot(StateInstalling)
	}
	if cur != nil && cur.digest == digest {
		return 0, false
	}
	m.nextID++
	v := &naiveVersion{
		id:       m.nextID,
		script:   reg.script,
		digest:   digest,
		state:    StateInstalling,
		manifest: make(map[string]string),
		clients:  make(map[string]bool),
	}
	if old := reg.slot(StateInstalling); old != nil {
		old.state = StateRedundant
	}
	reg.versions = append(reg.versions, v)
	m.versions[v.id] = v
	return v.id, true
}

func (m *naiveModel) Register(scope, script string) (bool, error) {
	if !validScope(scope) {
		return false, invalidArgf("scope %q must end with '/'", scope)
	}
	if script == "" {
		return false, invalidArgf("script URL must not be empty")
	}
	now, err := m.clock()
	if err != nil {
		return false, err
	}
	if reg := m.findReg(scope); reg != nil {
		if reg.script == script {
			reg.pending = false
			m.lastTime = now
			return true, nil
		}
		digest, err := m.digest(script)
		if err != nil {
			return true, err
		}
		reg.pending = false
		reg.script = script
		m.applyCheck(reg, digest, now)
		m.lastTime = now
		return true, nil
	}
	m.regs = append(m.regs, &naiveReg{scope: scope, script: script, clients: make(map[string]bool)})
	m.lastTime = now
	return false, nil
}

func (m *naiveModel) Unregister(scope string) error {
	if !validScope(scope) {
		return invalidArgf("scope %q must end with '/'", scope)
	}
	now, err := m.clock()
	if err != nil {
		return err
	}
	reg := m.findReg(scope)
	if reg == nil {
		return fmt.Errorf("%w: scope %q", ErrRegistrationNotFound, scope)
	}
	if reg.pending {
		return stateNotAllowedf("scope %q already pending removal", scope)
	}
	reg.pending = true
	if len(reg.clients) == 0 {
		m.finalize(reg)
	}
	m.lastTime = now
	return nil
}

func (m *naiveModel) CheckUpdate(scope string) (uint64, bool, error) {
	if !validScope(scope) {
		return 0, false, invalidArgf("scope %q must end with '/'", scope)
	}
	now, err := m.clock()
	if err != nil {
		return 0, false, err
	}
	reg := m.findReg(scope)
	if reg == nil {
		return 0, false, fmt.Errorf("%w: scope %q", ErrRegistrationNotFound, scope)
	}
	if reg.pending {
		return 0, false, stateNotAllowedf("scope %q pending removal", scope)
	}
	if reg.checked && now.Sub(reg.last) < m.min {
		return 0, false, fmt.Errorf("%w: scope %q", ErrTooFrequent, scope)
	}
	digest, err := m.digest(reg.script)
	if err != nil {
		return 0, false, err
	}
	id, created := m.applyCheck(reg, digest, now)
	m.lastTime = now
	return id, created, nil
}

func (m *naiveModel) InstallSuccess(id uint64, manifest map[string]string) error {
	now, err := m.clock()
	if err != nil {
		return err
	}
	v, ok := m.versions[id]
	if !ok {
		return fmt.Errorf("%w: id %d", ErrVersionNotFound, id)
	}
	if v.state != StateInstalling {
		return stateNotAllowedf("version %d is %s", id, v.state)
	}
	reg := m.regOf(v)
	if old := reg.slot(StateWaiting); old != nil {
		old.state = StateRedundant
	}
	v.manifest = make(map[string]string, len(manifest))
	for url, digest := range manifest {
		v.manifest[url] = digest
	}
	v.state = StateWaiting
	m.maybePromote(reg)
	m.lastTime = now
	return nil
}

func (m *naiveModel) regOf(v *naiveVersion) *naiveReg {
	for _, r := range m.regs {
		for _, candidate := range r.versions {
			if candidate == v {
				return r
			}
		}
	}
	return nil
}

func (m *naiveModel) InstallFail(id uint64) error {
	now, err := m.clock()
	if err != nil {
		return err
	}
	v, ok := m.versions[id]
	if !ok {
		return fmt.Errorf("%w: id %d", ErrVersionNotFound, id)
	}
	if v.state != StateInstalling {
		return stateNotAllowedf("version %d is %s", id, v.state)
	}
	v.state = StateRedundant
	m.lastTime = now
	return nil
}

func (m *naiveModel) Declare(id uint64, opts TakeoverOptions) error {
	now, err := m.clock()
	if err != nil {
		return err
	}
	v, ok := m.versions[id]
	if !ok {
		return fmt.Errorf("%w: id %d", ErrVersionNotFound, id)
	}
	if v.state != StateInstalling && v.state != StateWaiting {
		return stateNotAllowedf("version %d is %s", id, v.state)
	}
	v.opts = opts
	if v.state == StateWaiting {
		m.maybePromote(m.regOf(v))
	}
	m.lastTime = now
	return nil
}

func (m *naiveModel) SetManifestEntry(id uint64, url, digest string) error {
	now, err := m.clock()
	if err != nil {
		return err
	}
	v, ok := m.versions[id]
	if !ok {
		return fmt.Errorf("%w: id %d", ErrVersionNotFound, id)
	}
	if v.state == StateRedundant {
		return stateNotAllowedf("version %d is redundant", id)
	}
	v.manifest[url] = digest
	m.lastTime = now
	return nil
}

func (m *naiveModel) Navigate(clientID, url string) error {
	if clientID == "" {
		return invalidArgf("client ID must not be empty")
	}
	now, err := m.clock()
	if err != nil {
		return err
	}
	cl, ok := m.clients[clientID]
	if !ok {
		cl = &naiveClient{id: clientID}
		m.clients[clientID] = cl
	}
	m.release(cl)
	if url != "" {
		if reg := m.match(url); reg != nil && !reg.pending {
			if active := reg.slot(StateActive); active != nil {
				cl.reg = reg
				cl.versionID = active.id
				reg.clients[clientID] = true
				active.clients[clientID] = true
			}
		}
	}
	m.lastTime = now
	return nil
}

func (m *naiveModel) Unload(clientID string) error {
	return m.Navigate(clientID, "")
}

func (m *naiveModel) Fetch(clientID, url string) (FetchResult, error) {
	if clientID == "" {
		return FetchResult{}, invalidArgf("client ID must not be empty")
	}
	if _, err := m.clock(); err != nil {
		return FetchResult{}, err
	}
	cl, ok := m.clients[clientID]
	if !ok || cl.versionID == 0 {
		return FetchResult{Network: true}, nil
	}
	v := m.versions[cl.versionID]
	if v.state == StateRedundant {
		return FetchResult{Network: true}, nil
	}
	if digest, hit := v.manifest[url]; hit {
		return FetchResult{Digest: digest}, nil
	}
	return FetchResult{Network: true}, nil
}

// --- state dumps: identical format for both implementations ---------

func manifestString(m map[string]string) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, k := range keys {
		fmt.Fprintf(&b, "%s=%s;", k, m[k])
	}
	return b.String()
}

func dumpReal(c *Coordinator) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	var b strings.Builder
	scopes := make([]string, 0, len(c.byScope))
	for scope := range c.byScope {
		scopes = append(scopes, scope)
	}
	sort.Strings(scopes)
	for _, scope := range scopes {
		r := c.byScope[scope]
		slotID := func(v *Version) uint64 {
			if v == nil {
				return 0
			}
			return v.id
		}
		clients := make([]string, 0, len(r.clients))
		for id := range r.clients {
			clients = append(clients, id)
		}
		sort.Strings(clients)
		fmt.Fprintf(&b, "reg %s|%s|pending=%v|i=%d w=%d a=%d|clients=%v|last=%d:%v\n",
			scope, r.scriptURL, r.pendingRemoval,
			slotID(r.installing), slotID(r.waiting), slotID(r.active),
			clients, r.lastCheck.UnixNano(), r.hasChecked)
	}
	ids := make([]int, 0, len(c.versions))
	for id := range c.versions {
		ids = append(ids, int(id))
	}
	sort.Ints(ids)
	for _, id := range ids {
		v := c.versions[uint64(id)]
		fmt.Fprintf(&b, "ver %d|%s|%s|%s|opts=%+v|clients=%d|manifest=%s\n",
			v.id, v.state, v.scriptURL, v.digest, v.opts, v.clientCount,
			manifestString(v.manifest))
	}
	clientIDs := make([]string, 0, len(c.clients))
	for id := range c.clients {
		clientIDs = append(clientIDs, id)
	}
	sort.Strings(clientIDs)
	for _, id := range clientIDs {
		cl := c.clients[id]
		var verID uint64
		scope := ""
		if cl.version != nil {
			verID = cl.version.id
		}
		if cl.reg != nil {
			scope = cl.reg.scope
		}
		fmt.Fprintf(&b, "client %s|ver=%d|scope=%s\n", id, verID, scope)
	}
	fmt.Fprintf(&b, "clock=%d nextID=%d\n", c.lastTime.UnixNano(), c.nextVersionID)
	return b.String()
}

func dumpModel(m *naiveModel) string {
	var b strings.Builder
	regs := make([]*naiveReg, len(m.regs))
	copy(regs, m.regs)
	sort.Slice(regs, func(i, j int) bool { return regs[i].scope < regs[j].scope })
	for _, r := range regs {
		slotID := func(state VersionState) uint64 {
			if v := r.slot(state); v != nil {
				return v.id
			}
			return 0
		}
		clients := make([]string, 0, len(r.clients))
		for id := range r.clients {
			clients = append(clients, id)
		}
		sort.Strings(clients)
		fmt.Fprintf(&b, "reg %s|%s|pending=%v|i=%d w=%d a=%d|clients=%v|last=%d:%v\n",
			r.scope, r.script, r.pending,
			slotID(StateInstalling), slotID(StateWaiting), slotID(StateActive),
			clients, r.last.UnixNano(), r.checked)
	}
	ids := make([]int, 0, len(m.versions))
	for id := range m.versions {
		ids = append(ids, int(id))
	}
	sort.Ints(ids)
	for _, id := range ids {
		v := m.versions[uint64(id)]
		fmt.Fprintf(&b, "ver %d|%s|%s|%s|opts=%+v|clients=%d|manifest=%s\n",
			v.id, v.state, v.script, v.digest, v.opts, len(v.clients),
			manifestString(v.manifest))
	}
	clientIDs := make([]string, 0, len(m.clients))
	for id := range m.clients {
		clientIDs = append(clientIDs, id)
	}
	sort.Strings(clientIDs)
	for _, id := range clientIDs {
		cl := m.clients[id]
		scope := ""
		if cl.reg != nil {
			scope = cl.reg.scope
		}
		fmt.Fprintf(&b, "client %s|ver=%d|scope=%s\n", id, cl.versionID, scope)
	}
	fmt.Fprintf(&b, "clock=%d nextID=%d\n", m.lastTime.UnixNano(), m.nextID)
	return b.String()
}

// --- randomized comparison ------------------------------------------

func errKind(err error) string {
	switch {
	case err == nil:
		return "ok"
	case errors.Is(err, ErrInvalidArgument):
		return "invalid-argument"
	case errors.Is(err, ErrClockRollback):
		return "clock-rollback"
	case errors.Is(err, ErrRegistrationNotFound):
		return "registration-not-found"
	case errors.Is(err, ErrVersionNotFound):
		return "version-not-found"
	case errors.Is(err, ErrStateNotAllowed):
		return "state-not-allowed"
	case errors.Is(err, ErrTooFrequent):
		return "too-frequent"
	default:
		return "other:" + err.Error()
	}
}

func TestRandomizedAgainstNaiveModel(t *testing.T) {
	for seed := int64(1); seed <= 30; seed++ {
		seed := seed
		t.Run(fmt.Sprintf("seed=%d", seed), func(t *testing.T) {
			runRandomSequence(t, seed, 400)
		})
	}
}

func runRandomSequence(t *testing.T, seed int64, steps int) {
	t.Helper()
	rng := rand.New(rand.NewSource(seed))
	now := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	digests := map[string]string{}
	digestGen := 0
	clockFn := func() time.Time { return now }
	digestFn := func(script string) (string, error) { return digests[script], nil }

	const minInterval = 2 * time.Second
	real := New(Options{MinUpdateInterval: minInterval, Now: clockFn, ScriptDigest: digestFn})
	model := newNaiveModel(minInterval, clockFn, digestFn)

	scopes := []string{"/", "/a/", "/a/b/", "/b/", "/app/"}
	scripts := []string{"sw1.js", "sw2.js", "sw3.js"}
	clientIDs := []string{"c1", "c2", "c3", "c4", "c5", "c6"}
	navURLs := []string{"/", "/a", "/a/", "/a/b", "/a/b/c", "/b/x", "/app/", "/app/page", "/zzz"}
	resources := []string{"/r1", "/r2", "/r3", "/r4", "/r5", "/r6"}
	pick := func(pool []string) string { return pool[rng.Intn(len(pool))] }

	var versionIDs []uint64
	manifestFor := func() map[string]string {
		out := map[string]string{}
		for _, res := range resources {
			if rng.Intn(2) == 0 {
				out[res] = fmt.Sprintf("m%d", rng.Intn(4))
			}
		}
		return out
	}

	compare := func(step int, op, out string) {
		t.Helper()
		realDump := dumpReal(real)
		modelDump := dumpModel(model)
		summary := fmt.Sprintf("regs=%d versions=%d clients=%d",
			strings.Count(realDump, "\nreg ")+strings.Count(realDump, "reg "),
			len(versionIDs), strings.Count(realDump, "client "))
		t.Logf("step %d: %s -> %s | %s", step, op, out, summary)
		if realDump != modelDump {
			t.Fatalf("state divergence after step %d (%s -> %s)\n--- real ---\n%s\n--- model ---\n%s",
				step, op, out, realDump, modelDump)
		}
	}

	for step := 0; step < steps; step++ {
		// Clock: usually forward, rarely backwards to exercise rollback.
		if rng.Intn(100) < 3 {
			now = now.Add(-10 * time.Second)
		} else {
			now = now.Add(time.Duration(rng.Intn(4)) * time.Second)
		}
		// Occasionally change a script digest to enable updates.
		if rng.Intn(100) < 8 {
			digestGen++
			digests[pick(scripts)] = fmt.Sprintf("d%d", digestGen)
		}

		var op, out string
		switch rng.Intn(10) {
		case 0: // register
			scope, script := pick(scopes), pick(scripts)
			re, rerr := real.Register(scope, script)
			me, merr := model.Register(scope, script)
			op = fmt.Sprintf("Register(%q,%q)", scope, script)
			out = fmt.Sprintf("existed=%v err=%s", re, errKind(rerr))
			if re != me || errKind(rerr) != errKind(merr) {
				t.Fatalf("step %d %s: real=(%v,%s) model=(%v,%s)",
					step, op, re, errKind(rerr), me, errKind(merr))
			}
		case 1: // unregister
			scope := pick(scopes)
			rerr := real.Unregister(scope)
			merr := model.Unregister(scope)
			op = fmt.Sprintf("Unregister(%q)", scope)
			out = fmt.Sprintf("err=%s", errKind(rerr))
			if errKind(rerr) != errKind(merr) {
				t.Fatalf("step %d %s: real=%s model=%s", step, op, errKind(rerr), errKind(merr))
			}
		case 2: // check update
			scope := pick(scopes)
			rid, rc, rerr := real.CheckUpdate(scope)
			mid, mc, merr := model.CheckUpdate(scope)
			op = fmt.Sprintf("CheckUpdate(%q)", scope)
			out = fmt.Sprintf("id=%d created=%v err=%s", rid, rc, errKind(rerr))
			if rid != mid || rc != mc || errKind(rerr) != errKind(merr) {
				t.Fatalf("step %d %s: real=(%d,%v,%s) model=(%d,%v,%s)",
					step, op, rid, rc, errKind(rerr), mid, mc, errKind(merr))
			}
			if rc {
				versionIDs = append(versionIDs, rid)
			}
		case 3: // install success
			if len(versionIDs) == 0 {
				continue
			}
			id := versionIDs[rng.Intn(len(versionIDs))]
			manifest := manifestFor()
			rerr := real.InstallSuccess(id, manifest)
			merr := model.InstallSuccess(id, manifest)
			op = fmt.Sprintf("InstallSuccess(%d,%v)", id, manifest)
			out = fmt.Sprintf("err=%s", errKind(rerr))
			if errKind(rerr) != errKind(merr) {
				t.Fatalf("step %d %s: real=%s model=%s", step, op, errKind(rerr), errKind(merr))
			}
		case 4: // install fail or declare
			if len(versionIDs) == 0 {
				continue
			}
			id := versionIDs[rng.Intn(len(versionIDs))]
			if rng.Intn(2) == 0 {
				rerr := real.InstallFail(id)
				merr := model.InstallFail(id)
				op = fmt.Sprintf("InstallFail(%d)", id)
				out = fmt.Sprintf("err=%s", errKind(rerr))
				if errKind(rerr) != errKind(merr) {
					t.Fatalf("step %d %s: real=%s model=%s", step, op, errKind(rerr), errKind(merr))
				}
			} else {
				opts := TakeoverOptions{
					SkipWaiting:     rng.Intn(2) == 0,
					ClaimClients:    rng.Intn(2) == 0,
					InheritManifest: rng.Intn(2) == 0,
				}
				rerr := real.Declare(id, opts)
				merr := model.Declare(id, opts)
				op = fmt.Sprintf("Declare(%d,%+v)", id, opts)
				out = fmt.Sprintf("err=%s", errKind(rerr))
				if errKind(rerr) != errKind(merr) {
					t.Fatalf("step %d %s: real=%s model=%s", step, op, errKind(rerr), errKind(merr))
				}
			}
		case 5, 6: // navigate
			client, url := pick(clientIDs), pick(navURLs)
			rerr := real.Navigate(client, url)
			merr := model.Navigate(client, url)
			op = fmt.Sprintf("Navigate(%q,%q)", client, url)
			out = fmt.Sprintf("err=%s", errKind(rerr))
			if errKind(rerr) != errKind(merr) {
				t.Fatalf("step %d %s: real=%s model=%s", step, op, errKind(rerr), errKind(merr))
			}
		case 7: // unload
			client := pick(clientIDs)
			rerr := real.Unload(client)
			merr := model.Unload(client)
			op = fmt.Sprintf("Unload(%q)", client)
			out = fmt.Sprintf("err=%s", errKind(rerr))
			if errKind(rerr) != errKind(merr) {
				t.Fatalf("step %d %s: real=%s model=%s", step, op, errKind(rerr), errKind(merr))
			}
		case 8: // fetch
			client, res := pick(clientIDs), pick(resources)
			rres, rerr := real.Fetch(client, res)
			mres, merr := model.Fetch(client, res)
			op = fmt.Sprintf("Fetch(%q,%q)", client, res)
			out = fmt.Sprintf("res=%+v err=%s", rres, errKind(rerr))
			if rres != mres || errKind(rerr) != errKind(merr) {
				t.Fatalf("step %d %s: real=(%+v,%s) model=(%+v,%s)",
					step, op, rres, errKind(rerr), mres, errKind(merr))
			}
		case 9: // set manifest entry
			if len(versionIDs) == 0 {
				continue
			}
			id := versionIDs[rng.Intn(len(versionIDs))]
			res := pick(resources)
			digest := fmt.Sprintf("m%d", rng.Intn(4))
			rerr := real.SetManifestEntry(id, res, digest)
			merr := model.SetManifestEntry(id, res, digest)
			op = fmt.Sprintf("SetManifestEntry(%d,%q,%q)", id, res, digest)
			out = fmt.Sprintf("err=%s", errKind(rerr))
			if errKind(rerr) != errKind(merr) {
				t.Fatalf("step %d %s: real=%s model=%s", step, op, errKind(rerr), errKind(merr))
			}
		}
		compare(step, op, out)
	}
	t.Logf("seed %d: %d steps, %d versions created, final state identical", seed, steps, len(versionIDs))
}

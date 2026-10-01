// Package nodeadmission implements a node admission and eviction manager with
// taints, tolerations, NoExecute eviction-time derivation, per-node per-tick
// eviction rate limiting and terminating-pod grace slots.
package nodeadmission

import (
	"sort"
	"sync"
)

const (
	maxNowMs       int64 = 1e15
	maxGraceMs     int64 = 1e12
	maxPodsBound   int64 = 1e6
	maxEvictRate   int64 = 1e6
	maxTolerationS int64 = 1e15
	msPerSecond    int64 = 1000
)

// Effect of a taint.
const (
	NoSchedule       = "NoSchedule"
	PreferNoSchedule = "PreferNoSchedule"
	NoExecute        = "NoExecute"
)

// Toleration operators.
const (
	OpEqual  = "Equal"
	OpExists = "Exists"
)

// Taint is (key, value, effect); addedAt is maintained per (key, effect).
type Taint struct {
	Key    string
	Value  string
	Effect string
}

// Toleration is (key, operator, value, effect, seconds).
// An empty Effect matches any effect. An empty Key requires Operator Exists
// and matches any key. Seconds == -1 means no time limit.
type Toleration struct {
	Key      string
	Operator string
	Value    string
	Effect   string
	Seconds  int64
}

// RejectCode identifies a rejection reason.
type RejectCode int

const (
	OK RejectCode = iota
	ErrInvalidConfig
	ErrInvalidArgument
	ErrClockMovedBack
	ErrPodExists
	ErrNodeNotFound
	ErrUntoleratedTaint
	ErrCapacity
	ErrNodeExists
	ErrTaintNotFound
)

// RejectError is returned for every rejected operation; Code is the reason and
// Detail carries extra information (e.g. the offending taint).
type RejectError struct {
	Code   RejectCode
	Detail string
}

func (e *RejectError) Error() string {
	return e.Detail
}

func reject(code RejectCode, detail string) error {
	return &RejectError{Code: code, Detail: detail}
}

type taintState struct {
	key     string
	effect  string
	value   string
	addedAt int64
}

type podState struct {
	node        string
	terminating bool
	releaseAt   int64
	tolerations []Toleration
}

type nodeState struct {
	maxPods int64
	taints  map[string]*taintState // key: taint key + "\x00" + effect
	pods    map[string]struct{}    // running or terminating, until released
}

// Manager is safe for concurrent use.
type Manager struct {
	mu      sync.Mutex
	grace   int64
	rate    int64
	lastNow int64
	nodes   map[string]*nodeState
	pods    map[string]*podState
}

// NewManager validates grace ms [0,1e12] and per-tick eviction rate [1,1e6].
func NewManager(graceMs, rate int64) (*Manager, error) {
	if graceMs < 0 || graceMs > maxGraceMs || rate < 1 || rate > maxEvictRate {
		return nil, reject(ErrInvalidConfig, "invalid manager configuration: grace or rate out of range")
	}
	return &Manager{
		grace:   graceMs,
		rate:    rate,
		lastNow: -1,
		nodes:   make(map[string]*nodeState),
		pods:    make(map[string]*podState),
	}, nil
}

func validEffect(e string) bool {
	return e == NoSchedule || e == PreferNoSchedule || e == NoExecute
}

func validNow(now int64) bool {
	return now >= 0 && now <= maxNowMs
}

func taintMapKey(k, effect string) string {
	return k + "\x00" + effect
}

func validateToleration(t Toleration) bool {
	if t.Operator != OpEqual && t.Operator != OpExists {
		return false
	}
	if t.Effect != "" && !validEffect(t.Effect) {
		return false
	}
	if t.Key == "" {
		if t.Operator != OpExists || t.Value != "" {
			return false
		}
	} else if t.Operator == OpExists && t.Value != "" {
		return false
	}
	if t.Seconds != -1 {
		if t.Effect != NoExecute || t.Seconds < 0 || t.Seconds > maxTolerationS {
			return false
		}
	}
	return true
}

func validateTaint(t Taint) bool {
	return t.Key != "" && validEffect(t.Effect)
}

// releaseExpired forgets pods whose terminating grace has elapsed at now.
func (m *Manager) releaseExpired(ns *nodeState, now int64) {
	for id := range ns.pods {
		ps := m.pods[id]
		if ps.terminating && now >= ps.releaseAt {
			delete(ns.pods, id)
			delete(m.pods, id)
		}
	}
}

// AddNode registers a node with a name and capacity maxPods in [1,1e6].
func (m *Manager) AddNode(name string, maxPods int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if name == "" || maxPods < 1 || maxPods > maxPodsBound {
		return reject(ErrInvalidArgument, "invalid AddNode argument: empty name or maxPods out of range")
	}
	if _, ok := m.nodes[name]; ok {
		return reject(ErrNodeExists, "node already exists: "+name)
	}
	m.nodes[name] = &nodeState{
		maxPods: maxPods,
		taints:  make(map[string]*taintState),
		pods:    make(map[string]struct{}),
	}
	return nil
}

// Taint adds a taint at now, or replaces only its value if (key, effect)
// already exists (addedAt is preserved).
func (m *Manager) Taint(node string, t Taint, now int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !validateTaint(t) || !validNow(now) {
		return reject(ErrInvalidArgument, "invalid Taint argument")
	}
	if now < m.lastNow {
		return reject(ErrClockMovedBack, "clock moved backwards")
	}
	ns, ok := m.nodes[node]
	if !ok {
		return reject(ErrNodeNotFound, "node not found: "+node)
	}
	mk := taintMapKey(t.Key, t.Effect)
	if ts, exists := ns.taints[mk]; exists {
		ts.value = t.Value
	} else {
		ns.taints[mk] = &taintState{key: t.Key, effect: t.Effect, value: t.Value, addedAt: now}
	}
	m.lastNow = now
	return nil
}

// Untaint removes a (key, effect) taint.
func (m *Manager) Untaint(node, key, effect string, now int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if key == "" || !validEffect(effect) || !validNow(now) {
		return reject(ErrInvalidArgument, "invalid Untaint argument")
	}
	if now < m.lastNow {
		return reject(ErrClockMovedBack, "clock moved backwards")
	}
	ns, ok := m.nodes[node]
	if !ok {
		return reject(ErrNodeNotFound, "node not found: "+node)
	}
	mk := taintMapKey(key, effect)
	if _, exists := ns.taints[mk]; !exists {
		return reject(ErrTaintNotFound, "taint not found: "+key+"/"+effect)
	}
	delete(ns.taints, mk)
	m.lastNow = now
	return nil
}

// Schedule binds a pod (with tolerations) to a node at now.
func (m *Manager) Schedule(podID, node string, tolerations []Toleration, now int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if podID == "" || !validNow(now) {
		return reject(ErrInvalidArgument, "invalid Schedule argument: empty pod id or now out of range")
	}
	for _, tol := range tolerations {
		if !validateToleration(tol) {
			return reject(ErrInvalidArgument, "invalid toleration in Schedule")
		}
	}
	if now < m.lastNow {
		return reject(ErrClockMovedBack, "clock moved backwards")
	}
	if _, exists := m.pods[podID]; exists {
		return reject(ErrPodExists, "pod already exists: "+podID)
	}
	ns, ok := m.nodes[node]
	if !ok {
		return reject(ErrNodeNotFound, "node not found: "+node)
	}
	m.releaseExpired(ns, now)
	for _, ts := range sortedTaints(ns) {
		if ts.effect != NoSchedule && ts.effect != NoExecute {
			continue
		}
		if !anyToleration(tolerations, ts) {
			return reject(ErrUntoleratedTaint, "untolerated taint: "+ts.key+"/"+ts.effect)
		}
	}
	if int64(len(ns.pods)) >= ns.maxPods {
		return reject(ErrCapacity, "node capacity exhausted: "+node)
	}
	ns.pods[podID] = struct{}{}
	m.pods[podID] = &podState{node: node, tolerations: append([]Toleration(nil), tolerations...)}
	m.lastNow = now
	return nil
}

// Tick evicts due pods on every node, rate-limited per node, and returns the
// evicted pod IDs sorted by (eviction time, pod ID).
func (m *Manager) Tick(now int64) ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !validNow(now) {
		return nil, reject(ErrInvalidArgument, "invalid Tick argument: now out of range")
	}
	if now < m.lastNow {
		return nil, reject(ErrClockMovedBack, "clock moved backwards")
	}
	m.lastNow = now

	var all []evictionCandidate
	for _, ns := range m.nodes {
		m.releaseExpired(ns, now)
	}
	for _, ns := range m.nodes {
		var due []evictionCandidate
		for id := range ns.pods {
			ps := m.pods[id]
			if ps.terminating {
				continue
			}
			at, evicted := evictionTime(ns, ps.tolerations)
			if evicted && at <= now {
				due = append(due, evictionCandidate{podID: id, at: at})
			}
		}
		sortCandidates(due)
		if int64(len(due)) > m.rate {
			due = due[:m.rate]
		}
		for _, c := range due {
			ps := m.pods[c.podID]
			ps.terminating = true
			ps.releaseAt = now + m.grace
		}
		// G == 0: a pod evicted at now has releaseAt == now and is gone
		// immediately, so it must not keep its slot or its ID.
		m.releaseExpired(ns, now)
		all = append(all, due...)
	}
	sortCandidates(all)
	out := make([]string, len(all))
	for i, c := range all {
		out[i] = c.podID
	}
	return out, nil
}

func anyToleration(tolerations []Toleration, ts *taintState) bool {
	for _, tol := range tolerations {
		if tolerationMatches(tol, ts.key, ts.value, ts.effect) {
			return true
		}
	}
	return false
}

// sortedTaints returns the node's taints sorted by (key bytes, effect bytes),
// matching the required first-untolerated-taint ordering.
func sortedTaints(ns *nodeState) []*taintState {
	out := make([]*taintState, 0, len(ns.taints))
	for _, ts := range ns.taints {
		out = append(out, ts)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].key != out[j].key {
			return out[i].key < out[j].key
		}
		return out[i].effect < out[j].effect
	})
	return out
}

func sortCandidates(c []evictionCandidate) {
	sort.Slice(c, func(i, j int) bool {
		if c[i].at != c[j].at {
			return c[i].at < c[j].at
		}
		return c[i].podID < c[j].podID
	})
}

// tolerationMatches reports whether tol matches a taint (key, value, effect).
func tolerationMatches(tol Toleration, key, value, effect string) bool {
	if tol.Effect != "" && tol.Effect != effect {
		return false
	}
	if tol.Key != "" && tol.Key != key {
		return false
	}
	if tol.Operator == OpExists {
		return true
	}
	return tol.Value == value
}

type evictionCandidate struct {
	podID string
	at    int64
}

// evictionTime derives the eviction timestamp of a running pod from the
// node's current NoExecute taints and the pod's tolerations.
// The boolean is false when the pod is never evicted.
func evictionTime(ns *nodeState, tolerations []Toleration) (int64, bool) {
	earliest := int64(0)
	found := false
	for _, ts := range ns.taints {
		if ts.effect != NoExecute {
			continue
		}
		d := ts.addedAt
		infinite := false
		matched := false
		for _, tol := range tolerations {
			if !tolerationMatches(tol, ts.key, ts.value, ts.effect) {
				continue
			}
			matched = true
			if tol.Seconds == -1 {
				infinite = true
			} else {
				d = ts.addedAt + tol.Seconds*msPerSecond
			}
			break
		}
		if matched && infinite {
			continue
		}
		if !found || d < earliest {
			earliest = d
			found = true
		}
	}
	return earliest, found
}

var _ = sort.Strings

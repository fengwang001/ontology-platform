package orphan

import (
	"fmt"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

// Edge is a directed link Src -> Dst of link type Type.
type Edge struct {
	Type string
	Src  string
	Dst  string
}

// Generation of a pending object.
type Generation int

const (
	NoGen Generation = 0
	Gen1  Generation = 1
	Gen2  Generation = 2
)

// State is the externally observable state of one object instance.
type State struct {
	Exists     bool
	Generation Generation
	SinceMs    int64
}

// System is the generational orphan reclamation subsystem.
type System struct {
	mu      sync.Mutex
	vc      *validatedConfig
	clock   Clock
	cascade CascadeDeleter
	objs    map[string]*objState
	logs    []LogEntry
	logger  Logger
}

type objState struct {
	id       string
	inbound  map[string]map[string]struct{} // type -> set of source ids
	outbound map[string]map[string]struct{} // type -> set of target ids
	gen      Generation
	since    int64
}

// Logger observes structured decision entries. The call happens while the
// subsystem lock is held, so implementations must return quickly.
type Logger interface {
	LogEntry(e LogEntry)
}

// WallClock is the default Clock.
type WallClock struct{}

func (WallClock) NowMs() int64 { return time.Now().UnixMilli() }

// ManualClock is a deterministic clock for tests and replay. It is safe for
// concurrent use (atomic storage), so interleaving scanners may advance it
// while mutators trigger evaluations.
type ManualClock struct{ t atomic.Int64 }

func (c *ManualClock) NowMs() int64     { return c.t.Load() }
func (c *ManualClock) Advance(ms int64) { c.t.Add(ms) }
func (c *ManualClock) Set(ms int64)     { c.t.Store(ms) }

// New constructs a validated System. Configuration errors follow the fixed
// class order 2 -> 3 -> 4; class 1 is live-instance level and is reported by
// edge operations.
func New(cfg Config, clock Clock, cascade CascadeDeleter) (*System, error) {
	vc, err := validateConfig(cfg)
	if err != nil {
		return nil, err
	}
	if clock == nil {
		clock = WallClock{}
	}
	return &System{vc: vc, clock: clock, cascade: cascade, objs: map[string]*objState{}}, nil
}

// AddLogger attaches an external log sink (in addition to the in-memory log).
func (s *System) AddLogger(l Logger) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.logger = l
}

// AddObject creates an object; returns false without change if id exists.
// A new object with no retaining inbound edge is immediately judged orphan
// and enters the first-generation queue.
func (s *System) AddObject(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.objs[id]; ok {
		return false
	}
	st := &objState{id: id, inbound: map[string]map[string]struct{}{}, outbound: map[string]map[string]struct{}{}}
	s.objs[id] = st
	s.evaluateLocked(st)
	return true
}

// AddEdge adds inbound edge typ: src -> dst and re-evaluates dst. Error
// order is fixed: class 1 (missing instance; dst checked before src) then
// class 2 (unconfigured link type).
func (s *System) AddEdge(typ, src, dst string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.objs[dst]; !ok {
		return fmt.Errorf("%w: %q", ErrObjectNotFound, dst)
	}
	if _, ok := s.objs[src]; !ok {
		return fmt.Errorf("%w: %q", ErrObjectNotFound, src)
	}
	if !s.vc.known[typ] {
		return fmt.Errorf("%w: %q", ErrTypeNotConfigured, typ)
	}
	s.addEdgeLocked(typ, src, dst)
	return nil
}

func (s *System) addEdgeLocked(typ, src, dst string) bool {
	st := s.objs[dst]
	srcSt := s.objs[src]
	set := st.inbound[typ]
	if set == nil {
		set = map[string]struct{}{}
		st.inbound[typ] = set
	}
	if _, ok := set[src]; ok {
		// Duplicate edge; outbound mirroring is idempotent too.
		return false
	}
	set[src] = struct{}{}
	oset := srcSt.outbound[typ]
	if oset == nil {
		oset = map[string]struct{}{}
		srcSt.outbound[typ] = oset
	}
	oset[dst] = struct{}{}
	s.evaluateLocked(st)
	return true
}

// RemoveEdge removes typ: src -> dst and re-evaluates dst. Missing edge
// yields (false, nil). Error order matches AddEdge.
func (s *System) RemoveEdge(typ, src, dst string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.objs[dst]; !ok {
		return false, fmt.Errorf("%w: %q", ErrObjectNotFound, dst)
	}
	if _, ok := s.objs[src]; !ok {
		return false, fmt.Errorf("%w: %q", ErrObjectNotFound, src)
	}
	if !s.vc.known[typ] {
		return false, fmt.Errorf("%w: %q", ErrTypeNotConfigured, typ)
	}
	return s.removeEdgeLocked(typ, src, dst), nil
}

// removeEdgeLocked removes one edge and re-evaluates the target.
func (s *System) removeEdgeLocked(typ, src, dst string) bool {
	st, ok := s.objs[dst]
	if !ok {
		return false
	}
	set := st.inbound[typ]
	if _, exists := set[src]; !exists {
		return false
	}
	delete(set, src)
	if len(set) == 0 {
		delete(st.inbound, typ)
	}
	if srcSt, ok := s.objs[src]; ok {
		if os := srcSt.outbound[typ]; os != nil {
			delete(os, dst)
			if len(os) == 0 {
				delete(srcSt.outbound, typ)
			}
		}
	}
	s.evaluateLocked(st)
	return true
}

// StateOf returns the observable state; ok is false for cleaned/unknown ids.
func (s *System) StateOf(id string) (State, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, ok := s.objs[id]
	if !ok {
		return State{}, false
	}
	return State{Exists: true, Generation: st.gen, SinceMs: st.since}, true
}

// Exists reports whether id is a live object.
func (s *System) Exists(id string) bool {
	_, ok := s.StateOf(id)
	return ok
}

// PendingGen1 and PendingGen2 return sorted snapshot lists of queued ids.
func (s *System) PendingGen1() []string { return s.pending(Gen1) }
func (s *System) PendingGen2() []string { return s.pending(Gen2) }

func (s *System) pending(g Generation) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []string
	for id, st := range s.objs {
		if st.gen == g {
			out = append(out, id)
		}
	}
	sort.Strings(out)
	return out
}

// RetainReport explains one retention evaluation.
type RetainReport struct {
	PresentTypes      []string       // types with >=1 inbound edge (sorted)
	PresentCounts     map[string]int // inbound edge count per present type
	IndependentChecks int            // layer-1 type-presence probes
	JointGroupsTried  int            // layer-2 groups probed
	JointMemberChecks int            // layer-2 member-presence probes
	Reason            string         // "independent:<t>", "joint:<group>", "orphan"
	Retained          bool
}

// Scan runs one generation-advancement pass at the current clock time.
// Gen-1 orphans whose grace elapsed promote to gen 2 with a fresh timer;
// gen-2 orphans whose grace elapsed are cleaned up atomically.
func (s *System) Scan() {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.clock.NowMs()
	ids := make([]string, 0, len(s.objs))
	for id := range s.objs {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		st, ok := s.objs[id]
		if !ok {
			continue // removed by an earlier cascade within this scan
		}
		switch st.gen {
		case Gen1:
			if now-st.since >= s.vc.cfg.GraceGen1Ms {
				st.gen = Gen2
				st.since = now
				s.logLocked(LogEntry{Op: "promote", TimeMs: now, Object: id, FromGen: Gen1, ToGen: Gen2, SinceMs: now})
			}
		case Gen2:
			if now-st.since >= s.vc.cfg.GraceGen2Ms {
				s.cleanupLocked(st, now)
			}
		}
	}
}

// cleanupLocked deletes st atomically together with the application's
// existing cascade handling of its outgoing edges. All of it runs under the
// subsystem lock, so no concurrent operation can observe the expired
// judgment before the queue record is updated.
func (s *System) cleanupLocked(st *objState, now int64) {
	id := st.id
	s.logLocked(LogEntry{Op: "cleanup_begin", TimeMs: now, Object: id, FromGen: Gen2})
	cx := &Cascade{s: s, now: now}
	if s.cascade != nil {
		s.cascade.DeleteCascade(cx, id)
	} else {
		DefaultCascade{}.DeleteCascade(cx, id)
	}
	// Drop dangling inbound references (edges whose target was st).
	for typ, set := range st.inbound {
		for src := range set {
			if srcSt, ok := s.objs[src]; ok {
				if os := srcSt.outbound[typ]; os != nil {
					delete(os, id)
					if len(os) == 0 {
						delete(srcSt.outbound, typ)
					}
				}
			}
		}
	}
	delete(s.objs, id)
	s.logLocked(LogEntry{Op: "cleanup_end", TimeMs: now, Object: id, FromGen: Gen2, ToGen: NoGen})
}

// Cascade is the locked execution context handed to a CascadeDeleter.
type Cascade struct {
	s   *System
	now int64
}

// CascadeDeleter performs the platform's existing cascade-deletion rules for
// an object. It is invoked exactly once per cleaned object, atomically with
// the cleanup of the object itself (under the subsystem lock).
type CascadeDeleter interface {
	DeleteCascade(cx *Cascade, id string)
}

// NowMs returns the scan time of the ongoing cleanup.
func (c *Cascade) NowMs() int64 { return c.now }

// RemoveEdge removes an edge during cascade processing and re-evaluates its
// live target. It must not target the object currently being deleted.
func (c *Cascade) RemoveEdge(typ, src, dst string) bool {
	return c.s.removeEdgeLocked(typ, src, dst)
}

// Outbound returns (type, target) pairs of id's outgoing edges, sorted.
func (c *Cascade) Outbound(id string) []Edge {
	st, ok := c.s.objs[id]
	if !ok {
		return nil
	}
	var out []Edge
	for t, set := range st.outbound {
		for d := range set {
			out = append(out, Edge{Type: t, Src: id, Dst: d})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Type != out[j].Type {
			return out[i].Type < out[j].Type
		}
		return out[i].Dst < out[j].Dst
	})
	return out
}

// DefaultCascade removes every outgoing edge of the deleted object in
// deterministic order. The platform's pre-existing cascade rules live behind
// CascadeDeleter and replace this; those rules are out of scope here, only
// atomicity of the cleanup action is guaranteed.
type DefaultCascade struct{}

// DeleteCascade implements CascadeDeleter.
func (DefaultCascade) DeleteCascade(cx *Cascade, id string) {
	for _, e := range cx.Outbound(id) {
		cx.RemoveEdge(e.Type, e.Src, e.Dst)
	}
}

// Evaluate runs the retention rule for one object immediately and returns its
// report. Serves both as the internal transition primitive and an observable
// hook for tests (order-of-layers and bound-on-probes).
func (s *System) Evaluate(id string) (State, RetainReport, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, ok := s.objs[id]
	if !ok {
		return State{}, RetainReport{}, false
	}
	r := s.evaluateLocked(st)
	return State{Exists: true, Generation: st.gen, SinceMs: st.since}, r, true
}

// evaluateLocked applies the fixed two-layer retention rule and performs the
// queue transition. Cost is O(number of *configured* link types / groups);
// the number of inbound edges never enters the probe count.
func (s *System) evaluateLocked(st *objState) RetainReport {
	now := s.clock.NowMs()
	r := s.retainLocked(st)
	from := st.gen
	switch {
	case r.Retained:
		// Released from either queue directly to non-orphan. No generation
		// memory survives; the next orphan episode starts again at gen 1.
		st.gen = NoGen
		st.since = 0
	case from == NoGen:
		st.gen = Gen1
		st.since = now
	}
	s.logLocked(LogEntry{
		Op: "evaluate", TimeMs: now, Object: st.id,
		PresentTypes: r.PresentTypes, PresentCounts: r.PresentCounts,
		IndependentChecks: r.IndependentChecks, JointMemberChecks: r.JointMemberChecks,
		JointGroupsTried: r.JointGroupsTried, Reason: r.Reason,
		Retained: r.Retained, FromGen: from, ToGen: st.gen, SinceMs: st.since,
	})
	return r
}

// retainLocked executes the two layers in their mandatory order:
// layer 1 — any independent type present -> retained;
// layer 2 — only if layer 1 found nothing, any fully present joint group ->
// retained. Layer 2 is never reached when layer 1 succeeds.
func (s *System) retainLocked(st *objState) RetainReport {
	r := RetainReport{PresentCounts: map[string]int{}}
	for t, set := range st.inbound {
		r.PresentTypes = append(r.PresentTypes, t)
		r.PresentCounts[t] = len(set)
	}
	sort.Strings(r.PresentTypes)

	// Layer 1: independent retention, one presence probe per configured type.
	for _, t := range s.vc.indep {
		r.IndependentChecks++
		if _, ok := st.inbound[t]; ok {
			r.Reason = "independent:" + t
			r.Retained = true
			return r
		}
	}

	// Layer 2: joint retention, one member-presence probe per member of each
	// group until the first fully-satisfied group.
	for _, g := range s.vc.groupList {
		r.JointGroupsTried++
		satisfied := true
		for _, m := range g.members {
			r.JointMemberChecks++
			if _, ok := st.inbound[m]; !ok {
				satisfied = false
				break
			}
		}
		if satisfied {
			for _, x := range g.extras {
				if _, ok := st.inbound[x]; !ok {
					satisfied = false
					break
				}
			}
		}
		if satisfied {
			r.Reason = "joint:" + g.id
			r.Retained = true
			return r
		}
	}
	r.Reason = "orphan"
	return r
}

// Logs returns a copy of all recorded decision entries.
func (s *System) Logs() []LogEntry {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]LogEntry, len(s.logs))
	copy(out, s.logs)
	return out
}

func (s *System) logLocked(e LogEntry) {
	if e.PresentCounts == nil {
		e.PresentCounts = map[string]int{}
	}
	s.logs = append(s.logs, e)
	if s.logger != nil {
		s.logger.LogEntry(e)
	}
}

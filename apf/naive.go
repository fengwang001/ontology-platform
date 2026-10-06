package apf

import (
	"fmt"
	"sort"
	"time"
)

// naiveController is a deliberately simple reference implementation of the
// same semantics as Controller, written with plain slices and linear scans.
// It exists so tests can cross-check the optimized controller against an
// independent model on long random operation sequences.
type naiveController struct {
	cfg      Config
	nominal  map[string]int
	levels   map[string]*naiveLevel
	lastTime time.Time
	nextID   int
	events   []naiveEvent
}

type naiveLevel struct {
	nominal      int
	occupied     int
	queueLimit   int
	queueTimeout time.Duration
	waiters      []*naiveWaiter // enqueue order
	flowOrder    []string       // by most recent empty->non-empty transition
	lastServed   string
	hasServed    bool
}

type naiveWaiter struct {
	id       int
	req      Request
	deadline time.Time
	flow     string
}

type naiveEvent struct {
	ticket int
	lease  *naiveLease
	err    *Error
}

type naiveLease struct {
	ctrl  *naiveController
	level *naiveLevel
	seats int
	done  bool
}

func newNaive(t time.Time, cfg Config) (*naiveController, error) {
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	n := &naiveController{
		lastTime: t,
		levels:   make(map[string]*naiveLevel),
	}
	n.applyConfig(cfg)
	return n, nil
}

func (n *naiveController) applyConfig(cfg Config) {
	n.cfg = cfg
	n.nominal = allocateNominalSeats(cfg.Levels, cfg.TotalSeats)
	limited := make(map[string]*Level)
	for i := range cfg.Levels {
		l := &cfg.Levels[i]
		if !l.Exempt {
			limited[l.Name] = l
		}
	}
	for name, l := range limited {
		st, ok := n.levels[name]
		if !ok {
			st = &naiveLevel{}
			n.levels[name] = st
		}
		st.nominal = n.nominal[name]
		st.queueLimit = l.QueueLimit
		st.queueTimeout = l.QueueTimeout
	}
	for name, st := range n.levels {
		if _, ok := limited[name]; !ok {
			st.nominal = 0
			st.queueLimit = 0
		}
	}
}

func (n *naiveController) classify(req Request) (level, flow string, ok bool) {
	best := -1
	for i, r := range n.cfg.Rules {
		if len(r.Users) > 0 && !contains(r.Users, req.User) {
			continue
		}
		if len(r.Verbs) > 0 && !contains(r.Verbs, req.Verb) {
			continue
		}
		if len(r.Resources) > 0 && !contains(r.Resources, req.Resource) {
			continue
		}
		if best == -1 ||
			r.Precedence < n.cfg.Rules[best].Precedence ||
			(r.Precedence == n.cfg.Rules[best].Precedence && r.Name < n.cfg.Rules[best].Name) {
			best = i
		}
	}
	if best == -1 {
		return "", "", false
	}
	r := n.cfg.Rules[best]
	if r.DistinguishBy == ByNamespace {
		return r.Level, "ns:" + req.Namespace, true
	}
	return r.Level, "user:" + req.User, true
}

func contains(set []string, v string) bool {
	for _, s := range set {
		if s == v {
			return true
		}
	}
	return false
}

func (n *naiveController) checkClock(t time.Time) error {
	if t.Before(n.lastTime) {
		return errf(KindClockSkew, fmt.Sprintf("time %s before %s", t, n.lastTime))
	}
	return nil
}

func (n *naiveController) maintenance(t time.Time) {
	names := n.sortedLevelNames()
	for _, name := range names {
		n.levels[name].evict(n, t)
	}
	for _, name := range names {
		n.levels[name].dispatch(n)
	}
}

func (n *naiveController) sortedLevelNames() []string {
	names := make([]string, 0, len(n.levels))
	for name := range n.levels {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func (l *naiveLevel) evict(n *naiveController, t time.Time) {
	for {
		// Find the expired waiter with the smallest (deadline, id).
		best := -1
		for i, w := range l.waiters {
			if w.deadline.After(t) {
				continue
			}
			if best == -1 || w.deadline.Before(l.waiters[best].deadline) ||
				(w.deadline.Equal(l.waiters[best].deadline) && w.id < l.waiters[best].id) {
				best = i
			}
		}
		if best == -1 {
			return
		}
		w := l.waiters[best]
		l.waiters = append(l.waiters[:best], l.waiters[best+1:]...)
		n.events = append(n.events, naiveEvent{ticket: w.id,
			err: errf(KindQueueTimeout, "naive timeout")})
	}
}

func (l *naiveLevel) flowHasWaiters(flow string) bool {
	for _, w := range l.waiters {
		if w.flow == flow {
			return true
		}
	}
	return false
}

func (l *naiveLevel) dispatch(n *naiveController) {
	for len(l.waiters) > 0 {
		// Locate the last served flow in the rotation order.
		idx := -1
		if l.hasServed {
			for i, f := range l.flowOrder {
				if f == l.lastServed {
					idx = i
					break
				}
			}
		}
		// First flow with waiters strictly after it, wrapping around.
		chosen := ""
		for k := 1; k <= len(l.flowOrder); k++ {
			f := l.flowOrder[(idx+k)%len(l.flowOrder)]
			if l.flowHasWaiters(f) {
				chosen = f
				break
			}
		}
		if chosen == "" {
			return
		}
		// Head of the chosen flow.
		headIdx := -1
		for i, w := range l.waiters {
			if w.flow == chosen {
				headIdx = i
				break
			}
		}
		head := l.waiters[headIdx]
		if l.occupied+head.req.Seats > l.nominal {
			return
		}
		l.waiters = append(l.waiters[:headIdx], l.waiters[headIdx+1:]...)
		l.occupied += head.req.Seats
		l.lastServed = chosen
		l.hasServed = true
		n.events = append(n.events, naiveEvent{ticket: head.id,
			lease: &naiveLease{ctrl: n, level: l, seats: head.req.Seats}})
	}
}

func (n *naiveController) admit(t time.Time, req Request) (*naiveLease, int, error) {
	if err := validateRequest(req); err != nil {
		return nil, -1, err
	}
	if err := n.checkClock(t); err != nil {
		return nil, -1, err
	}
	n.maintenance(t)

	levelName, flow, ok := n.classify(req)
	if !ok {
		return nil, -1, errf(KindNoMatch, "naive: no rule matches")
	}
	var exempt bool
	for _, l := range n.cfg.Levels {
		if l.Name == levelName {
			exempt = l.Exempt
		}
	}
	if exempt {
		n.lastTime = t
		return &naiveLease{ctrl: n}, -1, nil
	}
	st := n.levels[levelName]
	if req.Seats > st.nominal {
		return nil, -1, errf(KindUnsatisfiable, "naive: unsatisfiable")
	}
	if len(st.waiters) == 0 && st.occupied+req.Seats <= st.nominal {
		st.occupied += req.Seats
		n.lastTime = t
		return &naiveLease{ctrl: n, level: st, seats: req.Seats}, -1, nil
	}
	if len(st.waiters) >= st.queueLimit {
		return nil, -1, errf(KindQueueFull, "naive: queue full")
	}
	id := n.nextID
	n.nextID++
	st.waiters = append(st.waiters, &naiveWaiter{
		id:       id,
		req:      req,
		deadline: t.Add(st.queueTimeout),
		flow:     flow,
	})
	// Flow became non-empty: move it to the end of the rotation order.
	if !st.flowHasWaitersBefore(flow, len(st.waiters)-1) {
		pos := -1
		for i, f := range st.flowOrder {
			if f == flow {
				pos = i
				break
			}
		}
		if pos >= 0 {
			st.flowOrder = append(st.flowOrder[:pos], st.flowOrder[pos+1:]...)
		}
		st.flowOrder = append(st.flowOrder, flow)
	}
	n.lastTime = t
	return nil, id, nil
}

// flowHasWaitersBefore reports whether flow appears among the first k
// waiters (used to detect the empty -> non-empty transition).
func (l *naiveLevel) flowHasWaitersBefore(flow string, k int) bool {
	for i := 0; i < k && i < len(l.waiters); i++ {
		if l.waiters[i].flow == flow {
			return true
		}
	}
	return false
}

func (l *naiveLease) finish(t time.Time) error {
	n := l.ctrl
	if l.done {
		return errf(KindInvalidArgument, "naive: lease already finished")
	}
	if err := n.checkClock(t); err != nil {
		return err
	}
	n.maintenance(t)
	l.done = true
	if l.level != nil {
		l.level.occupied -= l.seats
	}
	for _, name := range n.sortedLevelNames() {
		n.levels[name].dispatch(n)
	}
	n.lastTime = t
	return nil
}

func (n *naiveController) updateConfig(t time.Time, cfg Config) error {
	if err := cfg.validate(); err != nil {
		return err
	}
	if err := n.checkClock(t); err != nil {
		return err
	}
	n.maintenance(t)
	n.applyConfig(cfg)
	for _, name := range n.sortedLevelNames() {
		st := n.levels[name]
		kept := st.waiters[:0]
		for _, w := range st.waiters {
			if w.req.Seats > st.nominal {
				n.events = append(n.events, naiveEvent{ticket: w.id,
					err: errf(KindUnsatisfiable, "naive: unsatisfiable after update")})
				continue
			}
			kept = append(kept, w)
		}
		st.waiters = kept
	}
	for _, name := range n.sortedLevelNames() {
		n.levels[name].dispatch(n)
	}
	n.lastTime = t
	return nil
}

// drainEvents returns and clears the events accumulated since the last call.
func (n *naiveController) drainEvents() []naiveEvent {
	ev := n.events
	n.events = nil
	return ev
}

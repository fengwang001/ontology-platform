package pdb

import (
	"sync"
	"time"
)

// labelKey identifies a label within a namespace.
type labelKey struct {
	namespace string
	key       string
	value     string
}

// budgetKey indexes budgets by namespace + one selector key.
type budgetKey struct {
	namespace string
	key       string
}

// Service is the eviction adjudication service. All exported methods are safe
// for concurrent use; every call is serialized by mu and appears atomic.
type Service struct {
	mu sync.Mutex

	grace time.Duration
	clock time.Time

	// pods: namespace -> uid -> entry
	pods map[string]map[string]*podEntry
	// budgets: namespace -> name -> entry
	budgets map[string]map[string]*budgetEntry

	// podByLabel indexes in-stats pods by each of their labels.
	podByLabel map[labelKey]map[PodRef]*podEntry
	// budgetsByKey indexes non-empty-selector budgets by each selector key.
	budgetsByKey map[budgetKey]map[NamespacedName]*budgetEntry

	// evictions indexes in-flight evictions by pod.
	evictions map[PodRef]*eviction
	// deadlines is the min-heap driving automatic expiry.
	deadlines deadlineHeap

	// instrumentation, used by the verifiable complexity test.
	instrument bool
	cost       costStats
}

// New creates a Service with the given eviction grace period.
func New(grace time.Duration) *Service {
	return &Service{
		grace:        grace,
		pods:         map[string]map[string]*podEntry{},
		budgets:      map[string]map[string]*budgetEntry{},
		podByLabel:   map[labelKey]map[PodRef]*podEntry{},
		budgetsByKey: map[budgetKey]map[NamespacedName]*budgetEntry{},
		evictions:    map[PodRef]*eviction{},
	}
}

// begin validates the injected clock and applies every due expiry. Parameter
// validation must happen before begin so invalid arguments never move state;
// begin itself rejects a rollback and changes no state when doing so.
func (s *Service) begin(now time.Time) error {
	if now.Before(s.clock) {
		return errf(ReasonClockRollback, PodRef{}, -1,
			"clock moved backwards: now=%s last=%s",
			now.Format(time.RFC3339Nano), s.clock.Format(time.RFC3339Nano))
	}
	s.expireDue(now)
	return nil
}

// commit advances the injected clock after an accepted call.
func (s *Service) commit(now time.Time) {
	if now.After(s.clock) {
		s.clock = now
	}
}

// expireDue applies every eviction whose deadline has been reached. Expiry is
// left-closed: the deadline instant itself counts as expired.
func (s *Service) expireDue(now time.Time) {
	for len(s.deadlines) > 0 {
		top := s.deadlines[0]
		if now.Before(top.deadline) {
			return
		}
		heapPop(&s.deadlines)
		s.finishEviction(top, false, now)
	}
}

// finishEviction completes an in-flight eviction.
//   - remove=true (confirm): the pod is deleted.
//   - remove=false (cancel/expiry): the pod stays and readiness is restored.
func (s *Service) finishEviction(e *eviction, remove bool, now time.Time) {
	delete(s.evictions, e.ref)
	pe, ok := s.lookupPod(e.ref)
	if !ok {
		return
	}
	pe.evicting = nil
	if remove {
		s.removePodLocked(pe)
		return
	}
	if !pe.pod.Phase.InStats() {
		return
	}
	if pe.pod.Ready != e.wasReady {
		s.applyReadyDelta(e.budget, pe, b2i(e.wasReady)-b2i(pe.pod.Ready))
		pe.pod.Ready = e.wasReady
	}
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

func (s *Service) lookupPod(ref PodRef) (*podEntry, bool) {
	ns, ok := s.pods[ref.Namespace]
	if !ok {
		return nil, false
	}
	pe, ok := ns[ref.UID]
	return pe, ok
}

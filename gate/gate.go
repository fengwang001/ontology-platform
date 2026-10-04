package gate

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"

	"ontology/compat"
	"ontology/schema"
)

// Sentinel errors. Every rejected operation wraps one of these, so callers
// can classify outcomes with errors.Is.
var (
	ErrInvalidArgument = errors.New("invalid argument")
	ErrClockSkew       = errors.New("clock moved backwards")
	ErrNotFound        = errors.New("not found")
	ErrExists          = errors.New("already exists")
	ErrNoChange        = errors.New("no schema change")
	ErrIncompatible    = errors.New("incompatible schema")
	ErrBlocked         = errors.New("blocked by subscribers")
	ErrStillBroken     = errors.New("subscriber still broken")
)

const (
	minNow = int64(0)
	maxNow = int64(1_000_000_000_000)
)

// Registry is the concurrency-safe store of all subjects and subscribers.
type Registry struct {
	mu       sync.RWMutex
	subjects map[string]*subject
}

// NewRegistry creates an empty registry.
func NewRegistry() *Registry {
	return &Registry{subjects: make(map[string]*subject)}
}

type subject struct {
	mode      schema.Mode
	versions  []*schema.Version // 0-indexed; version number = index+1
	consumers map[string]*subscriber
	now       int64
}

type subscriber struct {
	pinned   *schema.Version    // currently pinned version snapshot
	pinnedN  int                // pinned version number
	view     *schema.Projection // reader view over pinned
	lagging  bool
	lagged   int   // version that marked the subscriber lagging
	until    int64 // waiver deadline; valid while opNow < until
	hasWaive bool
}

// Blocker records one subscriber that blocks a publish and its first
// reader-side violation.
type Blocker struct {
	Consumer  string
	Violation compat.Violation
}

// IncompatibleError carries the failing mode check direction and the first
// violation. It wraps ErrIncompatible.
type IncompatibleError struct {
	Direction compat.Direction
	Violation compat.Violation
}

func (e *IncompatibleError) Error() string {
	return fmt.Sprintf("%s: %s violation at field %q: %s", ErrIncompatible,
		dirName(e.Direction), e.Violation.Field, reasonName(e.Violation.Reason))
}
func (e *IncompatibleError) Unwrap() error { return ErrIncompatible }

// BlockedError carries every blocking subscriber in consumer byte order.
// It wraps ErrBlocked.
type BlockedError struct {
	Blockers []Blocker
}

func (e *BlockedError) Error() string {
	parts := make([]string, len(e.Blockers))
	for i, b := range e.Blockers {
		parts[i] = fmt.Sprintf("%s:%s %s", b.Consumer, b.Violation.Field,
			reasonName(b.Violation.Reason))
	}
	return fmt.Sprintf("%s: %s", ErrBlocked, strings.Join(parts, ", "))
}
func (e *BlockedError) Unwrap() error { return ErrBlocked }

func dirName(d compat.Direction) string {
	switch d {
	case compat.Backward:
		return "BACKWARD"
	case compat.Forward:
		return "FORWARD"
	}
	return "UNKNOWN"
}

func reasonName(r compat.Reason) string {
	switch r {
	case compat.MissingNoDefault:
		return "MissingNoDefault"
	case compat.TypeMismatch:
		return "TypeMismatch"
	case compat.OptionalToRequired:
		return "OptionalToRequired"
	}
	return "Unknown"
}

func validClock(now int64) bool { return now >= minNow && now <= maxNow }

// CreateSubject creates subject with version 1 built from fields.
func (r *Registry) CreateSubject(name string, mode schema.Mode, fields []schema.Field, now int64) (int, error) {
	ver, err := schema.NewVersion(fields)
	if err != nil || !schema.ValidMode(mode) {
		return 0, ErrInvalidArgument
	}
	if !validClock(now) {
		return 0, ErrInvalidArgument
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.subjects[name]; ok {
		return 0, ErrExists
	}
	r.subjects[name] = &subject{
		mode:      mode,
		versions:  []*schema.Version{ver},
		consumers: make(map[string]*subscriber),
		now:       now,
	}
	return 1, nil
}

// Subscribe registers a consumer pinned to an existing version with a
// projected reader view. The view must already read the latest version.
func (r *Registry) Subscribe(subjName, consumer string, pinned int, fields []string, now int64) error {
	if consumer == "" || !validNameList(fields) || !validClock(now) {
		return ErrInvalidArgument
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	s, ok := r.subjects[subjName]
	if !ok {
		return ErrNotFound
	}
	if now < s.now {
		return ErrClockSkew
	}
	if _, dup := s.consumers[consumer]; dup {
		return ErrExists
	}
	if pinned < 1 || pinned > len(s.versions) {
		return ErrNotFound
	}
	view, err := schema.Project(s.versions[pinned-1], fields)
	if err != nil {
		return ErrNotFound
	}
	if _, ok := compat.CanRead(view, s.versions[len(s.versions)-1]); !ok {
		return ErrStillBroken
	}
	s.consumers[consumer] = &subscriber{
		pinned:  s.versions[pinned-1],
		pinnedN: pinned,
		view:    view,
	}
	s.now = now
	return nil
}

// Waive registers an exemption for consumer valid while t < until.
func (r *Registry) Waive(subjName, consumer string, until, now int64) error {
	if !validClock(now) || until <= now {
		return ErrInvalidArgument
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	s, ok := r.subjects[subjName]
	if !ok {
		return ErrNotFound
	}
	if now < s.now {
		return ErrClockSkew
	}
	c, ok := s.consumers[consumer]
	if !ok {
		return ErrNotFound
	}
	c.until = until
	c.hasWaive = true
	s.now = now
	return nil
}

// Publish runs the release gate: no-change, mode compatibility, then
// non-lagging subscriber checks; accepted fields become latest+1.
func (r *Registry) Publish(subjName string, fields []schema.Field, now int64) (int, error) {
	candidate, err := schema.NewVersion(fields)
	if err != nil || !validClock(now) {
		return 0, ErrInvalidArgument
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	s, ok := r.subjects[subjName]
	if !ok {
		return 0, ErrNotFound
	}
	if now < s.now {
		return 0, ErrClockSkew
	}
	latest := s.versions[len(s.versions)-1]
	if candidate.Equal(latest) {
		return 0, ErrNoChange
	}
	if s.mode == schema.Backward || s.mode == schema.Full {
		if v, ok := compat.CanRead(candidate, latest); !ok {
			return 0, &IncompatibleError{Direction: compat.Backward, Violation: v}
		}
	}
	if s.mode == schema.Forward || s.mode == schema.Full {
		if v, ok := compat.CanRead(latest, candidate); !ok {
			return 0, &IncompatibleError{Direction: compat.Forward, Violation: v}
		}
	}

	names := make([]string, 0, len(s.consumers))
	for n := range s.consumers {
		names = append(names, n)
	}
	sort.Strings(names)
	var blockers []Blocker
	var waived []string
	for _, n := range names {
		c := s.consumers[n]
		if c.lagging {
			continue
		}
		v, ok := compat.CanRead(c.view, candidate)
		if ok {
			continue
		}
		if c.hasWaive && now < c.until {
			waived = append(waived, n)
			continue
		}
		blockers = append(blockers, Blocker{Consumer: n, Violation: v})
	}
	if len(blockers) > 0 {
		return 0, &BlockedError{Blockers: blockers}
	}

	newNum := len(s.versions) + 1
	s.versions = append(s.versions, candidate)
	for _, n := range waived {
		c := s.consumers[n]
		c.lagging = true
		c.lagged = newNum
	}
	s.now = now
	return newNum, nil
}

// Advance moves a consumer to a later existing version with a new field
// projection; the new view must read latest. Lagging and waiver state are
// cleared on success.
func (r *Registry) Advance(subjName, consumer string, to int, fields []string, now int64) error {
	if !validNameList(fields) || !validClock(now) {
		return ErrInvalidArgument
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	s, ok := r.subjects[subjName]
	if !ok {
		return ErrNotFound
	}
	if now < s.now {
		return ErrClockSkew
	}
	c, ok := s.consumers[consumer]
	if !ok {
		return ErrNotFound
	}
	if to <= c.pinnedN || to > len(s.versions) {
		return ErrNotFound
	}
	view, err := schema.Project(s.versions[to-1], fields)
	if err != nil {
		return ErrNotFound
	}
	if _, ok := compat.CanRead(view, s.versions[len(s.versions)-1]); !ok {
		return ErrStillBroken
	}
	c.pinned = s.versions[to-1]
	c.pinnedN = to
	c.view = view
	c.lagging = false
	c.lagged = 0
	c.until = 0
	c.hasWaive = false
	s.now = now
	return nil
}

// StatusInfo describes a subscriber's pin and lagging state.
type StatusInfo struct {
	Pinned      int
	Lagging     bool
	LaggedAtVer int
}

// Status returns the consumer's current pin and lagging state.
func (r *Registry) Status(subjName, consumer string) (StatusInfo, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	s, ok := r.subjects[subjName]
	if !ok {
		return StatusInfo{}, ErrNotFound
	}
	c, ok := s.consumers[consumer]
	if !ok {
		return StatusInfo{}, ErrNotFound
	}
	return StatusInfo{Pinned: c.pinnedN, Lagging: c.lagging, LaggedAtVer: c.lagged}, nil
}

func validNameList(names []string) bool {
	if len(names) < 1 || len(names) > 64 {
		return false
	}
	seen := make(map[string]struct{}, len(names))
	for _, n := range names {
		if n == "" {
			return false
		}
		if _, dup := seen[n]; dup {
			return false
		}
		seen[n] = struct{}{}
	}
	return true
}

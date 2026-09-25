// Package hook defines read-only validation hooks and a type-indexed registry.
// A Check observes a frozen snapshot only; it cannot mutate the object.
package hook

import (
	"errors"
	"fmt"

	"ontology/snapshot"
)

// Phase selects when a hook runs relative to the change.
type Phase int

const (
	PhasePre Phase = iota // sees the pre-change (old) snapshot
	PhasePost             // sees the post-change (new) snapshot
)

func (p Phase) String() string {
	switch p {
	case PhasePre:
		return "pre"
	case PhasePost:
		return "post"
	default:
		return "unknown"
	}
}

// ApplyAny marks a hook as applicable to every object type.
const ApplyAny = "*"

var (
	ErrEmptyName = errors.New("hook: name must not be empty")
	ErrNilCheck  = errors.New("hook: check must not be nil")
	ErrDupName   = errors.New("hook: duplicate hook name")
)

// Check is the read-only validation function.
type Check func(snap *snapshot.Snapshot) (pass bool, reason string)

// Hook is a registered validation hook.
type Hook struct {
	name       string
	appliesTo  string
	phase      Phase
	priority   int
	registered int // stable tie-breaker assigned at registration
	check      Check
}

// New validates and constructs a hook. Priority descends: higher runs first.
func New(name, appliesTo string, phase Phase, priority int, check Check) (Hook, error) {
	if name == "" {
		return Hook{}, ErrEmptyName
	}
	if appliesTo == "" {
		appliesTo = ApplyAny
	}
	if phase != PhasePre && phase != PhasePost {
		return Hook{}, fmt.Errorf("hook: invalid phase %d", phase)
	}
	if check == nil {
		return Hook{}, ErrNilCheck
	}
	return Hook{name: name, appliesTo: appliesTo, phase: phase, priority: priority, check: check}, nil
}

func (h Hook) Name() string      { return h.name }
func (h Hook) AppliesTo() string { return h.appliesTo }
func (h Hook) Phase() Phase      { return h.phase }
func (h Hook) Priority() int     { return h.priority }
func (h Hook) Registered() int   { return h.registered }

// Registry indexes hooks by object type so matching cost is independent of the
// total number of registered hooks.
type Registry struct {
	byType map[string][]Hook // keyed bucket, including ApplyAny bucket
	names  map[string]bool
	seq    int

	// lookupVisits counts candidate hooks inspected during Matching. It proves
	// matching does not scan the whole registry.
	lookupVisits int
}

func NewRegistry() *Registry {
	return &Registry{byType: make(map[string][]Hook), names: make(map[string]bool)}
}

// Register adds a hook to its type bucket.
func (r *Registry) Register(h Hook) error {
	if h.check == nil {
		return ErrNilCheck
	}
	if r.names[h.name] {
		return fmt.Errorf("%w: %s", ErrDupName, h.name)
	}
	r.seq++
	h.registered = r.seq
	r.byType[h.appliesTo] = append(r.byType[h.appliesTo], h)
	r.names[h.name] = true
	return nil
}

// MustRegister panics on construction/registration errors.
func (r *Registry) MustRegister(name, appliesTo string, phase Phase, priority int, check Check) Hook {
	h, err := New(name, appliesTo, phase, priority, check)
	if err != nil {
		panic(err)
	}
	if err := r.Register(h); err != nil {
		panic(err)
	}
	return h
}

// Matching returns hooks applicable to objectType: its own bucket plus ApplyAny.
// Only those two buckets are visited, regardless of total registry size.
func (r *Registry) Matching(objectType string) []Hook {
	r.lookupVisits = 0
	out := make([]Hook, 0, len(r.byType[objectType])+len(r.byType[ApplyAny]))
	for _, h := range r.byType[objectType] {
		r.lookupVisits++
		out = append(out, h)
	}
	if objectType != ApplyAny {
		for _, h := range r.byType[ApplyAny] {
			r.lookupVisits++
			out = append(out, h)
		}
	}
	return out
}

// LookupVisits reports candidate hooks inspected by the last Matching call.
func (r *Registry) LookupVisits() int { return r.lookupVisits }

// Run executes the read-only check against a frozen snapshot.
func (h Hook) Run(snap *snapshot.Snapshot) (bool, string) {
	return h.check(snap)
}

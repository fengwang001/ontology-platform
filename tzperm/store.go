package tzperm

import (
	"sort"
	"sync"
)

// ZoneVersion is one effective-dated version of a region default timezone.
type ZoneVersion struct {
	EffectiveFrom int64 // inclusive, Unix seconds UTC
	Zone          *ZoneRules
}

// WindowRules is a daily local-time window. StartSec and EndSec are seconds
// after local midnight.
//
// EndSec > StartSec: a normal same-day interval.
// EndSec < StartSec: the window wraps around local midnight.
// EndSec == StartSec: the window covers the whole day.
//
// Membership is always half-open [StartSec, EndSec): the start boundary is
// inside the window and the end boundary is outside. For a wrap-around
// window this also disambiguates midnight, which belongs to the ending day
// only as the excluded end boundary of the previous day and as the included
// start boundary of the new day.
type WindowRules struct {
	StartSec int
	EndSec   int
}

// Valid reports whether the rule's endpoints are legal second-of-day values.
// It is the "window rule validation" gate used by the engine.
func (w WindowRules) Valid() bool {
	return w.StartSec >= 0 && w.StartSec < 86400 && w.EndSec >= 0 && w.EndSec < 86400
}

// Contains evaluates half-open membership for a local second-of-day.
func (w WindowRules) Contains(sod int) bool {
	switch {
	case w.StartSec == w.EndSec:
		return true
	case w.StartSec < w.EndSec:
		return sod >= w.StartSec && sod < w.EndSec
	default:
		return sod >= w.StartSec || sod < w.EndSec
	}
}

// WindowVersion is one effective-dated version of a window rule set.
type WindowVersion struct {
	EffectiveFrom int64
	Rules         WindowRules
}

// TypeVersion records an object-type schema version. AttrsDeprecated lists
// attributes retired as of that version.
type TypeVersion struct {
	EffectiveFrom   int64
	AttrsDeprecated map[string]bool
}

// TimeAttribute is a stored time-valued property. WallSec is the reading
// annotated at entry time in EntryZone.
type TimeAttribute struct {
	Name      string
	WallSec   int64
	EntryZone *ZoneRules
}

// Object is an ontology object bound to a region and an object type.
type Object struct {
	ID       string
	TypeID   string
	RegionID string
	Attrs    map[string]*TimeAttribute
}

// policy maps an (object type, attribute) pair to its gating window set.
type policy struct {
	windowSetID string
}

// Store holds all platform state. A single RWMutex serializes every
// mutating operation against every read snapshot, which gives the subsystem
// its linearizability argument: each Check executes while holding the write
// side of the lock (it appends an audit record), so the real-time order of
// operations is itself a valid serialization order.
type Store struct {
	mu sync.RWMutex

	regions    map[string][]ZoneVersion
	windowSets map[string][]WindowVersion
	types      map[string][]TypeVersion
	policies   map[policyKey]policy
	objects    map[string]Object
}

type policyKey struct {
	objectType string
	attr       string
}

// PolicyKey is the exported view of an (object type, attribute) policy key.
// It is used by external verification tooling such as the naive reference
// model.
type PolicyKey struct {
	ObjectType string
	Attr       string
}

// PolicyKeyForTest constructs an internal policy key from its exported form.
func PolicyKeyForTest(objectType, attr string) policyKey {
	return policyKey{objectType: objectType, attr: attr}
}

// WindowSetIDForTest exposes a policy's window set identifier.
func (p policy) WindowSetIDForTest() string { return p.windowSetID }

// ExportedPolicies returns the policy map keyed by exported keys.
func (s Snapshot) ExportedPolicies() map[PolicyKey]string {
	out := make(map[PolicyKey]string, len(s.Policies))
	for k, v := range s.Policies {
		out[PolicyKey{ObjectType: k.objectType, Attr: k.attr}] = v.windowSetID
	}
	return out
}

// NewStore creates an empty store.
func NewStore() *Store {
	return &Store{
		regions:    map[string][]ZoneVersion{},
		windowSets: map[string][]WindowVersion{},
		types:      map[string][]TypeVersion{},
		policies:   map[policyKey]policy{},
		objects:    map[string]Object{},
	}
}

// lock helpers used by the engine and by mutators.
func (s *Store) rLock()   { s.mu.RLock() }
func (s *Store) rUnlock() { s.mu.RUnlock() }
func (s *Store) wLock()   { s.mu.Lock() }
func (s *Store) wUnlock() { s.mu.Unlock() }

// UpsertRegion replaces a region's timezone version list. The slice is
// copied and sorted; callers may reuse their copy afterwards.
func (s *Store) UpsertRegion(id string, versions []ZoneVersion) {
	vs := append([]ZoneVersion(nil), versions...)
	sort.Slice(vs, func(i, j int) bool { return vs[i].EffectiveFrom < vs[j].EffectiveFrom })
	s.mu.Lock()
	s.regions[id] = vs
	s.mu.Unlock()
}

// UpsertWindowSet replaces a window-rule version list.
func (s *Store) UpsertWindowSet(id string, versions []WindowVersion) {
	vs := append([]WindowVersion(nil), versions...)
	sort.Slice(vs, func(i, j int) bool { return vs[i].EffectiveFrom < vs[j].EffectiveFrom })
	s.mu.Lock()
	s.windowSets[id] = vs
	s.mu.Unlock()
}

// UpsertObjectType replaces an object-type version list.
func (s *Store) UpsertObjectType(id string, versions []TypeVersion) {
	vs := append([]TypeVersion(nil), versions...)
	sort.Slice(vs, func(i, j int) bool { return vs[i].EffectiveFrom < vs[j].EffectiveFrom })
	s.mu.Lock()
	s.types[id] = vs
	s.mu.Unlock()
}

// BindPolicy declares that viewing attr of objectType requires membership in
// the given window set.
func (s *Store) BindPolicy(objectType, attr, windowSetID string) {
	s.mu.Lock()
	s.policies[policyKey{objectType: objectType, attr: attr}] = policy{windowSetID: windowSetID}
	s.mu.Unlock()
}

// PutObject inserts or replaces an object. Its attribute map is copied.
func (s *Store) PutObject(obj Object) {
	attrs := make(map[string]*TimeAttribute, len(obj.Attrs))
	for k, v := range obj.Attrs {
		attrs[k] = v
	}
	obj.Attrs = attrs
	s.mu.Lock()
	s.objects[obj.ID] = obj
	s.mu.Unlock()
}

// lookupResult is one effective-version lookup together with the number of
// comparisons performed. It is what the engine uses to prove its cost is
// logarithmic in the accumulated version count.
type lookupResult[T any] struct {
	found       bool
	index       int
	comparisons int
}

// lookupEffective returns the latest version whose EffectiveFrom <= t.
// comparisons counts every version timestamp examined, which is
// O(log n+1) and independently testable.
func lookupEffective[T any](versions []T, effectiveFrom func(T) int64, t int64) lookupResult[T] {
	lo, hi := 0, len(versions)
	comparisons := 0
	for lo < hi {
		mid := lo + (hi-lo)/2
		comparisons++
		if effectiveFrom(versions[mid]) <= t {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	if lo == 0 {
		return lookupResult[T]{found: false, comparisons: comparisons}
	}
	return lookupResult[T]{found: true, index: lo - 1, comparisons: comparisons}
}

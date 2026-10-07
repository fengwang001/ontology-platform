package meshauthz

import (
	"sort"
	"sync"
	"sync/atomic"
)

// Store holds the installed policy set and evaluates requests against
// it. The zero value is not usable; construct with NewStore.
//
// Concurrency model: the policy set is published as one immutable
// snapshot through an atomic pointer, so a replacement and any number
// of concurrent evaluations are equivalent to some serial order — an
// evaluation either sees the whole old version or the whole new one,
// never a half-installed set. ReplaceAll calls are serialized by a
// mutex and bump the version monotonically.
type Store struct {
	rootNamespace string

	replaceMu sync.Mutex
	snap      atomic.Pointer[snapshot]

	// examinedPolicies counts how many compiled policies evaluations
	// inspected for applicability. It is an observability hook that
	// makes the evaluation complexity claim directly testable.
	examinedPolicies atomic.Int64
}

// NewStore creates a store whose root namespace is rootNamespace.
// Policies installed in the root namespace apply to every namespace
// in the mesh (their selectors still must be satisfied). The root
// namespace must be non-empty.
func NewStore(rootNamespace string) (*Store, error) {
	if rootNamespace == "" {
		return nil, invalidArgumentf("root namespace must not be empty")
	}
	s := &Store{rootNamespace: rootNamespace}
	s.snap.Store(&snapshot{version: 0, byNS: map[string]*nsBucket{}})
	return s, nil
}

// Version returns the currently installed policy-set version.
func (s *Store) Version() uint64 {
	return s.snap.Load().version
}

// ExaminedPolicies returns the total number of compiled policies that
// all evaluations so far have inspected for applicability. Because
// snapshots are indexed by namespace, this number is independent of
// how many policies exist in namespaces unrelated to the evaluated
// requests, which the package tests assert.
func (s *Store) ExaminedPolicies() int64 {
	return s.examinedPolicies.Load()
}

// ReplaceAll atomically replaces the entire policy set. The candidate
// set is validated and compiled first; any problem rejects the whole
// replacement, leaving the installed version untouched, and the error
// reports the first offending policy in submission order. On success
// the version increases monotonically (by one) and the new version is
// returned. On failure the returned version is the unchanged current
// one.
func (s *Store) ReplaceAll(policies []Policy) (uint64, error) {
	s.replaceMu.Lock()
	defer s.replaceMu.Unlock()

	current := s.snap.Load()
	next, err := buildSnapshot(current.version+1, policies)
	if err != nil {
		return current.version, err
	}
	s.snap.Store(next)
	return next.version, nil
}

// Evaluate decides whether req is allowed and explains the decision.
//
// Request validation runs before any policy state is touched, so an
// illegal request always yields the highest-priority error category.
// Evaluation itself cannot fail because of policy content: policy
// sets are fully validated at replacement time, so every legal
// request gets a decision.
func (s *Store) Evaluate(req *Request) (Result, error) {
	if req == nil {
		return Result{}, invalidArgumentf("request must not be nil")
	}
	if err := validateRequest(req); err != nil {
		return Result{}, err
	}
	// Linearization point: one atomic load pins the complete version
	// this evaluation observes.
	snap := s.snap.Load()
	nreq := normalizeRequest(req)
	return s.evaluate(snap, &nreq), nil
}

// evaluate runs the decision algorithm against one pinned snapshot.
func (s *Store) evaluate(snap *snapshot, req *normalizedRequest) Result {
	targetBucket := snap.byNS[req.req.TargetNamespace]
	rootBucket := snap.byNS[s.rootNamespace]
	if req.req.TargetNamespace == s.rootNamespace {
		// The target namespace IS the root namespace; counting it
		// twice would double-report evidence.
		rootBucket = nil
	}

	examined := 0
	// collectMatched appends the refs of applicable, matched policies
	// from one action bucket. Applicability (selector) is checked
	// before the more expensive rule matching.
	collectMatched := func(list []*compiledPolicy, hits *[]PolicyRef) {
		for _, p := range list {
			examined++
			if !selectorMatches(p.selector, req.req.TargetLabels) {
				continue
			}
			if policyMatches(p, req) {
				*hits = append(*hits, p.ref())
			}
		}
	}

	// 1. Deny first: any matched applicable deny policy decides.
	var denyHits []PolicyRef
	if targetBucket != nil {
		collectMatched(targetBucket.deny, &denyHits)
	}
	if rootBucket != nil {
		collectMatched(rootBucket.deny, &denyHits)
	}

	// Audit evidence never influences the decision; every applicable
	// matched audit policy is listed, sorted by name then namespace.
	var auditHits []PolicyRef
	if targetBucket != nil {
		collectMatched(targetBucket.audit, &auditHits)
	}
	if rootBucket != nil {
		collectMatched(rootBucket.audit, &auditHits)
	}
	sortPolicyRefs(auditHits)

	var allowHits []PolicyRef
	allowExists := false
	scanAllow := func(bucket *nsBucket) {
		if bucket == nil {
			return
		}
		for _, p := range bucket.allow {
			examined++
			if !selectorMatches(p.selector, req.req.TargetLabels) {
				continue
			}
			allowExists = true
			if policyMatches(p, req) {
				allowHits = append(allowHits, p.ref())
			}
		}
	}
	scanAllow(targetBucket)
	scanAllow(rootBucket)

	s.examinedPolicies.Add(int64(examined))

	result := Result{Evidence: Evidence{Version: snap.version, AuditPolicies: auditHits}}
	switch {
	case len(denyHits) > 0:
		result.Decision = DecisionDeny
		sortPolicyRefs(denyHits)
		result.Evidence.DecisionPolicies = denyHits
	case !allowExists:
		// 2. No applicable allow policy at all: default allow, and
		// the decision part of the evidence stays empty.
		result.Decision = DecisionAllow
	case len(allowHits) > 0:
		// 3. At least one applicable allow policy matched.
		result.Decision = DecisionAllow
		sortPolicyRefs(allowHits)
		result.Evidence.DecisionPolicies = allowHits
	default:
		// 4. Allow policies exist but none matched: deny, with an
		// empty decision part in the evidence.
		result.Decision = DecisionDeny
	}
	return result
}

// sortPolicyRefs orders evidence deterministically: by name, then by
// namespace. This is required for audit policies and applied to
// decision policies as well so the whole evidence is reproducible.
func sortPolicyRefs(refs []PolicyRef) {
	sort.Slice(refs, func(i, j int) bool {
		if refs[i].Name != refs[j].Name {
			return refs[i].Name < refs[j].Name
		}
		return refs[i].Namespace < refs[j].Namespace
	})
}

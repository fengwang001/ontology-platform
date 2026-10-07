package meshauthz

// Compiled, immutable policy forms. Snapshots are never mutated after
// construction, which is what makes lock-free atomic publication safe.

type compiledSource struct {
	identities    []pattern
	notIdentities []pattern
	namespaces    []pattern
	notNamespaces []pattern
}

type compiledOperation struct {
	methods    []string
	notMethods []string
	paths      []pattern
	notPaths   []pattern
	ports      []int
	notPorts   []int
}

type compiledCondition struct {
	key       string // lower-cased header name
	values    []string
	notValues []string
}

type compiledRule struct {
	from      *compiledSource
	operation *compiledOperation
	when      []compiledCondition
}

type compiledPolicy struct {
	name      string
	namespace string
	action    Action
	selector  map[string]string
	rules     []compiledRule
}

func (p *compiledPolicy) ref() PolicyRef {
	return PolicyRef{Name: p.name, Namespace: p.namespace, Action: p.action}
}

// snapshot is one complete, immutable policy-set version.
//
// Policies are indexed by namespace so evaluation only ever touches
// the target namespace's bucket plus the root namespace's bucket; the
// number of policies elsewhere in the mesh cannot affect evaluation
// cost. Buckets are split by action up front, so the deny-first
// decision order never scans allow/audit policies and vice versa.
type snapshot struct {
	version uint64
	// byNS maps a namespace to its compiled policies, pre-split by
	// action and kept in submission order for deterministic evidence.
	byNS map[string]*nsBucket
}

type nsBucket struct {
	deny  []*compiledPolicy
	allow []*compiledPolicy
	audit []*compiledPolicy
}

func (b *nsBucket) add(p *compiledPolicy) {
	switch p.action {
	case ActionDeny:
		b.deny = append(b.deny, p)
	case ActionAllow:
		b.allow = append(b.allow, p)
	case ActionAudit:
		b.audit = append(b.audit, p)
	}
}

// buildSnapshot validates and compiles a candidate policy set into a
// new snapshot. It performs no shared-state mutation; the caller
// publishes the result atomically.
func buildSnapshot(version uint64, policies []Policy) (*snapshot, error) {
	if err := validatePolicySet(policies); err != nil {
		return nil, err
	}
	snap := &snapshot{version: version, byNS: make(map[string]*nsBucket)}
	for i := range policies {
		cp, err := compilePolicy(&policies[i], i)
		if err != nil {
			return nil, err
		}
		bucket := snap.byNS[cp.namespace]
		if bucket == nil {
			bucket = &nsBucket{}
			snap.byNS[cp.namespace] = bucket
		}
		bucket.add(cp)
	}
	return snap, nil
}

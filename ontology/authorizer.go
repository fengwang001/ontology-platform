package ontology

import "sync/atomic"

type resolvedRule struct {
	decision Decision
	priority int
	declared bool
}

type subjectSnapshot struct {
	objects map[string]resolvedRule
	links   map[string]resolvedRule
}

type policySnapshot struct {
	subjects map[string]subjectSnapshot
}

type Authorizer struct {
	snapshot atomic.Pointer[policySnapshot]
}

func NewAuthorizer(policy *Policy) (*Authorizer, error) {
	if policy == nil {
		var err error
		policy, err = NewPolicy(nil, nil, nil, nil)
		if err != nil {
			return nil, err
		}
	}
	authorizer := &Authorizer{}
	if err := authorizer.UpdatePolicy(policy); err != nil {
		return nil, err
	}
	return authorizer, nil
}

func (a *Authorizer) UpdatePolicy(policy *Policy) error {
	if policy == nil {
		return ErrInvalidSubject
	}
	a.snapshot.Store(compileSnapshot(policy))
	return nil
}

func compileSnapshot(policy *Policy) *policySnapshot {
	snapshot := &policySnapshot{subjects: make(map[string]subjectSnapshot, len(policy.memberships))}
	for subjectID, groupIDs := range policy.memberships {
		subject := subjectSnapshot{
			objects: make(map[string]resolvedRule),
			links:   make(map[string]resolvedRule),
		}
		for _, groupID := range groupIDs {
			priority := policy.groups[groupID]
			mergeDeclarations(policy.objectDeclarations[groupID], priority, subject.objects)
			mergeDeclarations(policy.linkDeclarations[groupID], priority, subject.links)
		}
		snapshot.subjects[subjectID] = subject
	}
	return snapshot
}

func mergeDeclarations(declarations map[string]Decision, priority int, rules map[string]resolvedRule) {
	for typeID, decision := range declarations {
		current, exists := rules[typeID]
		switch {
		case !exists || priority > current.priority:
			rules[typeID] = resolvedRule{decision: decision, priority: priority, declared: true}
		case priority == current.priority && decision != current.decision:
			rules[typeID] = resolvedRule{decision: DecisionDeny, priority: priority, declared: true}
		}
	}
}

func (a *Authorizer) currentSnapshot() *policySnapshot {
	snapshot := a.snapshot.Load()
	if snapshot != nil {
		return snapshot
	}
	return &policySnapshot{subjects: map[string]subjectSnapshot{}}
}

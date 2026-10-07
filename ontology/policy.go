package ontology

import "fmt"

type Decision int

const (
	DecisionUnspecified Decision = iota
	DecisionAllow
	DecisionDeny
)

type Group struct {
	ID       string
	Priority int
}

type Membership struct {
	SubjectID string
	GroupID   string
}

type ObjectDeclaration struct {
	GroupID      string
	ObjectTypeID string
	Decision     Decision
}

type LinkDeclaration struct {
	GroupID    string
	LinkTypeID string
	Decision   Decision
}

type Policy struct {
	groups             map[string]int
	memberships        map[string][]string
	objectDeclarations map[string]map[string]Decision
	linkDeclarations   map[string]map[string]Decision
}

func NewPolicy(groups []Group, memberships []Membership, objectDeclarations []ObjectDeclaration, linkDeclarations []LinkDeclaration) (*Policy, error) {
	policy := &Policy{
		groups:             make(map[string]int, len(groups)),
		memberships:        make(map[string][]string),
		objectDeclarations: make(map[string]map[string]Decision),
		linkDeclarations:   make(map[string]map[string]Decision),
	}

	for _, group := range groups {
		if !validID(group.ID) {
			return nil, fmt.Errorf("ontology: invalid group identifier")
		}
		if _, exists := policy.groups[group.ID]; exists {
			return nil, fmt.Errorf("ontology: duplicate group ID %q", group.ID)
		}
		policy.groups[group.ID] = group.Priority
	}

	seenMemberships := make(map[string]map[string]bool)
	for _, membership := range memberships {
		if !validID(membership.SubjectID) {
			return nil, ErrInvalidSubject
		}
		if !validID(membership.GroupID) {
			return nil, fmt.Errorf("ontology: invalid membership group identifier")
		}
		if _, exists := policy.groups[membership.GroupID]; !exists {
			return nil, fmt.Errorf("ontology: membership references unknown group %q", membership.GroupID)
		}
		if seenMemberships[membership.SubjectID] == nil {
			seenMemberships[membership.SubjectID] = make(map[string]bool)
		}
		if seenMemberships[membership.SubjectID][membership.GroupID] {
			return nil, fmt.Errorf("ontology: duplicate membership for subject %q and group %q", membership.SubjectID, membership.GroupID)
		}
		seenMemberships[membership.SubjectID][membership.GroupID] = true
		policy.memberships[membership.SubjectID] = append(policy.memberships[membership.SubjectID], membership.GroupID)
	}

	for _, declaration := range objectDeclarations {
		if err := validateGroupedDecision(declaration.GroupID, declaration.ObjectTypeID, declaration.Decision, policy); err != nil {
			return nil, err
		}
		if policy.objectDeclarations[declaration.GroupID] == nil {
			policy.objectDeclarations[declaration.GroupID] = make(map[string]Decision)
		}
		if _, exists := policy.objectDeclarations[declaration.GroupID][declaration.ObjectTypeID]; exists {
			return nil, fmt.Errorf("ontology: duplicate object declaration for group %q and type %q", declaration.GroupID, declaration.ObjectTypeID)
		}
		policy.objectDeclarations[declaration.GroupID][declaration.ObjectTypeID] = declaration.Decision
	}

	for _, declaration := range linkDeclarations {
		if err := validateGroupedDecision(declaration.GroupID, declaration.LinkTypeID, declaration.Decision, policy); err != nil {
			return nil, err
		}
		if policy.linkDeclarations[declaration.GroupID] == nil {
			policy.linkDeclarations[declaration.GroupID] = make(map[string]Decision)
		}
		if _, exists := policy.linkDeclarations[declaration.GroupID][declaration.LinkTypeID]; exists {
			return nil, fmt.Errorf("ontology: duplicate link declaration for group %q and type %q", declaration.GroupID, declaration.LinkTypeID)
		}
		policy.linkDeclarations[declaration.GroupID][declaration.LinkTypeID] = declaration.Decision
	}

	return policy, nil
}

func validateGroupedDecision(groupID string, typeID string, decision Decision, policy *Policy) error {
	if !validID(groupID) {
		return fmt.Errorf("ontology: invalid declaration group identifier")
	}
	if !validID(typeID) {
		return fmt.Errorf("ontology: invalid declared type identifier")
	}
	if decision != DecisionAllow && decision != DecisionDeny {
		return fmt.Errorf("ontology: declaration must be allow or deny")
	}
	if _, exists := policy.groups[groupID]; !exists {
		return fmt.Errorf("ontology: declaration references unknown group %q", groupID)
	}
	return nil
}

func (d Decision) String() string {
	switch d {
	case DecisionAllow:
		return "allow"
	case DecisionDeny:
		return "deny"
	default:
		return "unspecified"
	}
}

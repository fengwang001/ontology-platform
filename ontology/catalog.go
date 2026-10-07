package ontology

import (
	"sort"
	"sync"
)

// PolicyCatalog holds registered policies and serves the candidate set for a
// given (type, subject). Candidate lookup uses per-type, per-subject indexes,
// so the touched policy count depends only on policies that can match the
// subject — never on the total number of registered policies.
type PolicyCatalog struct {
	mu               sync.RWMutex
	types            map[string]*typeIndex
	rowPolicies      []RowPolicy
	propertyPolicies []PropertyPolicy
}

type typeIndex struct {
	rows  []RowPolicy
	props []PropertyPolicy
	// rowBySubject / propBySubject hold, for each explicitly named subject,
	// the ordinal positions of policies selecting that subject. Policies that
	// select every subject (empty Subjects) are in wildcards.
	rowWildcards  []int
	propWildcards []int
	rowBySubject  map[string][]int
	propBySubject map[string][]int
}

// NewPolicyCatalog creates an empty catalog.
func NewPolicyCatalog() *PolicyCatalog {
	return &PolicyCatalog{types: map[string]*typeIndex{}}
}

// RegisterRowPolicy adds a row-level policy.
func (c *PolicyCatalog) RegisterRowPolicy(p RowPolicy) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.rowPolicies = append(c.rowPolicies, p)
	idx := c.indexFor(p.ObjectType)
	pos := len(idx.rows)
	idx.rows = append(idx.rows, p)
	if len(p.Subjects) == 0 {
		idx.rowWildcards = append(idx.rowWildcards, pos)
	} else {
		for _, s := range p.Subjects {
			idx.rowBySubject[s] = append(idx.rowBySubject[s], pos)
		}
	}
	return nil
}

// RegisterPropertyPolicy adds a property-level policy.
func (c *PolicyCatalog) RegisterPropertyPolicy(p PropertyPolicy) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.propertyPolicies = append(c.propertyPolicies, p)
	idx := c.indexFor(p.ObjectType)
	pos := len(idx.props)
	idx.props = append(idx.props, p)
	if len(p.Subjects) == 0 {
		idx.propWildcards = append(idx.propWildcards, pos)
	} else {
		for _, s := range p.Subjects {
			idx.propBySubject[s] = append(idx.propBySubject[s], pos)
		}
	}
	return nil
}

func (c *PolicyCatalog) indexFor(typeName string) *typeIndex {
	idx, ok := c.types[typeName]
	if !ok {
		idx = &typeIndex{rowBySubject: map[string][]int{}, propBySubject: map[string][]int{}}
		c.types[typeName] = idx
	}
	return idx
}

// RowCandidates returns row policies that can apply to typeName/subject.
func (c *PolicyCatalog) RowCandidates(typeName, subject string) []RowPolicy {
	c.mu.RLock()
	defer c.mu.RUnlock()
	idx, ok := c.types[typeName]
	if !ok {
		return nil
	}
	positions := append([]int{}, idx.rowWildcards...)
	positions = append(positions, idx.rowBySubject[subject]...)
	sort.Ints(positions)
	out := make([]RowPolicy, 0, len(positions))
	for _, pos := range positions {
		out = append(out, idx.rows[pos])
	}
	return out
}

// PropertyCandidates returns property policies that can apply to
// typeName/subject.
func (c *PolicyCatalog) PropertyCandidates(typeName, subject string) []PropertyPolicy {
	c.mu.RLock()
	defer c.mu.RUnlock()
	idx, ok := c.types[typeName]
	if !ok {
		return nil
	}
	positions := append([]int{}, idx.propWildcards...)
	positions = append(positions, idx.propBySubject[subject]...)
	sort.Ints(positions)
	out := make([]PropertyPolicy, 0, len(positions))
	for _, pos := range positions {
		out = append(out, idx.props[pos])
	}
	return out
}

// scanRowPolicies returns every row policy for a type; used by the naive
// reference implementation and by observability tooling.
func (c *PolicyCatalog) scanRowPolicies(typeName string) []RowPolicy {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if idx, ok := c.types[typeName]; ok {
		return append([]RowPolicy(nil), idx.rows...)
	}
	return nil
}

// scanPropertyPolicies returns every property policy for a type.
func (c *PolicyCatalog) scanPropertyPolicies(typeName string) []PropertyPolicy {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if idx, ok := c.types[typeName]; ok {
		return append([]PropertyPolicy(nil), idx.props...)
	}
	return nil
}

// totalPolicies returns the total registered policy count (all types). It is
// used by the overhead-observability tests to prove a single adjudication's
// touched count is independent of catalog size.
func (c *PolicyCatalog) totalPolicies() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.rowPolicies) + len(c.propertyPolicies)
}

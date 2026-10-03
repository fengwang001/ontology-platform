package cluster

import "ontology/token"

// New validates limits and constructs an empty merger.
func New(theta, maxTemplates, maxTenants int) (*Merger, error) {
	if theta < 1 || theta > 100 || maxTemplates < 1 || maxTemplates > 100000 || maxTenants < 1 || maxTenants > 10000 {
		return nil, ErrInvalidArgument
	}
	return &Merger{
		theta:        theta,
		maxTemplates: int64(maxTemplates),
		maxTenants:   maxTenants,
		tenants:      make(map[string]*tenantState),
	}, nil
}

// Ingest validates, masks, clusters, and counts one message.
func (m *Merger) Ingest(tenant, msg string) (Result, error) {
	if !validTenant(tenant) {
		return Result{}, ErrInvalidArgument
	}
	tokens, err := token.Parse(msg)
	if err != nil {
		return Result{}, ErrInvalidArgument
	}

	state, err := m.tenantForIngest(tenant)
	if err != nil {
		return Result{}, err
	}

	threshold, generation := m.currentThreshold()
	state.mu.Lock()
	defer state.mu.Unlock()

	key := leafKey{n: len(tokens), first: tokens[0]}
	leaf := state.leaves[key]
	if leaf == nil {
		leaf = &leafGroup{}
		state.leaves[key] = leaf
	}

	leaf.comparisons += int64(len(leaf.templates))
	best := -1
	bestEq := -1
	bestWild := 0
	for i, candidate := range leaf.templates {
		eq := 0
		wild := 0
		for pos, word := range candidate.words {
			if word == "<*>" {
				eq++
				wild++
			} else if word == tokens[pos] {
				eq++
			}
		}
		if eq*100 < len(tokens)*threshold {
			continue
		}
		if best == -1 || eq > bestEq || (eq == bestEq && wild < bestWild) || (eq == bestEq && wild == bestWild && candidate.id < leaf.templates[best].id) {
			best = i
			bestEq = eq
			bestWild = wild
		}
	}

	if best != -1 {
		chosen := leaf.templates[best]
		for i, word := range chosen.words {
			if word != tokens[i] && word != "<*>" {
				chosen.words[i] = "<*>"
			}
		}
		chosen.count++
		return Result{ID: chosen.id}, nil
	}

	if state.nextID >= m.maxTemplates {
		state.overflow++
		return Result{Overflow: true}, nil
	}

	state.nextID++
	created := &templateEntry{
		id:    state.nextID,
		count: 1,
		gen:   generation,
		words: append([]string(nil), tokens...),
	}
	leaf.templates = append(leaf.templates, created)
	return Result{ID: created.id, Created: true}, nil
}

// SetTheta changes the threshold for future Ingest calls.
func (m *Merger) SetTheta(theta int) error {
	if theta < 1 || theta > 100 {
		return ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.theta = theta
	m.gen++
	return nil
}

// Gen returns the current threshold generation.
func (m *Merger) Gen() int64 {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.gen
}

func (m *Merger) tenantForIngest(tenant string) (*tenantState, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if state := m.tenants[tenant]; state != nil {
		return state, nil
	}
	if len(m.tenants) >= m.maxTenants {
		return nil, ErrTenantLimit
	}
	state := &tenantState{leaves: make(map[leafKey]*leafGroup)}
	m.tenants[tenant] = state
	return state, nil
}

func (m *Merger) currentThreshold() (int, int64) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.theta, m.gen
}

func validTenant(tenant string) bool {
	return tenant != "" && len(tenant) <= 64
}

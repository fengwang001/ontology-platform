package ontology

import (
	"sort"
	"time"
)

func (r *Registry) UpdateDefinition(def Definition) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := validateDefinition(def); err != nil {
		return err
	}
	if _, exists := r.defs[def.Name]; !exists {
		return errorf(ErrUndefined, "definition %q is not registered", def.Name)
	}
	r.defs[def.Name] = cloneDefinition(def)
	r.revision++
	now := time.Now()
	visited := make(map[uint64]bool)
	type queueItem struct {
		node *instanceNode
		hop  int
	}
	queue := make([]queueItem, 0)
	for _, node := range r.byDef[def.Name] {
		if !visited[node.id] {
			visited[node.id] = true
			queue = append(queue, queueItem{node: node, hop: 0})
		}
	}
	for len(queue) > 0 {
		item := queue[0]
		queue = queue[1:]
		node := item.node
		wasActive := !node.stale
		node.stale = true
		node.reason = &StaleReason{
			UpdatedDef: def.Name,
			Revision:   r.revision,
			Hop:        item.hop,
			UpdatedAt:  now,
		}
		if wasActive {
			delete(r.active, instanceKey(node.def, node.args))
		}
		for _, dependent := range node.dependents {
			if !visited[dependent.id] {
				visited[dependent.id] = true
				queue = append(queue, queueItem{node: dependent, hop: item.hop + 1})
			}
		}
	}
	return nil
}

func (r *Registry) Cleanup(id uint64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	node, ok := r.instances[id]
	if !ok {
		return errorf(ErrDependency, "instance %d does not exist", id)
	}
	if !node.stale {
		return errorf(ErrDependency, "instance %d is not stale", id)
	}
	for _, dependent := range node.dependents {
		if !dependent.stale {
			return errorf(ErrDependency, "instance %d is still required by active instance %d", id, dependent.id)
		}
	}
	for _, dependency := range node.deps {
		delete(dependency.dependents, node.id)
	}
	for _, dependent := range node.dependents {
		delete(dependent.deps, node.id)
	}
	delete(r.byDef[node.def], node.id)
	delete(r.instances, node.id)
	return nil
}

func (r *Registry) Get(id uint64) (*Instance, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	node, ok := r.instances[id]
	if !ok {
		return nil, false
	}
	return r.snapshotInstance(node), true
}

func (r *Registry) Summary() Summary {
	r.mu.Lock()
	defer r.mu.Unlock()
	summary := Summary{
		StaleInstances:    0,
		InstancesByDef:    make(map[string]int, len(r.byDef)),
		CumulativeHits:    r.hits,
		CumulativeCreates: r.creates,
		Revision:          r.revision,
	}
	for def, instances := range r.byDef {
		summary.InstancesByDef[def] = len(instances)
	}
	for _, node := range r.instances {
		if node.stale {
			summary.StaleInstances++
		} else {
			summary.ActiveInstances++
		}
	}
	return summary
}

func (r *Registry) snapshotInstance(node *instanceNode) *Instance {
	dependencies := make([]string, 0, len(node.deps))
	for _, dependency := range node.deps {
		dependencies = append(dependencies, instanceKey(dependency.def, dependency.args))
	}
	sort.Strings(dependencies)
	dependents := make([]string, 0, len(node.dependents))
	for _, dependent := range node.dependents {
		dependents = append(dependents, instanceKey(dependent.def, dependent.args))
	}
	sort.Strings(dependents)
	var reason *StaleReason
	if node.reason != nil {
		copiedReason := *node.reason
		reason = &copiedReason
	}
	return &Instance{
		ID:           node.id,
		Def:          node.def,
		Args:         copyArgs(node.args),
		Result:       node.result,
		Dependencies: dependencies,
		Dependents:   dependents,
		Stale:        node.stale,
		StaleReason:  reason,
		CreatedAt:    node.createdAt,
	}
}

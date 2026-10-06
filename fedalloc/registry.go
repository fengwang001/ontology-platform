package fedalloc

import "sync"

// Registry is the concurrently safe, dynamically modifiable cluster registry.
type Registry struct {
	mu sync.RWMutex
	// clusters is the registered, name-indexed live configuration.
	clusters map[string]Cluster
	// tombstones holds one final load reading per deleted cluster so that its
	// replicas are forced to migrate on the next successful allocation.
	tombstones map[string]int64
}

// NewRegistry creates an empty registry.
func NewRegistry() *Registry {
	return &Registry{clusters: map[string]Cluster{}, tombstones: map[string]int64{}}
}

// Upsert registers a new cluster or atomically replaces an existing one.
func (r *Registry) Upsert(c Cluster) *AllocationError {
	if err := validateClusterConfig(c); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.clusters[c.Name] = c
	delete(r.tombstones, c.Name)
	return nil
}

// Delete removes a cluster; missing names are an invalid-argument error.
func (r *Registry) Delete(name string) *AllocationError {
	if name == "" {
		return newError(KindInvalidArgument, "cluster name must not be empty")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	c, ok := r.clusters[name]
	if !ok {
		return newError(KindInvalidArgument, "cluster not registered: "+name)
	}
	delete(r.clusters, name)
	if c.CurrentReplicas > 0 {
		r.tombstones[name] = c.CurrentReplicas
	}
	return nil
}

// Allocate evaluates a request against a consistent point-in-time snapshot
// and returns the plan without mutating any registered state.
func (r *Registry) Allocate(total int64) (AllocationResult, *AllocationError) {
	r.mu.RLock()
	views := make([]clusterView, 0, len(r.clusters)+len(r.tombstones))
	for _, c := range r.clusters {
		views = append(views, toView(c))
	}
	for name, load := range r.tombstones {
		views = append(views, clusterView{
			name:            name,
			cap:             0,
			available:       false,
			currentReplicas: load,
		})
	}
	r.mu.RUnlock()

	targets, err := allocate(views, total)
	if err != nil {
		return AllocationResult{}, err
	}
	return buildPlan(views, targets, total), nil
}

// Commit applies the last successful allocation to current replica counts and
// clears tombstones. Allocate itself never mutates registered state.
func (r *Registry) Commit(result AllocationResult) *AllocationError {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, t := range result.Targets {
		if c, ok := r.clusters[t.Name]; ok {
			c.CurrentReplicas = t.Replicas
			r.clusters[t.Name] = c
		}
		delete(r.tombstones, t.Name)
	}
	return nil
}

// AllocateAndCommit atomically computes a plan against a consistent snapshot
// and applies it. A rejected request mutates no registered state.
func (r *Registry) AllocateAndCommit(total int64) (AllocationResult, *AllocationError) {
	r.mu.Lock()
	defer r.mu.Unlock()

	views := make([]clusterView, 0, len(r.clusters)+len(r.tombstones))
	for _, c := range r.clusters {
		views = append(views, toView(c))
	}
	for name, load := range r.tombstones {
		views = append(views, clusterView{
			name:            name,
			cap:             0,
			available:       false,
			currentReplicas: load,
		})
	}

	targets, err := allocate(views, total)
	if err != nil {
		return AllocationResult{}, err
	}
	result := buildPlan(views, targets, total)

	for _, t := range result.Targets {
		if c, ok := r.clusters[t.Name]; ok {
			c.CurrentReplicas = t.Replicas
			r.clusters[t.Name] = c
		}
		delete(r.tombstones, t.Name)
	}
	return result, nil
}

// Snapshot returns a point-in-time deep copy of all registered clusters.
// Deleted clusters retained as tombstones are reported as unavailable with
// capacity zero.
func (r *Registry) Snapshot() []Cluster {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Cluster, 0, len(r.clusters)+len(r.tombstones))
	for _, c := range r.clusters {
		out = append(out, c)
	}
	for name, load := range r.tombstones {
		out = append(out, Cluster{
			Name:            name,
			Available:       false,
			Capacity:        0,
			CurrentReplicas: load,
		})
	}
	return out
}

package fedalloc

// clusterView is the immutable, allocation-time view of one cluster.
type clusterView struct {
	name            string
	weight          int64
	minReplicas     int64
	cap             int64 // effective cap: min(maxReplicas, capacity)
	available       bool
	currentReplicas int64
}

// validateClusterConfig checks registration-time legality of a cluster.
// Dynamic registration rejects: negative weight, negative minimums,
// negative capacity, minimum greater than maximum, etc.
func validateClusterConfig(c Cluster) *AllocationError {
	if c.Name == "" {
		return newError(KindInvalidArgument, "cluster name must not be empty")
	}
	if c.Weight < 0 {
		return newError(KindInvalidArgument, "cluster "+c.Name+": weight must not be negative")
	}
	if c.MinReplicas < 0 {
		return newError(KindInvalidArgument, "cluster "+c.Name+": minReplicas must not be negative")
	}
	if c.Capacity < 0 {
		return newError(KindInvalidArgument, "cluster "+c.Name+": capacity must not be negative")
	}
	if c.CurrentReplicas < 0 {
		return newError(KindInvalidArgument, "cluster "+c.Name+": currentReplicas must not be negative")
	}
	if c.MaxReplicas != nil {
		if *c.MaxReplicas < 0 {
			return newError(KindInvalidArgument, "cluster "+c.Name+": maxReplicas must not be negative")
		}
		if *c.MaxReplicas < c.MinReplicas {
			return newError(KindInvalidArgument, "cluster "+c.Name+": minReplicas must not exceed maxReplicas")
		}
	}
	return nil
}

// toView converts a registered cluster into an immutable allocation view.
func toView(c Cluster) clusterView {
	cap := c.Capacity
	if c.MaxReplicas != nil && *c.MaxReplicas < cap {
		cap = *c.MaxReplicas
	}
	return clusterView{
		name:            c.Name,
		weight:          c.Weight,
		minReplicas:     c.MinReplicas,
		cap:             cap,
		available:       c.Available,
		currentReplicas: c.CurrentReplicas,
	}
}

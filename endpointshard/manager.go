package endpointshard

import (
	"sync"
)

// Manager owns every service. The service registry is guarded by its
// own lock; each service additionally has its own lock, so operations
// on different services proceed in parallel while operations on one
// service are linearizable (a Sync/Resize and a Query of the same
// service never interleave).
type Manager struct {
	mu       sync.RWMutex
	services map[string]*serviceState
	stats    *Stats
}

// NewManager returns an empty Manager.
func NewManager() *Manager {
	return &Manager{
		services: make(map[string]*serviceState),
		stats:    &Stats{},
	}
}

// Stats returns a snapshot of the operation counters.
func (m *Manager) Stats() StatsSnapshot { return m.stats.snapshot() }

// ResetStats zeroes the operation counters.
func (m *Manager) ResetStats() { m.stats.reset() }

func (m *Manager) get(name string) *serviceState {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.services[name]
}

// CreateService creates a service with the given shard capacity.
// Errors, in priority order: ErrInvalidArgument (empty name or
// non-positive capacity), ErrServiceAlreadyExists.
func (m *Manager) CreateService(name string, capacity int) error {
	if name == "" {
		return invalidArg("CreateService", name, "service name must not be empty")
	}
	if capacity <= 0 {
		return invalidArg("CreateService", name, "capacity must be positive, got %d", capacity)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.services[name]; ok {
		return alreadyExists("CreateService", name)
	}
	m.services[name] = newServiceState(capacity, m.stats)
	return nil
}

// DeleteService removes a service and all of its state.
// Errors: ErrInvalidArgument (empty name), ErrServiceNotFound.
func (m *Manager) DeleteService(name string) error {
	if name == "" {
		return invalidArg("DeleteService", name, "service name must not be empty")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.services[name]; !ok {
		return notFound("DeleteService", name)
	}
	delete(m.services, name)
	return nil
}

// validateDesired enforces the argument rules of Sync before any
// service lookup, so ErrInvalidArgument always wins over
// ErrServiceNotFound.
func validateDesired(op, name string, desired []Endpoint) error {
	seen := make(map[string]struct{}, len(desired))
	for _, ep := range desired {
		if ep.ID == "" {
			return invalidArg(op, name, "endpoint ID must not be empty")
		}
		if ep.Region == "" {
			return invalidArg(op, name, "endpoint %q: region must not be empty", ep.ID)
		}
		if _, dup := seen[ep.ID]; dup {
			return invalidArg(op, name, "duplicate endpoint ID %q", ep.ID)
		}
		seen[ep.ID] = struct{}{}
	}
	return nil
}

// Sync adjusts the service's shards to match desired exactly and
// returns the change report. Errors, in priority order:
// ErrInvalidArgument, ErrServiceNotFound. A rejected Sync changes no
// state, including shard numbering and generations.
func (m *Manager) Sync(name string, desired []Endpoint) (*SyncReport, error) {
	if err := validateDesired("Sync", name, desired); err != nil {
		return nil, err
	}
	svc := m.get(name)
	if svc == nil {
		return nil, notFound("Sync", name)
	}
	svc.mu.Lock()
	defer svc.mu.Unlock()
	return svc.syncLocked(desired), nil
}

// Resize changes the shard capacity of a service and returns the
// change report of the atomic adjustment (empty when nothing moved).
// Errors, in priority order: ErrInvalidArgument (non-positive
// capacity), ErrServiceNotFound.
func (m *Manager) Resize(name string, capacity int) (*SyncReport, error) {
	if capacity <= 0 {
		return nil, invalidArg("Resize", name, "capacity must be positive, got %d", capacity)
	}
	svc := m.get(name)
	if svc == nil {
		return nil, notFound("Resize", name)
	}
	svc.mu.Lock()
	defer svc.mu.Unlock()
	return svc.resizeLocked(capacity), nil
}

// Query answers a consumer read for a region. The result is a
// consistent view of the state either before or after any concurrent
// Sync. Errors: ErrServiceNotFound.
func (m *Manager) Query(name, region string) (*QueryResult, error) {
	svc := m.get(name)
	if svc == nil {
		return nil, notFound("Query", name)
	}
	svc.mu.RLock()
	defer svc.mu.RUnlock()
	return svc.queryLocked(region), nil
}

// Inspect returns a consistent snapshot of the service's shards,
// ordered by shard number. Intended for tests, debugging and
// observability. Errors: ErrServiceNotFound.
func (m *Manager) Inspect(name string) ([]ShardInfo, error) {
	svc := m.get(name)
	if svc == nil {
		return nil, notFound("Inspect", name)
	}
	svc.mu.RLock()
	defer svc.mu.RUnlock()
	return svc.inspectLocked(), nil
}

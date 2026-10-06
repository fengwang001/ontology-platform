package mesh

import "sync"

// subset holds registered endpoints and a readiness cache so request
// routing costs O(1) and never depends on endpoint count.
type subset struct {
	endpoints  []Endpoint
	readyCount int
}

// Registry tracks subset registrations for one service. Mutations are
// serialized; readiness lookups during routing are read-locked and O(1).
type Registry struct {
	mu      sync.RWMutex
	subsets map[string]*subset
}

// NewRegistry creates an empty registry.
func NewRegistry() *Registry {
	return &Registry{subsets: map[string]*subset{}}
}

// Register creates or replaces one subset definition atomically.
func (r *Registry) Register(def SubsetDef) *Error {
	if def.Name == "" {
		return &Error{Kind: KindInvalidArgument, Message: "empty subset name"}
	}
	s := &subset{endpoints: make([]Endpoint, len(def.Endpoints))}
	copy(s.endpoints, def.Endpoints)
	for i := range s.endpoints {
		if s.endpoints[i].Ready {
			s.readyCount++
		}
	}
	r.mu.Lock()
	r.subsets[def.Name] = s
	r.mu.Unlock()
	return nil
}

// SetReady flips the readiness of one endpoint (matched by address).
func (r *Registry) SetReady(subsetName, addr string, ready bool) *Error {
	r.mu.Lock()
	defer r.mu.Unlock()
	s, ok := r.subsets[subsetName]
	if !ok {
		return &Error{Kind: KindInvalidArgument, Message: "unknown subset: " + subsetName}
	}
	for i := range s.endpoints {
		if s.endpoints[i].Addr == addr {
			if s.endpoints[i].Ready != ready {
				s.endpoints[i].Ready = ready
				if ready {
					s.readyCount++
				} else {
					s.readyCount--
				}
			}
			return nil
		}
	}
	return &Error{Kind: KindInvalidArgument, Message: "unknown endpoint: " + addr}
}

// hasReadyEndpoint reports whether the subset is registered and has at
// least one ready endpoint. O(1), independent of endpoint count.
func (r *Registry) hasReadyEndpoint(name string) bool {
	r.mu.RLock()
	s := r.subsets[name]
	ok := s != nil && s.readyCount > 0
	r.mu.RUnlock()
	return ok
}

package budget

import (
	"errors"
	"sync"

	"ontology/cluster"
)

var (
	ErrInvalidArgument = errors.New("invalid argument")
	ErrUnauthorized    = errors.New("unauthorized")
	ErrTenantNotFound  = errors.New("tenant not found")
)

// TemplateInfo is an authorized template snapshot.
type TemplateInfo struct {
	ID    int64
	Text  string
	Count int64
	Gen   int64
}

// Service enforces caller-to-tenant read grants.
type Service struct {
	mu      sync.RWMutex
	merger  *cluster.Merger
	granted map[[2]string]struct{}
}

// New creates an authorization service around a merger.
func New(merger *cluster.Merger) *Service {
	return &Service{
		merger:  merger,
		granted: make(map[[2]string]struct{}),
	}
}

// Grant records that caller may read tenant's templates.
func (s *Service) Grant(caller, tenant string) error {
	if caller == "" || tenant == "" {
		return ErrInvalidArgument
	}
	key := [2]string{caller, tenant}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.granted[key] = struct{}{}
	return nil
}

// Templates returns templates only after exact caller-tenant authorization.
func (s *Service) Templates(caller, tenant string) ([]TemplateInfo, int64, error) {
	if caller == "" || tenant == "" {
		return nil, 0, ErrInvalidArgument
	}
	key := [2]string{caller, tenant}
	s.mu.RLock()
	_, allowed := s.granted[key]
	s.mu.RUnlock()
	if !allowed {
		return nil, 0, ErrUnauthorized
	}

	infos, overflow, err := s.merger.Templates(tenant)
	if err != nil {
		if errors.Is(err, cluster.ErrTenantNotFound) {
			return nil, 0, ErrTenantNotFound
		}
		return nil, 0, ErrInvalidArgument
	}
	result := make([]TemplateInfo, len(infos))
	for i, info := range infos {
		result[i] = TemplateInfo(info)
	}
	return result, overflow, nil
}

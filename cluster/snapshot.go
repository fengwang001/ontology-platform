package cluster

import (
	"errors"
	"strings"
	"sync"
)

var (
	ErrInvalidArgument = errors.New("invalid argument")
	ErrTenantLimit     = errors.New("tenant limit reached")
	ErrTenantNotFound  = errors.New("tenant not found")
)

// Result identifies the destination of one ingested message.
type Result struct {
	ID       int64
	Created  bool
	Overflow bool
}

// TemplateInfo is one tenant-scoped log template.
type TemplateInfo struct {
	ID    int64
	Text  string
	Count int64
	Gen   int64
}

type leafKey struct {
	n     int
	first string
}

type templateEntry struct {
	id    int64
	count int64
	gen   int64
	words []string
}

type leafGroup struct {
	templates   []*templateEntry
	comparisons int64
}

type tenantState struct {
	mu       sync.Mutex
	nextID   int64
	overflow int64
	leaves   map[leafKey]*leafGroup
}

// Merger groups masked logs into tenant-isolated templates.
type Merger struct {
	mu           sync.RWMutex
	theta        int
	maxTemplates int64
	maxTenants   int
	gen          int64
	tenants      map[string]*tenantState
}

// Templates returns a tenant's templates ordered by ID and its overflow count.
func (m *Merger) Templates(tenant string) ([]TemplateInfo, int64, error) {
	m.mu.RLock()
	state := m.tenants[tenant]
	m.mu.RUnlock()
	if state == nil {
		return nil, 0, ErrTenantNotFound
	}

	state.mu.Lock()
	defer state.mu.Unlock()

	infos := make([]TemplateInfo, 0, state.nextID)
	for _, leaf := range state.leaves {
		for _, entry := range leaf.templates {
			infos = append(infos, TemplateInfo{
				ID:    entry.id,
				Text:  strings.Join(entry.words, " "),
				Count: entry.count,
				Gen:   entry.gen,
			})
		}
	}
	sortTemplates(infos)
	return infos, state.overflow, nil
}

func sortTemplates(infos []TemplateInfo) {
	for i := 1; i < len(infos); i++ {
		for j := i; j > 0 && infos[j-1].ID > infos[j].ID; j-- {
			infos[j-1], infos[j] = infos[j], infos[j-1]
		}
	}
}

func (m *Merger) leafComparisons(tenant string) map[string]int64 {
	m.mu.RLock()
	state := m.tenants[tenant]
	m.mu.RUnlock()
	if state == nil {
		return nil
	}

	state.mu.Lock()
	defer state.mu.Unlock()
	result := make(map[string]int64, len(state.leaves))
	for key, leaf := range state.leaves {
		result[key.first] += leaf.comparisons
	}
	return result
}

func (m *Merger) resetLeafComparisons(tenant string) {
	m.mu.RLock()
	state := m.tenants[tenant]
	m.mu.RUnlock()
	if state == nil {
		return
	}

	state.mu.Lock()
	defer state.mu.Unlock()
	for _, leaf := range state.leaves {
		leaf.comparisons = 0
	}
}

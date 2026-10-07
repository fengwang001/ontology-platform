package ontology

import (
	"fmt"
	"sort"
	"sync"
)

type Store struct {
	mu sync.Mutex

	objects map[string]struct{}
	links   []Link
	out     map[string][]Link
	in      map[string][]Link
	configs map[string]LinkTypeConfig

	nextRequestID   int64
	lastDedupProbes int
	logs            []LogEntry
}

func NewStore() *Store {
	return &Store{
		objects: make(map[string]struct{}),
		out:     make(map[string][]Link),
		in:      make(map[string][]Link),
		configs: make(map[string]LinkTypeConfig),
	}
}

func (s *Store) RegisterLinkType(config LinkTypeConfig) error {
	if config.Name == "" {
		return fmt.Errorf("ontology: link type name is required")
	}
	if !validAction(config.OnDelete) {
		return fmt.Errorf("ontology: invalid delete action %q", config.OnDelete)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.configs[config.Name] = config
	return nil
}

func (s *Store) CreateObject(id string) error {
	if id == "" {
		return fmt.Errorf("ontology: object id is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.objects[id]; exists {
		return fmt.Errorf("ontology: object %q already exists", id)
	}
	s.objects[id] = struct{}{}
	return nil
}

func (s *Store) AddLink(link Link) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.addLinkLocked(link)
}

func (s *Store) HasObject(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.objects[id]
	return ok
}

func (s *Store) Links() []Link {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Link(nil), s.links...)
}

func (s *Store) DeleteObjectWithOrder(id string, deterministic bool) (DeleteResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.objects[id]; !ok {
		s.recordFailureLocked(id, ErrObjectNotFound, nil)
		return DeleteResult{}, fmt.Errorf("%w: %q", ErrObjectNotFound, id)
	}
	plan, err := s.planDeleteOrderedLocked(id, deterministic)
	if err != nil {
		if plan != nil {
			s.lastDedupProbes = plan.dedupProbes
		}
		s.recordFailureLocked(id, err, plan)
		return DeleteResult{}, err
	}
	s.lastDedupProbes = plan.dedupProbes
	s.applyPlanLocked(plan)
	result := plan.result()
	s.recordSuccessLocked(id, plan, result)
	return result, nil
}

func (s *Store) LastDedupProbes() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.logs) == 0 {
		return 0
	}
	return s.lastDedupProbes
}

func (s *Store) loadRawLinkForTest(link Link) {
	s.links = append(s.links, link)
	s.out[link.Source] = append(s.out[link.Source], link)
	s.in[link.Target] = append(s.in[link.Target], link)
}

func (s *Store) addLinkLocked(link Link) error {
	if link.Type == "" {
		return fmt.Errorf("ontology: link type is required")
	}
	if link.Source == "" || link.Target == "" {
		return fmt.Errorf("ontology: link endpoints are required")
	}
	if _, ok := s.configs[link.Type]; !ok {
		return fmt.Errorf("%w: %q", ErrUndefinedLinkType, link.Type)
	}
	if _, ok := s.objects[link.Source]; !ok {
		return fmt.Errorf("%w: source %q", ErrObjectNotFound, link.Source)
	}
	if _, ok := s.objects[link.Target]; !ok {
		return fmt.Errorf("%w: target %q", ErrObjectNotFound, link.Target)
	}
	for _, existing := range s.out[link.Source] {
		if existing == link {
			return nil
		}
	}
	s.links = append(s.links, link)
	s.out[link.Source] = append(s.out[link.Source], link)
	s.in[link.Target] = append(s.in[link.Target], link)
	return nil
}

func (s *Store) DeleteObject(id string) (DeleteResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.objects[id]; !ok {
		s.recordFailureLocked(id, ErrObjectNotFound, nil)
		return DeleteResult{}, fmt.Errorf("%w: %q", ErrObjectNotFound, id)
	}
	plan, err := s.planDeleteLocked(id)
	if err != nil {
		if plan != nil {
			s.lastDedupProbes = plan.dedupProbes
		}
		s.recordFailureLocked(id, err, plan)
		return DeleteResult{}, err
	}
	s.lastDedupProbes = plan.dedupProbes
	s.applyPlanLocked(plan)
	result := plan.result()
	s.recordSuccessLocked(id, plan, result)
	return result, nil
}

func (s *Store) Logs() []LogEntry {
	s.mu.Lock()
	defer s.mu.Unlock()
	logs := make([]LogEntry, len(s.logs))
	copy(logs, s.logs)
	return logs
}

func (s *Store) Snapshot() (map[string]struct{}, []Link, map[string]LinkTypeConfig) {
	s.mu.Lock()
	defer s.mu.Unlock()
	objects := make(map[string]struct{}, len(s.objects))
	for id := range s.objects {
		objects[id] = struct{}{}
	}
	links := append([]Link(nil), s.links...)
	configs := make(map[string]LinkTypeConfig, len(s.configs))
	for name, config := range s.configs {
		configs[name] = config
	}
	return objects, links, configs
}

func sortedLinks(links []Link) []Link {
	result := append([]Link(nil), links...)
	sort.Slice(result, func(i, j int) bool {
		if result[i].Type != result[j].Type {
			return result[i].Type < result[j].Type
		}
		if result[i].Source != result[j].Source {
			return result[i].Source < result[j].Source
		}
		return result[i].Target < result[j].Target
	})
	return result
}

func validAction(action DeleteAction) bool {
	return action == Cascade || action == SetNull || action == Restrict
}

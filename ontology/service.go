package ontology

import "fmt"
import "sync"

type Service struct {
	mu       sync.Mutex
	repo     *repository
	lineage  *lineageResolver
	results  map[blameKey][]Attribution
	accepted int
	rejected int
}

type blameKey struct {
	commitID    string
	path        string
	listVersion int
}

func NewService() *Service {
	repo := newRepository()
	return &Service{
		repo:    repo,
		lineage: newLineageResolver(),
		results: make(map[blameKey][]Attribution),
	}
}

func (s *Service) Load(input CommitInput) error {
	cloned := cloneCommitInput(input)
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.repo.addCommit(cloned)
}

func cloneCommitInput(input CommitInput) CommitInput {
	files := make(map[string]string, len(input.Files))
	for path, content := range input.Files {
		files[path] = content
	}
	parents := append([]string(nil), input.ParentIDs...)
	renames := append([]Rename(nil), input.Renames...)
	return CommitInput{
		ID:        input.ID,
		ParentIDs: parents,
		Files:     files,
		Renames:   renames,
	}
}

func (s *Service) AddIgnoreList(commitIDs []string) (int, error) {
	ids := append([]string(nil), commitIDs...)
	for _, id := range ids {
		if id == "" {
			return 0, fmt.Errorf("%w: empty ignored commit id", ErrInvalidArgument)
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.repo.addIgnoreList(ids)
}

func (s *Service) Blame(commitID, path string, listVersion int) ([]Attribution, error) {
	if commitID == "" || path == "" || listVersion < 0 {
		s.rejected++
		return nil, fmt.Errorf("%w: empty commit id, empty path, or negative list version", ErrInvalidArgument)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	cm, ok := s.repo.commits[commitID]
	if !ok {
		s.rejected++
		return nil, fmt.Errorf("%w: %s", ErrCommitNotFound, commitID)
	}
	if listVersion >= len(s.repo.lists) {
		s.rejected++
		return nil, fmt.Errorf("%w: %d", ErrListVersionNotFound, listVersion)
	}
	if _, ok := cm.files[path]; !ok {
		s.rejected++
		return nil, fmt.Errorf("%w: %s", ErrPathNotFound, path)
	}

	key := blameKey{commitID: commitID, path: path, listVersion: listVersion}
	if cached, ok := s.results[key]; ok {
		s.accepted++
		return append([]Attribution(nil), cached...), nil
	}
	raw := s.lineage.states(cm, path)
	ignored := s.repo.lists[listVersion]
	answer := make([]Attribution, len(raw))
	for i, state := range raw {
		answer[i] = state.attribute(ignored)
	}
	s.results[key] = answer
	s.accepted++
	return append([]Attribution(nil), answer...), nil
}

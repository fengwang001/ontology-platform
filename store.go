package retention

import (
	"sync"
)

type Store struct {
	mu           sync.RWMutex
	policy       Policy
	lastNow      int64
	commits      map[ID]*Commit
	content      map[ID]*Content
	refs         map[string]*refState
	pins         map[ID]int
	schedule     scheduleHeap
	ancestors    map[ID]map[ID]bool
	deadCommits  map[ID]bool
	deadContents map[ID]bool
}

func NewStore(policy Policy) (*Store, error) {
	if policy.ReachableRetention < 0 || policy.UnreachableRetention < 0 || policy.FreshGrace < 0 {
		return nil, ErrInvalidArgument
	}
	return &Store{
		policy:       policy,
		commits:      make(map[ID]*Commit),
		content:      make(map[ID]*Content),
		refs:         make(map[string]*refState),
		pins:         make(map[ID]int),
		ancestors:    make(map[ID]map[ID]bool),
		deadCommits:  map[ID]bool{},
		deadContents: map[ID]bool{},
	}, nil
}

func (s *Store) AddCommit(commit Commit) error {
	if commit.ID == "" || commit.Created < 0 || commit.Written < 0 {
		return ErrInvalidArgument
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.commits[commit.ID]; exists {
		return ErrInvalidArgument
	}
	if s.deadCommits[commit.ID] {
		return ErrInvalidArgument
	}
	for _, contentID := range commit.Contents {
		if s.deadContents[contentID] {
			return ErrInvalidArgument
		}
		if _, exists := s.content[contentID]; !exists {
			return ErrInvalidArgument
		}
	}
	for _, parent := range commit.Parents {
		if _, exists := s.commits[parent]; !exists {
			return ErrCommitNotFound
		}
	}
	if err := s.advanceClock(commit.Written); err != nil {
		return err
	}
	copied := commit
	copied.Parents = append([]ID(nil), commit.Parents...)
	copied.Contents = append([]ID(nil), commit.Contents...)
	s.commits[copied.ID] = &copied
	return nil
}

func (s *Store) AddContent(content Content) error {
	if content.ID == "" || content.Size < 0 || content.Written < 0 {
		return ErrInvalidArgument
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.content[content.ID]; exists {
		return ErrInvalidArgument
	}
	if s.deadContents[content.ID] {
		return ErrInvalidArgument
	}
	if err := s.advanceClock(content.Written); err != nil {
		return err
	}
	copied := content
	s.content[copied.ID] = &copied
	return nil
}

func (s *Store) Set(name string, commit ID, now int64, operator string) error {
	if name == "" || commit == "" || now < 0 {
		return ErrInvalidArgument
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.advanceClock(now); err != nil {
		return err
	}
	if _, exists := s.commits[commit]; !exists {
		return ErrCommitNotFound
	}
	return s.appendRefRecord(name, commit, now, operator)
}

func (s *Store) Delete(name string, now int64, operator string) error {
	if name == "" || now < 0 {
		return ErrInvalidArgument
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.advanceClock(now); err != nil {
		return err
	}
	if _, exists := s.refs[name]; !exists {
		return ErrRefNotFound
	}
	return s.appendRefRecord(name, "", now, operator)
}

func (s *Store) History(name string, reverseIndex int) (Record, error) {
	if name == "" || reverseIndex <= 0 {
		return Record{}, ErrInvalidArgument
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	state, exists := s.refs[name]
	if !exists {
		return Record{}, ErrRefNotFound
	}
	if reverseIndex > len(state.active) {
		return Record{}, ErrRecordNotFound
	}
	return state.active[len(state.active)-reverseIndex].record, nil
}

func (s *Store) RefExisted(name string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, exists := s.refs[name]
	return exists
}

func (s *Store) Expire(now int64) error {
	if now < 0 {
		return ErrInvalidArgument
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.advanceClock(now); err != nil {
		return err
	}
	s.expireLocked(now)
	return nil
}

func (s *Store) advanceClock(now int64) error {
	if now < s.lastNow {
		return ErrClockMovedBack
	}
	s.lastNow = now
	return nil
}

func (s *Store) expireLocked(now int64) {
	for {
		entry, _ := s.popCurrentDue(now)
		if entry == nil {
			break
		}
		state := entry.state
		s.removeEntry(state, entry)
		s.releaseRecordRoot(state, entry)
	}
}

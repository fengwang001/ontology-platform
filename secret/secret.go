package secret

import (
	"errors"
	"strings"
	"sync"
)

var (
	ErrInvalidArgument = errors.New("invalid argument")
	ErrNotFound        = errors.New("not found")
	ErrAlreadyExists   = errors.New("already exists")
)

type Visibility int

const (
	All Visibility = iota
	Private
	Selected
)

type Level int

const (
	EnvLevel Level = iota
	RepoLevel
	OrgLevel
)

type Entry struct {
	Value         string
	Level         Level
	Version       int
	ProtectedOnly bool
}

// OrgEntry is an organization-level secret definition.
type OrgEntry struct {
	Value         string
	Version       int
	Vis           Visibility
	Repos         []string
	ProtectedOnly bool
}

type secret struct {
	value         string
	version       int
	protectedOnly bool
}

type env struct {
	branches []string
	secrets  map[string]*secret
}

type repo struct {
	private  bool
	patterns []string
	secrets  map[string]*secret
	envs     map[string]*env
}

// Store is the three-level (org, repo, env) secret store.
type Store struct {
	mu      sync.RWMutex
	org     map[string]*secret
	orgMeta map[string]*OrgEntry
	repos   map[string]*repo
}

// Reader resolves names against one consistent snapshot of the store.
type Reader struct {
	repo    *repo
	env     *env
	private bool
	repoID  string
	org     map[string]*secret
	orgMeta map[string]*OrgEntry
	probes  int
}

func NewStore() *Store { return &Store{} }

// ValidName reports whether name matches [A-Z_][A-Z0-9_]* and is at most 64 bytes.
func ValidName(name string) bool {
	if len(name) == 0 || len(name) > 64 {
		return false
	}
	for i := 0; i < len(name); i++ {
		c := name[i]
		if c == '_' || (c >= 'A' && c <= 'Z') {
			continue
		}
		if i > 0 && c >= '0' && c <= '9' {
			continue
		}
		return false
	}
	return true
}

func validValue(value string) bool {
	return len(value) >= 1 && len(value) <= 4096
}

func validPatterns(patterns []string) bool {
	for _, p := range patterns {
		if p == "" {
			return false
		}
		if star := strings.IndexByte(p, '*'); star != -1 && star != len(p)-1 {
			return false
		}
	}
	return true
}

func clone(in []string) []string {
	if in == nil {
		return nil
	}
	out := make([]string, len(in))
	copy(out, in)
	return out
}

// AddRepo registers a repository with its protected-branch patterns.
func (s *Store) AddRepo(name string, private bool, protected []string) error {
	if name == "" || !validPatterns(protected) {
		return ErrInvalidArgument
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.repos == nil {
		s.repos = map[string]*repo{}
	}
	if _, ok := s.repos[name]; ok {
		return ErrAlreadyExists
	}
	s.repos[name] = &repo{
		private:  private,
		patterns: clone(protected),
		secrets:  map[string]*secret{},
		envs:     map[string]*env{},
	}
	return nil
}

// AddEnv registers an environment for a repository with its allowed-branch patterns.
// An empty pattern list allows every branch.
func (s *Store) AddEnv(repoName, envName string, branches []string) error {
	if repoName == "" || envName == "" || !validPatterns(branches) {
		return ErrInvalidArgument
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.repos[repoName]
	if !ok {
		return ErrNotFound
	}
	if _, ok := r.envs[envName]; ok {
		return ErrAlreadyExists
	}
	r.envs[envName] = &env{branches: clone(branches), secrets: map[string]*secret{}}
	return nil
}

// RepoPrivate reports whether the repository is private.
func (s *Store) RepoPrivate(repoName string) (bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	r, ok := s.repos[repoName]
	if !ok {
		return false, ErrNotFound
	}
	return r.private, nil
}

// RepoPatterns returns a copy of the repository's protected-branch patterns.
func (s *Store) RepoPatterns(repoName string) ([]string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	r, ok := s.repos[repoName]
	if !ok {
		return nil, ErrNotFound
	}
	return clone(r.patterns), nil
}

// EnvBranches returns a copy of the environment's allowed-branch patterns.
func (s *Store) EnvBranches(repoName, envName string) ([]string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	r, ok := s.repos[repoName]
	if !ok {
		return nil, ErrNotFound
	}
	e, ok := r.envs[envName]
	if !ok {
		return nil, ErrNotFound
	}
	return clone(e.branches), nil
}

// SetOrg creates or updates an organization secret and bumps its version.
func (s *Store) SetOrg(name, value string, vis Visibility, repos []string, protectedOnly bool) error {
	if !ValidName(name) || !validValue(value) || vis < All || vis > Selected {
		return ErrInvalidArgument
	}
	if vis == Selected && len(repos) == 0 {
		return ErrInvalidArgument
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.org == nil {
		s.org = map[string]*secret{}
		s.orgMeta = map[string]*OrgEntry{}
	}
	sec := s.org[name]
	if sec == nil {
		sec = &secret{version: 0}
		s.org[name] = sec
	}
	sec.version++
	sec.value = value
	sec.protectedOnly = protectedOnly
	s.orgMeta[name] = &OrgEntry{
		Value:         value,
		Version:       sec.version,
		Vis:           vis,
		Repos:         clone(repos),
		ProtectedOnly: protectedOnly,
	}
	return nil
}

// SetRepo creates or updates a repository secret and bumps its version.
func (s *Store) SetRepo(repoName, name, value string, protectedOnly bool) error {
	if !ValidName(name) || !validValue(value) {
		return ErrInvalidArgument
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.repos[repoName]
	if !ok {
		return ErrNotFound
	}
	sec := r.secrets[name]
	if sec == nil {
		sec = &secret{version: 0}
		r.secrets[name] = sec
	}
	sec.version++
	sec.value = value
	sec.protectedOnly = protectedOnly
	return nil
}

// SetEnv creates or updates an environment secret and bumps its version.
func (s *Store) SetEnv(repoName, envName, name, value string, protectedOnly bool) error {
	if !ValidName(name) || !validValue(value) {
		return ErrInvalidArgument
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.repos[repoName]
	if !ok {
		return ErrNotFound
	}
	e, ok := r.envs[envName]
	if !ok {
		return ErrNotFound
	}
	sec := e.secrets[name]
	if sec == nil {
		sec = &secret{version: 0}
		e.secrets[name] = sec
	}
	sec.version++
	sec.value = value
	sec.protectedOnly = protectedOnly
	return nil
}

// DeleteOrg removes an organization secret.
func (s *Store) DeleteOrg(name string) error {
	if !ValidName(name) {
		return ErrInvalidArgument
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.org[name]; !ok {
		return ErrNotFound
	}
	delete(s.org, name)
	delete(s.orgMeta, name)
	return nil
}

// DeleteRepoSecret removes a repository secret.
func (s *Store) DeleteRepoSecret(repoName, name string) error {
	if !ValidName(name) {
		return ErrInvalidArgument
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.repos[repoName]
	if !ok {
		return ErrNotFound
	}
	if _, ok := r.secrets[name]; !ok {
		return ErrNotFound
	}
	delete(r.secrets, name)
	return nil
}

// DeleteEnvSecret removes an environment secret.
func (s *Store) DeleteEnvSecret(repoName, envName, name string) error {
	if !ValidName(name) {
		return ErrInvalidArgument
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.repos[repoName]
	if !ok {
		return ErrNotFound
	}
	e, ok := r.envs[envName]
	if !ok {
		return ErrNotFound
	}
	if _, ok := e.secrets[name]; !ok {
		return ErrNotFound
	}
	delete(e.secrets, name)
	return nil
}

// View runs fn against a consistent read-only snapshot for one job.
// The read lock is held for the whole fn, so an Inject sees one instant.
func (s *Store) View(repoName, envName string, fn func(*Reader) error) error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	r, ok := s.repos[repoName]
	if !ok {
		return ErrNotFound
	}
	var e *env
	if envName != "" {
		e, ok = r.envs[envName]
		if !ok {
			return ErrNotFound
		}
	}
	rd := &Reader{
		repo:    r,
		env:     e,
		private: r.private,
		repoID:  repoName,
		org:     s.org,
		orgMeta: s.orgMeta,
	}
	return fn(rd)
}

// Probes reports how many store lookups this reader has performed.
func (rd *Reader) Probes() int { return rd.probes }

// Lookup resolves a name in env, repo, org order and reports the first
// existing definition. An invisible org definition counts as nonexistent.
func (rd *Reader) Lookup(name string) (Entry, bool) {
	if rd.env != nil {
		rd.probes++
		if sec := rd.env.secrets[name]; sec != nil {
			return Entry{Value: sec.value, Level: EnvLevel, Version: sec.version, ProtectedOnly: sec.protectedOnly}, true
		}
	}
	rd.probes++
	if sec := rd.repo.secrets[name]; sec != nil {
		return Entry{Value: sec.value, Level: RepoLevel, Version: sec.version, ProtectedOnly: sec.protectedOnly}, true
	}
	rd.probes++
	meta := rd.orgMeta[name]
	if meta == nil || !rd.orgVisible(meta) {
		return Entry{}, false
	}
	return Entry{Value: meta.Value, Level: OrgLevel, Version: meta.Version, ProtectedOnly: meta.ProtectedOnly}, true
}

func (rd *Reader) orgVisible(meta *OrgEntry) bool {
	switch meta.Vis {
	case All:
		return true
	case Private:
		return rd.private
	case Selected:
		for _, name := range meta.Repos {
			if name == rd.repoID {
				return true
			}
		}
		return false
	}
	return false
}

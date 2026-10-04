package secret

import (
	"errors"
	"sync"

	"ontology/scope"
)

var (
	ErrInvalid        = errors.New("invalid argument")
	ErrRepoNotFound   = errors.New("repository not found")
	ErrEnvNotFound    = errors.New("environment not found")
	ErrExists         = errors.New("already exists")
	ErrSecretNotFound = errors.New("secret not found")
)

// Definition 是一条密钥定义。
type Definition struct {
	Value         string
	Version       int
	ProtectedOnly bool
}

// RepoView 是一次读取会话中某仓库的只读视图。
type RepoView struct {
	Private     bool
	Protected   []string
	EnvBranches map[string][]string
}

// OrgDefinition 是组织级密钥定义（含可见性策略）。
type OrgDefinition struct {
	Definition
	Visibility    scope.Visibility
	SelectedRepos []string
}

// View 是一次读取会话的一致快照。
type View struct {
	repos map[string]repoState
	org   map[string]OrgDefinition
	store *Store
}

type repoState struct {
	private   bool
	protected []string
	envs      map[string][]string
	secrets   map[string]Definition
	envSecret map[string]map[string]Definition
}

// Store 是密钥与仓库/环境登记的并发安全存储。
type Store struct {
	mu    sync.RWMutex
	repos map[string]repoState
	org   map[string]OrgDefinition
}

func New() *Store { return &Store{} }

func (s *Store) AddRepo(repo string, private bool, protected []string) error {
	if repo == "" || !scope.ValidPatterns(protected) {
		return ErrInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.repos == nil {
		s.repos = map[string]repoState{}
	}
	if _, ok := s.repos[repo]; ok {
		return ErrExists
	}
	s.repos[repo] = repoState{
		private:   private,
		protected: cloneStrings(protected),
		envs:      map[string][]string{},
		secrets:   map[string]Definition{},
		envSecret: map[string]map[string]Definition{},
	}
	return nil
}

func (s *Store) AddEnv(repo, env string, branches []string) error {
	if repo == "" || env == "" || !scope.ValidPatterns(branches) {
		return ErrInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.repos[repo]
	if !ok {
		return ErrRepoNotFound
	}
	if _, ok := r.envs[env]; ok {
		return ErrExists
	}
	r.envs[env] = cloneStrings(branches)
	s.repos[repo] = r
	return nil
}

func (s *Store) SetOrg(name, value string, vis scope.Visibility, repos []string, protectedOnly bool) error {
	if !scope.ValidName(name) || !scope.ValidValue(value) || vis < scope.All || vis > scope.Selected {
		return ErrInvalid
	}
	for _, r := range repos {
		if r == "" {
			return ErrInvalid
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.org == nil {
		s.org = map[string]OrgDefinition{}
	}
	d := s.org[name]
	d.Value = value
	d.Version++
	d.ProtectedOnly = protectedOnly
	d.Visibility = vis
	d.SelectedRepos = cloneStrings(repos)
	s.org[name] = d
	return nil
}

func (s *Store) SetRepo(repo, name, value string, protectedOnly bool) error {
	if repo == "" || !scope.ValidName(name) || !scope.ValidValue(value) {
		return ErrInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.repos[repo]
	if !ok {
		return ErrRepoNotFound
	}
	d := r.secrets[name]
	d.Value = value
	d.Version++
	d.ProtectedOnly = protectedOnly
	r.secrets[name] = d
	s.repos[repo] = r
	return nil
}

func (s *Store) SetEnv(repo, env, name, value string, protectedOnly bool) error {
	if repo == "" || env == "" || !scope.ValidName(name) || !scope.ValidValue(value) {
		return ErrInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.repos[repo]
	if !ok {
		return ErrRepoNotFound
	}
	if _, ok := r.envs[env]; !ok {
		return ErrEnvNotFound
	}
	if r.envSecret[env] == nil {
		r.envSecret[env] = map[string]Definition{}
	}
	d := r.envSecret[env][name]
	d.Value = value
	d.Version++
	d.ProtectedOnly = protectedOnly
	r.envSecret[env][name] = d
	s.repos[repo] = r
	return nil
}

func (s *Store) DeleteOrg(name string) error {
	if !scope.ValidName(name) {
		return ErrInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.org[name]; !ok {
		return ErrSecretNotFound
	}
	delete(s.org, name)
	return nil
}

func (s *Store) DeleteRepoSecret(repo, name string) error {
	if repo == "" || !scope.ValidName(name) {
		return ErrInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.repos[repo]
	if !ok {
		return ErrRepoNotFound
	}
	if _, ok := r.secrets[name]; !ok {
		return ErrSecretNotFound
	}
	delete(r.secrets, name)
	s.repos[repo] = r
	return nil
}

func (s *Store) DeleteEnv(repo, env, name string) error {
	if repo == "" || env == "" || !scope.ValidName(name) {
		return ErrInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.repos[repo]
	if !ok {
		return ErrRepoNotFound
	}
	if _, ok := r.envs[env]; !ok {
		return ErrEnvNotFound
	}
	if _, ok := r.envSecret[env][name]; !ok {
		return ErrSecretNotFound
	}
	delete(r.envSecret[env], name)
	s.repos[repo] = r
	return nil
}

// Snapshot 获取一致只读视图（调用方结束后必须调用 View.Release）。
func (s *Store) Snapshot() *View {
	s.mu.RLock()
	return &View{repos: s.repos, org: s.org, store: s}
}

func (v *View) Release() { v.store.mu.RUnlock() }

// Repo 返回仓库视图与是否存在。
func (v *View) Repo(repo string) (RepoView, bool) {
	r, ok := v.repos[repo]
	if !ok {
		return RepoView{}, false
	}
	return RepoView{
		Private:     r.private,
		Protected:   r.protected,
		EnvBranches: r.envs,
	}, true
}

// LookupEnv 查询环境级定义。
func (v *View) LookupEnv(repo, env, name string) (Definition, bool) {
	r, ok := v.repos[repo]
	if !ok {
		return Definition{}, false
	}
	d, ok := r.envSecret[env][name]
	return d, ok
}

// LookupRepo 查询仓库级定义。
func (v *View) LookupRepo(repo, name string) (Definition, bool) {
	r, ok := v.repos[repo]
	if !ok {
		return Definition{}, false
	}
	d, ok := r.secrets[name]
	return d, ok
}

// LookupOrg 查询组织级定义及其可见性策略（无论对该仓库是否可见）。
func (v *View) LookupOrg(name string) (OrgDefinition, bool) {
	d, ok := v.org[name]
	return d, ok
}

func cloneStrings(in []string) []string {
	if in == nil {
		return nil
	}
	out := make([]string, len(in))
	copy(out, in)
	return out
}

package depsolver

import (
	"context"
	"sort"
	"sync"
)

// PackageVersion 是一次发布的全部信息。
type PackageVersion struct {
	Name         string
	Version      string
	Dependencies map[string]string
}

// Snapshot 是某一时刻仓库的不可变完整快照。
type Snapshot struct {
	packages map[string]map[string]PackageVersion
	yanked   map[string]map[string]bool
}

// Repository 是并发安全的发布/撤回/求解入口。
type Repository struct {
	mu       sync.RWMutex
	packages map[string]map[string]PackageVersion
	yanked   map[string]map[string]bool
}

// NewRepository 创建空仓库。
func NewRepository() *Repository {
	return &Repository{
		packages: map[string]map[string]PackageVersion{},
		yanked:   map[string]map[string]bool{},
	}
}

// Publish 发布一个新版本。
func (r *Repository) Publish(pv PackageVersion) error {
	if pv.Name == "" {
		return ErrEmptyPackageName
	}
	version, err := ParseVersion(pv.Version)
	if err != nil {
		return err
	}
	parsed := make(map[string]Range, len(pv.Dependencies))
	depNames := make([]string, 0, len(pv.Dependencies))
	for name, text := range pv.Dependencies {
		if name == "" {
			return ErrEmptyPackageName
		}
		rng, err := ParseRange(text)
		if err != nil {
			return err
		}
		parsed[name] = rng
		depNames = append(depNames, name)
	}
	sort.Strings(depNames)

	r.mu.Lock()
	defer r.mu.Unlock()
	versions := r.packages[pv.Name]
	if versions == nil {
		versions = map[string]PackageVersion{}
		r.packages[pv.Name] = versions
	}
	if _, exists := versions[version.raw]; exists {
		return ErrDuplicateVersion
	}
	versions[version.raw] = pv
	return nil
}

// Yank 撤回一个已发布版本。
func (r *Repository) Yank(name, version string) error {
	if name == "" {
		return ErrEmptyPackageName
	}
	if _, err := ParseVersion(version); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	versions := r.packages[name]
	if versions == nil {
		return ErrVersionNotFound
	}
	if _, exists := versions[version]; !exists {
		return ErrVersionNotFound
	}
	yanks := r.yanked[name]
	if yanks == nil {
		yanks = map[string]bool{}
		r.yanked[name] = yanks
	}
	yanks[version] = true
	return nil
}

// Solve 基于某一时刻的完整快照求解根依赖。
func (r *Repository) Solve(ctx context.Context, root map[string]string) (map[string]string, error) {
	if root == nil {
		return nil, ErrInvalidArgument
	}
	parsedRoot := make(map[string]Range, len(root))
	names := make([]string, 0, len(root))
	for name, text := range root {
		if name == "" {
			return nil, ErrEmptyPackageName
		}
		rng, err := ParseRange(text)
		if err != nil {
			return nil, err
		}
		parsedRoot[name] = rng
		names = append(names, name)
	}
	sort.Strings(names)

	r.mu.RLock()
	snap := r.snapshotLocked()
	r.mu.RUnlock()
	return solveAt(ctx, snap, parsedRoot, names)
}

func (r *Repository) snapshotLocked() *Snapshot {
	pkgCopy := make(map[string]map[string]PackageVersion, len(r.packages))
	for name, versions := range r.packages {
		vs := make(map[string]PackageVersion, len(versions))
		for v, pv := range versions {
			vs[v] = pv
		}
		pkgCopy[name] = vs
	}
	yankCopy := make(map[string]map[string]bool, len(r.yanked))
	for name, versions := range r.yanked {
		vs := make(map[string]bool, len(versions))
		for v, y := range versions {
			vs[v] = y
		}
		yankCopy[name] = vs
	}
	return &Snapshot{packages: pkgCopy, yanked: yankCopy}
}

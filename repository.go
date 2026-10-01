package resolver

import (
	"errors"
	"sync"
)

var ErrInvalidPackageName = errors.New("package name must not be empty")

type packageVersion struct {
	version      string
	parsed       semanticVersion
	dependencies map[string]string
	ranges       map[string]versionRange
	yanked       bool
}

type repositorySnapshot struct {
	packages map[string]map[string]*packageVersion
}

type Repository struct {
	mu       sync.RWMutex
	packages map[string]map[string]*packageVersion
}

func NewRepository() *Repository {
	return &Repository{packages: map[string]map[string]*packageVersion{}}
}

func (r *Repository) Publish(name, version string, dependencies map[string]string) error {
	if name == "" {
		return ErrInvalidPackageName
	}

	parsed, err := parseVersion(version)
	if err != nil {
		return err
	}

	clonedDependencies := make(map[string]string, len(dependencies))
	parsedRanges := make(map[string]versionRange, len(dependencies))
	for dependencyName, rangeText := range dependencies {
		if dependencyName == "" {
			return ErrInvalidPackageName
		}
		parsedRange, err := parseRange(rangeText)
		if err != nil {
			return err
		}
		clonedDependencies[dependencyName] = rangeText
		parsedRanges[dependencyName] = parsedRange
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	versions := r.packages[name]
	if versions == nil {
		versions = map[string]*packageVersion{}
		r.packages[name] = versions
	}
	if _, exists := versions[version]; exists {
		return ErrDuplicateVersion
	}

	versions[version] = &packageVersion{
		version:      version,
		parsed:       parsed,
		dependencies: clonedDependencies,
		ranges:       parsedRanges,
	}
	return nil
}

func (r *Repository) Yank(name, version string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	versions := r.packages[name]
	selected := versions[version]
	if selected == nil {
		return ErrVersionNotFound
	}
	selected.yanked = true
	return nil
}

func (r *Repository) currentSnapshot() repositorySnapshot {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return cloneSnapshot(r.packages)
}

func cloneSnapshot(source map[string]map[string]*packageVersion) repositorySnapshot {
	packages := make(map[string]map[string]*packageVersion, len(source))
	for packageName, versions := range source {
		clonedVersions := make(map[string]*packageVersion, len(versions))
		for versionText, version := range versions {
			clonedVersion := *version
			clonedVersion.dependencies = cloneStringMap(version.dependencies)
			clonedVersion.ranges = cloneRangeMap(version.ranges)
			clonedVersions[versionText] = &clonedVersion
		}
		packages[packageName] = clonedVersions
	}
	return repositorySnapshot{packages: packages}
}

func cloneStringMap(source map[string]string) map[string]string {
	result := make(map[string]string, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}

func cloneRangeMap(source map[string]versionRange) map[string]versionRange {
	result := make(map[string]versionRange, len(source))
	for key, value := range source {
		comparators := append([]comparator(nil), value.comparators...)
		result[key] = versionRange{comparators: comparators}
	}
	return result
}

// Package graph registers packages, their versions and per-version
// constraints. After registration the graph is safe for concurrent reads.
package graph

import (
	"errors"
	"fmt"
	"sort"
	"sync"

	"ontology/rng"
	"ontology/ver"
)

// ErrDuplicateVersion reports registering the same package@version twice.
var ErrDuplicateVersion = errors.New("graph: duplicate package version")

// ErrUnknownVersion reports declaring constraints for a package@version
// that has not been registered.
var ErrUnknownVersion = errors.New("graph: unknown package version")

// Dependency is one constraint declared by a package@version on a target
// package.
type Dependency struct {
	Target string
	Con    *rng.Constraint
}

// Graph holds registered packages. The zero value is ready to use.
type Graph struct {
	mu   sync.RWMutex
	pkgs map[string]*pkgEntry
}

type pkgEntry struct {
	versions map[string]ver.Version // raw -> parsed
	sorted   []ver.Version          // descending
	deps     map[string][]Dependency
}

// New returns an empty Graph.
func New() *Graph {
	return &Graph{pkgs: map[string]*pkgEntry{}}
}

// AddVersion registers version v of pkg. Registering the same
// package@version twice fails with ErrDuplicateVersion and leaves the
// existing registration untouched.
func (g *Graph) AddVersion(pkg, v string) error {
	parsed, err := ver.Parse(v)
	if err != nil {
		return err
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	e := g.pkgs[pkg]
	if e == nil {
		e = &pkgEntry{versions: map[string]ver.Version{}, deps: map[string][]Dependency{}}
		g.pkgs[pkg] = e
	}
	if _, dup := e.versions[parsed.String()]; dup {
		return fmt.Errorf("%w: %s@%s", ErrDuplicateVersion, pkg, v)
	}
	e.versions[parsed.String()] = parsed
	e.sorted = append(e.sorted, parsed)
	sort.Slice(e.sorted, func(i, j int) bool { return e.sorted[i].Compare(e.sorted[j]) > 0 })
	return nil
}

// AddConstraint declares that pkg@version requires target to satisfy con.
// The source package@version must already be registered; the target
// package may be registered later (checked at solve time).
func (g *Graph) AddConstraint(pkg, version, target, con string) error {
	parsed, err := rng.Parse(con)
	if err != nil {
		return err
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	e := g.pkgs[pkg]
	if e == nil {
		return fmt.Errorf("%w: %s@%s", ErrUnknownVersion, pkg, version)
	}
	if _, ok := e.versions[version]; !ok {
		return fmt.Errorf("%w: %s@%s", ErrUnknownVersion, pkg, version)
	}
	e.deps[version] = append(e.deps[version], Dependency{Target: target, Con: parsed})
	return nil
}

// Has reports whether pkg is registered.
func (g *Graph) Has(pkg string) bool {
	g.mu.RLock()
	defer g.mu.RUnlock()
	_, ok := g.pkgs[pkg]
	return ok
}

// Versions returns the registered versions of pkg, sorted descending.
// The result is a copy; callers may not mutate shared state.
func (g *Graph) Versions(pkg string) []ver.Version {
	g.mu.RLock()
	defer g.mu.RUnlock()
	e := g.pkgs[pkg]
	if e == nil {
		return nil
	}
	return append([]ver.Version(nil), e.sorted...)
}

// Dependencies returns the constraints declared by pkg@version.
// The result is a copy.
func (g *Graph) Dependencies(pkg, version string) []Dependency {
	g.mu.RLock()
	defer g.mu.RUnlock()
	e := g.pkgs[pkg]
	if e == nil {
		return nil
	}
	return append([]Dependency(nil), e.deps[version]...)
}

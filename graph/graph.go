// Package graph registers packages, versions and their declared constraints.
package graph

import (
	"errors"
	"sort"
	"sync"

	"ontology/rng"
	"ontology/ver"
)

// ErrDuplicateVersion is returned when the same package@version is added twice.
var ErrDuplicateVersion = errors.New("graph: duplicate package version")

// Dep is one constraint declared by a package version against another package.
type Dep struct {
	Pkg  string
	Spec string
}

type depReq struct {
	spec string
	r    rng.Range
}

type release struct {
	version string
	deps    map[string]depReq
}

type pkgData struct {
	releases []*release // ascending by version
	byVer    map[string]*release
}

// Graph is an immutable-after-build registry of packages and versions.
type Graph struct {
	mu   sync.RWMutex
	pkgs map[string]*pkgData
}

// New creates an empty graph.
func New() *Graph { return &Graph{pkgs: map[string]*pkgData{}} }

// Add registers pkg@version declaring deps (dependency package -> spec).
// It fails without mutating the graph on duplicate version or bad syntax.
func (g *Graph) Add(pkg, version string, deps map[string]string) error {
	if _, err := ver.Parse(version); err != nil {
		return err
	}
	parsed := make(map[string]depReq, len(deps))
	for depPkg, spec := range deps {
		r, err := rng.Parse(spec)
		if err != nil {
			return err
		}
		parsed[depPkg] = depReq{spec: spec, r: r}
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	p := g.pkgs[pkg]
	if p == nil {
		p = &pkgData{byVer: map[string]*release{}}
		g.pkgs[pkg] = p
	}
	if _, dup := p.byVer[version]; dup {
		return ErrDuplicateVersion
	}
	rel := &release{version: version, deps: parsed}
	p.byVer[version] = rel
	p.releases = append(p.releases, rel)
	sort.Slice(p.releases, func(i, j int) bool {
		vi, _ := ver.Parse(p.releases[i].version)
		vj, _ := ver.Parse(p.releases[j].version)
		return ver.Compare(vi, vj) < 0
	})
	return nil
}

// Packages returns all registered package names, sorted.
func (g *Graph) Packages() []string {
	g.mu.RLock()
	defer g.mu.RUnlock()
	out := make([]string, 0, len(g.pkgs))
	for name := range g.pkgs {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// Versions returns registered versions of pkg, ascending.
func (g *Graph) Versions(pkg string) []string {
	g.mu.RLock()
	defer g.mu.RUnlock()
	p := g.pkgs[pkg]
	if p == nil {
		return nil
	}
	out := make([]string, len(p.releases))
	for i, r := range p.releases {
		out[i] = r.version
	}
	return out
}

// Constraint returns the parsed range declared by pkg@version toward dep.
func (g *Graph) Constraint(pkg, version, dep string) (rng.Range, bool) {
	g.mu.RLock()
	defer g.mu.RUnlock()
	p := g.pkgs[pkg]
	if p == nil {
		return rng.Range{}, false
	}
	rel := p.byVer[version]
	if rel == nil {
		return rng.Range{}, false
	}
	d, ok := rel.deps[dep]
	if !ok {
		return rng.Range{}, false
	}
	return d.r, true
}

// Has reports whether pkg is registered.
func (g *Graph) Has(pkg string) bool {
	g.mu.RLock()
	defer g.mu.RUnlock()
	_, ok := g.pkgs[pkg]
	return ok
}

// Deps returns the constraints declared by pkg@version, sorted by package.
func (g *Graph) Deps(pkg, version string) []Dep {
	g.mu.RLock()
	defer g.mu.RUnlock()
	p := g.pkgs[pkg]
	if p == nil {
		return nil
	}
	rel := p.byVer[version]
	if rel == nil {
		return nil
	}
	out := make([]Dep, 0, len(rel.deps))
	for depPkg, d := range rel.deps {
		out = append(out, Dep{Pkg: depPkg, Spec: d.spec})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Pkg < out[j].Pkg })
	return out
}

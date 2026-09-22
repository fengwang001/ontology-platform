// Package graph 登记包、版本及其声明的约束；登记后只读、可并发求解，依赖 rng。
package graph

import (
	"errors"

	"ontology/rng"
	"ontology/ver"
)

var (
	// ErrDuplicateVersion 同一包重复登记同一版本；已有登记不被改变。
	ErrDuplicateVersion = errors.New("duplicate package version")
	// ErrUnknownPackage 约束/求解引用了从未登记过的包。
	ErrUnknownPackage = errors.New("unknown package")
)

// Dep 是 pkg@version 对另一个包声明的一条原始约束。
type Dep struct {
	Target     string
	Constraint string
}

// Ref 唯一标识一个已登记的「包@版本」。
type Ref struct {
	Pkg string
	V   ver.Version
}

// Graph 是登记完成后只读的包版本图。
type Graph struct {
	pkgs map[string]*pkg
}

type pkg struct {
	versions []ver.Version // 严格升序
	deps     map[string][]rng.Entry
}

// New 返回空图。
func New() *Graph { return &Graph{pkgs: map[string]*pkg{}} }

// AddVersion 登记一个包版本及其依赖；重复版本返回 ErrDuplicateVersion 且不改原登记。
func (g *Graph) AddVersion(name, version string, deps ...Dep) error {
	v, err := ver.Parse(version)
	if err != nil {
		return err
	}
	p := g.pkgs[name]
	if p != nil {
		for _, ex := range p.versions {
			if ex.Compare(v) == 0 {
				return ErrDuplicateVersion
			}
		}
	}
	entries := make([]rng.Entry, 0, len(deps))
	for _, d := range deps {
		rr, err := rng.Parse(d.Constraint)
		if err != nil {
			return err
		}
		entries = append(entries, rng.Entry{
			Range: rr,
			Origin: rng.Origin{
				Pkg: name, V: v, Target: d.Target, Raw: d.Constraint,
			},
		})
	}
	sortEntries(entries)
	if p == nil {
		p = &pkg{deps: map[string][]rng.Entry{}}
		g.pkgs[name] = p
	}
	p.versions = insertSorted(p.versions, v)
	p.deps[v.String()] = entries
	return nil
}

// HasPackage 报告包是否已登记。
func (g *Graph) HasPackage(name string) bool { _, ok := g.pkgs[name]; return ok }

// Packages 按字典序返回所有已登记包名。
func (g *Graph) Packages() []string {
	names := make([]string, 0, len(g.pkgs))
	for n := range g.pkgs {
		names = append(names, n)
	}
	sortStrings(names)
	return names
}

// Versions 按版本号升序返回某包的全部候选；未登记包返回 nil,false。
func (g *Graph) Versions(name string) ([]ver.Version, bool) {
	p, ok := g.pkgs[name]
	if !ok {
		return nil, false
	}
	out := make([]ver.Version, len(p.versions))
	copy(out, p.versions)
	return out, true
}

// Deps 返回 pkg@version 声明的约束条目，按目标包名（再按版本序）排序。
func (g *Graph) Deps(ref Ref) []rng.Entry {
	p, ok := g.pkgs[ref.Pkg]
	if !ok {
		return nil
	}
	src := p.deps[ref.V.String()]
	out := make([]rng.Entry, len(src))
	copy(out, src)
	return out
}

func insertSorted(vs []ver.Version, v ver.Version) []ver.Version {
	i := 0
	for i < len(vs) && vs[i].Compare(v) < 0 {
		i++
	}
	vs = append(vs, ver.Version{})
	copy(vs[i+1:], vs[i:])
	vs[i] = v
	return vs
}

func sortEntries(es []rng.Entry) {
	for i := 1; i < len(es); i++ {
		for j := i; j > 0 && entryLess(es[j], es[j-1]); j-- {
			es[j], es[j-1] = es[j-1], es[j]
		}
	}
}

func entryLess(a, b rng.Entry) bool {
	if a.Origin.Target != b.Origin.Target {
		return a.Origin.Target < b.Origin.Target
	}
	return a.Range.String() < b.Range.String()
}

func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j-1] > s[j]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}

// Package solve 在只读 graph 上做约束回溯求解、确定性冲突解释与预算控制。
package solve

import (
	"errors"
	"fmt"
	"sort"

	"ontology/graph"
	"ontology/rng"
	"ontology/ver"
)

var (
	// ErrNoSolution 表示约束无解（与搜索预算超限严格区分）。
	ErrNoSolution = errors.New("no satisfying version selection")
	// ErrSearchBudget 表示尝试的「包@版本」组合数超过预算。
	ErrSearchBudget = errors.New("search budget exceeded")
	// ErrUnknownPackage 是引用未登记包的哨兵；具体为 *UnknownPackageError。
	ErrUnknownPackage = graph.ErrUnknownPackage
)

// graphErrUnknown 与 graph 包哨兵保持同一错误。
var graphErrUnknown = graph.ErrUnknownPackage

// Root 是一条根需求。
type Root struct {
	Package    string
	Constraint string
}

// Solution 是每个（从根可达的）包恰好一个版本的选择。
type Solution struct {
	chosen map[string]ver.Version
	// attempts 为非导出计数器：本次求解尝试过的「包@版本」组合数。
	attempts int
}

// Choice 是一条求解结果。
type Choice struct {
	Package string
	Version ver.Version
}

// Choices 按包名字典序返回全部选择。
func (s *Solution) Choices() []Choice {
	out := make([]Choice, 0, len(s.chosen))
	for p, v := range s.chosen {
		out = append(out, Choice{Package: p, Version: v})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Package < out[j].Package })
	return out
}

// VersionOf 返回某包被选中的版本。
func (s *Solution) VersionOf(pkg string) (ver.Version, bool) {
	v, ok := s.chosen[pkg]
	return v, ok
}

// Attempts 返回非导出计数器的快照（仅计数器本身，无其他求解内部状态暴露）。
func (s *Solution) Attempts() int { return s.attempts }

// Options 控制一次求解。
type Options struct {
	// MaxAttempts 为 0 表示不限制；否则超过即返回 ErrSearchBudget。
	MaxAttempts int
}

// Solver 绑定一张只读图，可被多个 goroutine 并发使用。
type Solver struct {
	g *graph.Graph
}

// New 绑定图。
func New(g *graph.Graph) *Solver { return &Solver{g: g} }

// Solve 求一份满足全部根需求及其传递依赖的选择。
func (s *Solver) Solve(roots []Root, opts Options) (*Solution, error) {
	sol, _, err := s.solve(roots, opts.MaxAttempts)
	return sol, err
}

// Validate 逐条核验一份解：恰好一个版本、全部生效约束满足、无不可达包。
func (s *Solver) Validate(sol *Solution, roots []Root) error {
	if sol == nil {
		return errors.New("nil solution")
	}
	for _, r := range roots {
		if _, err := rng.Parse(r.Constraint); err != nil {
			return err
		}
		v, ok := sol.VersionOf(r.Package)
		if !ok {
			return fmt.Errorf("root package %q missing from solution", r.Package)
		}
		rr, _ := rng.Parse(r.Constraint)
		if !rr.Contains(v) {
			return fmt.Errorf("%s@%s violates root constraint %s", r.Package, v, r.Constraint)
		}
	}
	for _, c := range sol.Choices() {
		for _, d := range s.g.Deps(graph.Ref{Pkg: c.Package, V: c.Version}) {
			v, ok := sol.VersionOf(d.Origin.Target)
			if !ok {
				return fmt.Errorf("dependency target %q not in solution", d.Origin.Target)
			}
			if !d.Range.Contains(v) {
				return fmt.Errorf("%s@%s requires %s %s but got %s",
					c.Package, c.Version, d.Origin.Target, d.Origin.Raw, v)
			}
		}
	}
	reachable := map[string]bool{}
	var walk func(string)
	walk = func(p string) {
		if reachable[p] {
			return
		}
		reachable[p] = true
		v, ok := sol.chosen[p]
		if !ok {
			return
		}
		for _, d := range s.g.Deps(graph.Ref{Pkg: p, V: v}) {
			walk(d.Origin.Target)
		}
	}
	for _, r := range roots {
		walk(r.Package)
	}
	for p := range sol.chosen {
		if !reachable[p] {
			return fmt.Errorf("unreachable package %q present in solution", p)
		}
	}
	return nil
}

// ChainLink 是冲突解释链上的一环。
type ChainLink struct {
	From       string // "<root>" 或声明约束的 "pkg@version"
	Constraint string // 该环节点声明的原始约束
	Target     string // 被约束的包
}

// ConflictError 是 ErrNoSolution 的具名载体，携带确定性的冲突链。
type ConflictError struct {
	Package string
	Reason  string
	Chain   []ChainLink
}

func (e *ConflictError) Unwrap() error { return ErrNoSolution }

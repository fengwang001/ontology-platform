// Package plan 检测批量重命名请求的冲突并编排安全的执行顺序。
//
// 排序规则：请求 r 依赖于 r2 当且仅当 r.New == r2.Old（r2 必须先搬走），
// 对依赖图按层做拓扑排序，同层按旧名字典序，保证结果确定。
// 环用 cycle 包借临时名破解，临时名数量等于环的个数。
package plan

import (
	"errors"
	"fmt"
	"sort"

	"ontology/cycle"
	"ontology/name"
)

// Request 是一条重命名请求：把 Old 改名为 New。
type Request struct {
	Old string
	New string
}

// Step 是一步可执行的重命名。
type Step = cycle.Step

// 四类冲突的哨兵错误，可用 errors.Is 区分。
var (
	ErrDuplicateOld = errors.New("plan: same old name requested twice")
	ErrOldMissing   = errors.New("plan: old name does not exist")
	ErrDuplicateNew = errors.New("plan: two requests share the same new name")
	ErrTargetExists = errors.New("plan: target exists and is not moved away")
)

// Conflict 描述一次冲突，Unwrap 返回对应哨兵错误。
type Conflict struct {
	Kind     error
	Old      string // 引发冲突的旧名
	New      string // 引发冲突的新名
	OtherOld string // 冲突的另一旧名（ErrDuplicateNew）
	OtherNew string // 冲突的另一新名（ErrDuplicateOld）
}

func (c *Conflict) Error() string {
	switch c.Kind {
	case ErrDuplicateOld:
		return fmt.Sprintf("%v: %q -> %q and %q", c.Kind, c.Old, c.New, c.OtherNew)
	case ErrDuplicateNew:
		return fmt.Sprintf("%v: %q and %q -> %q", c.Kind, c.Old, c.OtherOld, c.New)
	case ErrOldMissing:
		return fmt.Sprintf("%v: %q", c.Kind, c.Old)
	default:
		return fmt.Sprintf("%v: %q", c.Kind, c.New)
	}
}

// Unwrap 返回冲突类别哨兵。
func (c *Conflict) Unwrap() error { return c.Kind }

// lookups 统计冲突检测与排序阶段的查找次数（map 读与命名空间查询）。
var lookups int64

// Lookups 返回累计查找次数。
func Lookups() int64 { return lookups }

// ResetLookups 清零查找计数器。
func ResetLookups() { lookups = 0 }

// Plan 是一个可执行计划：Steps 按序执行，Temps 是破环引入的临时名。
type Plan struct {
	Steps []Step
	Temps []string
}

// Build 检测冲突并生成执行计划。自环（Old == New）是无操作，先行丢弃。
// 发现冲突时返回 *Conflict，命名空间不被修改。
func Build(ns *name.NS, reqs []Request) (*Plan, error) {
	rs := make([]Request, 0, len(reqs))
	for _, r := range reqs {
		if r.Old != r.New {
			rs = append(rs, r)
		}
	}
	oldIdx := make(map[string]int, len(rs))
	newIdx := make(map[string]int, len(rs))
	for i, r := range rs {
		lookups++
		if j, ok := oldIdx[r.Old]; ok {
			return nil, &Conflict{Kind: ErrDuplicateOld, Old: r.Old, New: rs[j].New, OtherNew: r.New}
		}
		oldIdx[r.Old] = i
	}
	for _, r := range rs {
		lookups++
		if !ns.Has(r.Old) {
			return nil, &Conflict{Kind: ErrOldMissing, Old: r.Old}
		}
	}
	for i, r := range rs {
		lookups++
		if j, ok := newIdx[r.New]; ok {
			return nil, &Conflict{Kind: ErrDuplicateNew, New: r.New, Old: rs[j].Old, OtherOld: r.Old}
		}
		newIdx[r.New] = i
	}
	for _, r := range rs {
		lookups += 2
		if _, moved := oldIdx[r.New]; !moved && ns.Has(r.New) {
			return nil, &Conflict{Kind: ErrTargetExists, Old: r.Old, New: r.New}
		}
	}

	n := len(rs)
	dep := make([]int, n)       // dep[i] 必须先于 i 执行，-1 表示无依赖
	dependent := make([]int, n) // dependent[i] 依赖于 i，-1 表示无
	indeg := make([]int, n)
	for i, r := range rs {
		lookups += 2
		j, ok := oldIdx[r.New]
		if !ok {
			j = -1
		}
		dep[i] = j
		k, ok := newIdx[r.Old]
		if !ok {
			k = -1
		}
		dependent[i] = k
		if dep[i] >= 0 {
			indeg[i] = 1
		}
	}

	p := &Plan{}
	done := make([]bool, n)
	ndone := 0
	var level []int
	for i := range rs {
		if indeg[i] == 0 {
			level = append(level, i)
		}
	}
	for len(level) > 0 {
		sort.Slice(level, func(a, b int) bool { return rs[level[a]].Old < rs[level[b]].Old })
		var next []int
		for _, i := range level {
			done[i] = true
			ndone++
			p.Steps = append(p.Steps, Step{Old: rs[i].Old, New: rs[i].New})
			if j := dependent[i]; j >= 0 {
				indeg[j]--
				if indeg[j] == 0 {
					next = append(next, j)
				}
			}
		}
		level = next
	}

	if ndone < n { // 剩余节点构成纯环，逐个破环
		var cycles [][]int
		for i := range rs {
			if done[i] {
				continue
			}
			var cyc []int
			for j := i; !done[j]; j = dep[j] {
				done[j] = true
				cyc = append(cyc, j)
			}
			m := 0 // 旋转到字典序最小的旧名开头，保证确定性
			for k := range cyc {
				if rs[cyc[k]].Old < rs[cyc[m]].Old {
					m = k
				}
			}
			cycles = append(cycles, append(append([]int{}, cyc[m:]...), cyc[:m]...))
		}
		sort.Slice(cycles, func(a, b int) bool { return rs[cycles[a][0]].Old < rs[cycles[b][0]].Old })
		taken := func(s string) bool {
			if ns.Has(s) {
				return true
			}
			if _, ok := oldIdx[s]; ok {
				return true
			}
			if _, ok := newIdx[s]; ok {
				return true
			}
			for _, t := range p.Temps {
				if t == s {
					return true
				}
			}
			return false
		}
		var gen cycle.Gen
		for _, cyc := range cycles {
			tmp, err := gen.Next(taken)
			if err != nil {
				return nil, err
			}
			steps := make([]Step, len(cyc))
			for k, idx := range cyc {
				steps[k] = Step{Old: rs[idx].Old, New: rs[idx].New}
			}
			p.Steps = append(p.Steps, cycle.Break(steps, tmp)...)
			p.Temps = append(p.Temps, tmp)
		}
	}
	return p, nil
}

// Package plan 做重命名请求的冲突检测与安全执行顺序编排。
package plan

import (
	"errors"
	"fmt"
	"sort"

	"ontology/cycle"
	"ontology/name"
)

// Request 是一条「旧名 -> 新名」的重命名请求。
type Request struct{ Old, New string }

// Step 是一个可执行步骤。
type Step struct{ Old, New string }

// 四类冲突的哨兵错误，均可用 errors.Is 区分。
var (
	ErrTargetExists    = errors.New("plan: target exists and is not moved away")
	ErrDuplicateTarget = errors.New("plan: two requests share one target")
	ErrDuplicateSource = errors.New("plan: same source appears twice")
	ErrSourceMissing   = errors.New("plan: source does not exist")
)

// lookups 是非导出计数器，记录一次 Plan 调用中的哈希查找次数，
// 供测试断言复杂度上界。
var lookups int

// Plan 是编排结果：按序执行 Steps 即可无覆盖地完成整批重命名。
type Plan struct{ Steps []Step }

// Compile 校验整批请求并编排执行步骤。任何冲突都在返回前判定，
// 本函数对命名空间只读不写。自环 a->a 是无操作，直接丢弃。
func Compile(s *name.Space, reqs []Request) (*Plan, error) {
	lookups = 0
	// 第一遍：合法性、重复旧名、重复新名、旧名存在性。
	byOld := make(map[string]string, len(reqs))
	byNew := make(map[string]string, len(reqs))
	for _, r := range reqs {
		if !name.Valid(r.Old) || !name.Valid(r.New) {
			return nil, name.ErrIllegalName
		}
		lookups++
		if prev, dup := byOld[r.Old]; dup {
			return nil, fmt.Errorf("%w: %q -> %q and %q", ErrDuplicateSource, r.Old, prev, r.New)
		}
		byOld[r.Old] = r.New
		lookups++
		if prev, dup := byNew[r.New]; dup {
			return nil, fmt.Errorf("%w: %q and %q both -> %q", ErrDuplicateTarget, prev, r.Old, r.New)
		}
		byNew[r.New] = r.Old
		lookups++
		if !s.Has(r.Old) {
			return nil, fmt.Errorf("%w: %q", ErrSourceMissing, r.Old)
		}
	}
	// moved 是本批真正会被搬走的旧名集合（自环不搬走任何名字）。
	moved := make(map[string]bool, len(byOld))
	for _, r := range reqs {
		if r.Old != r.New {
			moved[r.Old] = true
		}
	}
	// 第二遍：目标名已存在且不在本批被搬走 -> 冲突。
	for _, r := range reqs {
		if r.Old == r.New {
			continue
		}
		lookups += 2
		if s.Has(r.New) && !moved[r.New] {
			return nil, fmt.Errorf("%w: %q", ErrTargetExists, r.New)
		}
	}
	return &Plan{Steps: order(s, byOld, moved)}, nil
}

// order 在函数图（入度、出度均 ≤1）上编排：链按末端先行逆序执行，
// 环借一个临时名破解；分量之间按最小旧名字典序，保证确定性。
// lookups 只计哈希读（查找），不计写入。
func order(s *name.Space, byOld map[string]string, moved map[string]bool) []Step {
	m := make(map[string]string, len(moved))
	for old, new := range byOld {
		lookups++
		if moved[old] {
			m[old] = new
		}
	}
	olds := make([]string, 0, len(m))
	for old := range m {
		olds = append(olds, old)
	}
	sort.Strings(olds)
	targets := make(map[string]bool, len(m))
	for _, new := range m {
		targets[new] = true
	}
	used := func(n string) bool {
		lookups += 3
		if s.Has(n) {
			return true
		}
		if _, ok := m[n]; ok {
			return true
		}
		return targets[n]
	}
	const (
		unvisited = iota
		visiting
		done
	)
	state := make(map[string]int, len(m))
	var steps []Step
	tmpStart := 0
	emit := func(o, n string) { steps = append(steps, Step{o, n}) }
	for _, start := range olds {
		lookups++
		if state[start] != unvisited {
			continue
		}
		var chain []Step
		cur := start
		for {
			lookups++
			next, isSrc := m[cur]
			if !isSrc { // 链尾：目标自由，逆序吐出整链
				for i := len(chain) - 1; i >= 0; i-- {
					emit(chain[i].Old, chain[i].New)
				}
				break
			}
			lookups++
			st := state[cur]
			if st == done { // 汇入已处理分量：目标已腾空，同链尾处理
				for i := len(chain) - 1; i >= 0; i-- {
					emit(chain[i].Old, chain[i].New)
				}
				break
			}
			if st == visiting { // 回到起点：发现一个环，借一个临时名破环
				tmp, ns := cycle.Temp(used, tmpStart)
				tmpStart = ns
				nodes := make([]string, len(chain))
				for i, st := range chain {
					nodes[i] = st.Old
				}
				cycle.Break(nodes, tmp, emit)
				break
			}
			state[cur] = visiting
			chain = append(chain, Step{cur, next})
			cur = next
		}
		for _, st := range chain {
			state[st.Old] = done
		}
	}
	return steps
}

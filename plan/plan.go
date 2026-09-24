// Package plan 检测批量重命名的冲突并编排无覆盖的执行顺序。
//
// 排序规则：若请求 p 的新名是请求 q 的旧名，则 q 必须先执行。
// 冲突检测全部通过前不进行任何修改；自环（a→a）定义为无操作。
package plan

import (
	"errors"
	"fmt"
	"sort"

	"ontology/cycle"
)

// Request 是一条重命名请求：把 Old 搬到 New。
type Request struct {
	Old string
	New string
}

// 四类冲突的哨兵错误，可用 errors.Is 区分。
var (
	ErrTargetExists    = errors.New("plan: 目标名已存在且不会被搬走")
	ErrDuplicateTarget = errors.New("plan: 多个请求指向同一新名")
	ErrDuplicateSource = errors.New("plan: 同一旧名出现两次")
	ErrMissingSource   = errors.New("plan: 旧名不存在")
)

// ConflictError 携带冲突细节；Unwrap 返回对应哨兵错误。
type ConflictError struct {
	Kind error
	Name string // 冲突名：目标名或旧名
	A, B string // 重复目标时为两个旧名；重复旧名时为两个新名
}

func (e *ConflictError) Error() string {
	if e.A != "" || e.B != "" {
		return fmt.Sprintf("%v: %q（涉及 %q 与 %q）", e.Kind, e.Name, e.A, e.B)
	}
	return fmt.Sprintf("%v: %q", e.Kind, e.Name)
}

func (e *ConflictError) Unwrap() error { return e.Kind }

// Step 是一个执行步骤；Temp 表示破环引入的临时步骤。
type Step struct {
	Old  string
	New  string
	Temp bool
}

// Plan 是编排好的执行计划。
type Plan struct {
	Steps   []Step
	Temps   int // 临时名数量，恰好等于环的个数
	lookups int // 冲突检测阶段的查找次数（非导出计数器）
}

// Lookups 返回冲突检测的查找次数，用于验证线性复杂度。
func (p *Plan) Lookups() int { return p.lookups }

// Build 检测冲突并生成执行计划。has 报告名字是否存在于命名空间。
// 任何冲突都使整个批次被拒绝，且不产生任何修改。
func Build(reqs []Request, has func(string) bool) (*Plan, error) {
	p := &Plan{}
	eff := make([]Request, 0, len(reqs))
	for _, r := range reqs {
		if r.Old != r.New { // 自环是无操作
			eff = append(eff, r)
		}
	}
	byOld := make(map[string]string, len(eff))
	byNew := make(map[string]string, len(eff))
	for _, r := range eff {
		p.lookups++
		if prev, dup := byOld[r.Old]; dup {
			return nil, &ConflictError{Kind: ErrDuplicateSource, Name: r.Old, A: prev, B: r.New}
		}
		byOld[r.Old] = r.New
		p.lookups++
		if prev, dup := byNew[r.New]; dup {
			return nil, &ConflictError{Kind: ErrDuplicateTarget, Name: r.New, A: prev, B: r.Old}
		}
		byNew[r.New] = r.Old
	}
	for _, r := range eff {
		p.lookups++
		if !has(r.Old) {
			return nil, &ConflictError{Kind: ErrMissingSource, Name: r.Old}
		}
		p.lookups++
		if has(r.New) {
			if _, moved := byOld[r.New]; !moved {
				return nil, &ConflictError{Kind: ErrTargetExists, Name: r.New}
			}
		}
	}
	edges := make(map[string]string, len(eff))
	for _, r := range eff {
		edges[r.Old] = r.New
	}
	temps := map[string]bool{}
	namer := cycle.NewTempNamer(func(s string) bool {
		if has(s) {
			return true
		}
		if _, ok := byOld[s]; ok {
			return true
		}
		if _, ok := byNew[s]; ok {
			return true
		}
		return temps[s]
	})
	var fragments [][]Step
	for _, ring := range cycle.Find(edges) {
		tmp, err := namer.Next()
		if err != nil {
			return nil, err
		}
		temps[tmp] = true
		frag := []Step{{Old: ring[0], New: tmp, Temp: true}}
		for i := len(ring) - 1; i >= 1; i-- {
			frag = append(frag, Step{Old: ring[i], New: ring[(i+1)%len(ring)]})
		}
		frag = append(frag, Step{Old: tmp, New: ring[1], Temp: true})
		fragments = append(fragments, frag)
		for _, v := range ring {
			delete(edges, v)
		}
	}
	// 剩余依赖图是不相交的链，从目标空闲的一端逆序输出。
	inv := make(map[string]string, len(edges))
	for o, n := range edges {
		inv[n] = o
	}
	for o, n := range edges {
		if _, isNode := edges[n]; isNode {
			continue
		}
		var frag []Step
		for cur := o; ; {
			frag = append(frag, Step{Old: cur, New: edges[cur]})
			prev, ok := inv[cur]
			if !ok {
				break
			}
			cur = prev
		}
		fragments = append(fragments, frag)
	}
	sort.Slice(fragments, func(i, j int) bool {
		return fragments[i][0].Old < fragments[j][0].Old
	})
	for _, f := range fragments {
		p.Steps = append(p.Steps, f...)
	}
	p.Temps = len(temps)
	return p, nil
}

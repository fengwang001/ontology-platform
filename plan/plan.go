// Package plan 在不修改命名空间的前提下检测冲突、计算安全执行顺序并破环。
package plan

import (
	"errors"
	"sort"

	"ontology/cycle"
	"ontology/name"
)

var (
	// ErrTargetExists 目标名已存在且本批不会搬走。
	ErrTargetExists = errors.New("plan: target already exists and is not moved")
	// ErrDupTarget 两个请求指向同一新名。
	ErrDupTarget = errors.New("plan: duplicate target")
	// ErrDupSource 同一旧名出现两次（新名不同）。
	ErrDupSource = errors.New("plan: duplicate source")
	// ErrMissingSource 旧名不存在。
	ErrMissingSource = errors.New("plan: source does not exist")
)

// Req 是一条重命名请求。
type Req struct{ From, To string }

// Step 是一个可执行原子改名。
type Step struct{ From, To string }

// Stats 报告编译统计。Lookups 为 map 读次数；TempNames 为破环临时名数。
type Stats struct {
	Lookups   int
	TempNames int
	Names     int
}

// Compile 在已持写锁的命名空间上做冲突检测与编排，不产生任何修改。
// 自环 From==To 视为无操作剔除。
func Compile(ns *name.Namespace, reqs []Req) ([]Step, Stats, error) {
	before := ns.SnapshotLocked()
	steps, st, err := compile(ns, reqs)
	if err != nil {
		if got := ns.SnapshotLocked(); !name.Equal(got, before) {
			panic("plan: namespace changed during failed compile")
		}
	}
	return steps, st, err
}

func compile(ns *name.Namespace, reqs []Req) ([]Step, Stats, error) {
	edge := map[string]string{}
	targetOwner := map[string]string{}
	toSet := map[string]struct{}{}
	involved := map[string]struct{}{}
	var reqList []Req

	for _, r := range reqs {
		if r.From == r.To {
			continue // 自环：无操作
		}
		if prev, ok := edge[r.From]; ok && prev != r.To {
			return nil, Stats{}, &ConflictError{Kind: ErrDupSource,
				Names: []string{r.To, prev}}
		}
		if owner, ok := targetOwner[r.To]; ok && owner != r.From {
			return nil, Stats{}, &ConflictError{Kind: ErrDupTarget,
				Names: []string{owner, r.From}}
		}
		edge[r.From] = r.To
		targetOwner[r.To] = r.From
		toSet[r.To] = struct{}{}
		involved[r.From] = struct{}{}
		involved[r.To] = struct{}{}
		reqList = append(reqList, r)
	}
	for _, r := range reqList {
		if !ns.HasLocked(r.From) {
			return nil, Stats{}, &ConflictError{Kind: ErrMissingSource,
				Names: []string{r.From}}
		}
	}
	for _, r := range reqList {
		if ns.HasLocked(r.To) {
			if _, moved := edge[r.To]; !moved {
				return nil, Stats{}, &ConflictError{Kind: ErrTargetExists,
					Names: []string{r.To}}
			}
		}
	}

	remaining := map[string]string{}
	for f, t := range edge {
		remaining[f] = t
	}
	ready := func() []string {
		var rs []string
		for f, t := range remaining {
			if _, isPendingFrom := remaining[t]; !isPendingFrom {
				rs = append(rs, f)
			}
		}
		sort.Strings(rs)
		return rs
	}
	var steps []Step
	for {
		rs := ready()
		if len(rs) == 0 {
			break
		}
		for _, f := range rs {
			steps = append(steps, Step{f, remaining[f]})
			delete(remaining, f)
		}
	}

	var cycNodes []string
	for f := range remaining {
		cycNodes = append(cycNodes, f)
	}
	sort.Strings(cycNodes)
	seen := map[string]bool{}
	tempCount := 0
	for _, anchor := range cycNodes {
		if seen[anchor] {
			continue
		}
		var ring []string
		cur := anchor
		for {
			seen[cur] = true
			ring = append(ring, cur)
			cur = remaining[cur]
			if cur == anchor {
				break
			}
		}
		set := cycle.BuildNameSet(ns.SnapshotLocked(), keys(toSet))
		tmp, err := cycle.TempName(func(c string) bool { _, ok := set[c]; return ok })
		if err != nil {
			return nil, Stats{}, err
		}
		set[tmp] = struct{}{}
		tempCount++
		steps = append(steps, Step{ring[0], tmp})
		for i := len(ring) - 1; i >= 1; i-- {
			steps = append(steps, Step{ring[i], remaining[ring[i]]})
		}
		steps = append(steps, Step{tmp, remaining[ring[0]]})
	}

	return steps, Stats{Lookups: ns.Lookups, TempNames: tempCount,
		Names: len(involved)}, nil
}

func keys(m map[string]struct{}) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// ConflictError 携带可 errors.Is 判别的 Kind 与相关名字。
type ConflictError struct {
	Kind  error
	Names []string
}

func (e *ConflictError) Error() string { return e.Kind.Error() + ": " + join(e.Names) }
func (e *ConflictError) Unwrap() error { return e.Kind }

func join(ss []string) string {
	out := ""
	for i, s := range ss {
		if i > 0 {
			out += ","
		}
		out += s
	}
	return out
}

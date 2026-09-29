package rename

import (
	"fmt"
	"sort"
	"strconv"
)

// step 是一个不可再分的改名动作。执行时 from 必须存在、to 必须不存在。
type step struct {
	From string
	To   string
}

// component 是映射图的一个连通部分：要么是环，要么是链（以一个不搬走的
// 既有名字或全新名字为终点）。
type component struct {
	min    string   // 该组件中字典序最小的名字，决定处理顺序
	nodes  []string // 从“末端”到“始端”排列的链；环中从最小名开始
	cyclic bool
}

// plan 是一个批次推导出来的确定性执行序列。
type plan struct {
	steps []step
	temp  []string
}

// buildPlan 在给定命名空间快照下校验映射并推导执行序列。
// 任何一条规则不满足都返回 *ValidationError，且不产生任何副作用。
//
// 判定顺序固定，保证错误原因与映射项给出顺序无关：
//  1. 名字为空
//  2. 同一旧名出现两次
//  3. 两个旧名映射到同一新名
//  4. 旧名不存在
//  5. 新名已存在且不在本批被搬走的旧名中
func buildPlan(names map[string]struct{}, renames []Rename, tempPrefix string) (*plan, error) {
	seenOld := make(map[string]int)
	seenNew := make(map[string]string)

	for i, r := range renames {
		if r.Old == "" || r.New == "" {
			return nil, &ValidationError{
				Reason: ReasonEmptyName,
				Detail: fmt.Sprintf("rename #%d has an empty name: old=%q new=%q", i, r.Old, r.New),
				Rename: r,
			}
		}
	}

	for i, r := range renames {
		if first, dup := seenOld[r.Old]; dup {
			return nil, &ValidationError{
				Reason:      ReasonDuplicateOld,
				Detail:      fmt.Sprintf("old name %q appears more than once (first at index %d)", r.Old, first),
				Rename:      r,
				Conflicting: r.Old,
			}
		}
		seenOld[r.Old] = i
	}

	// 仅对真正生效的映射检查新名冲突；映射到自身不算占住一个新名。
	for _, r := range renames {
		if r.Old == r.New {
			continue
		}
		if other, dup := seenNew[r.New]; dup && other != r.Old {
			return nil, &ValidationError{
				Reason:      ReasonDuplicateNew,
				Detail:      fmt.Sprintf("new name %q is claimed by both %q and %q", r.New, other, r.Old),
				Rename:      r,
				Conflicting: other,
			}
		}
		seenNew[r.New] = r.Old
	}

	for _, r := range renames {
		if _, ok := names[r.Old]; !ok {
			return nil, &ValidationError{
				Reason: ReasonOldNotFound,
				Detail: fmt.Sprintf("old name %q does not exist in the namespace", r.Old),
				Rename: r,
			}
		}
	}

	moved := make(map[string]struct{}, len(seenOld))
	for _, r := range renames {
		if r.Old == r.New {
			continue
		}
		moved[r.Old] = struct{}{}
	}
	for _, r := range renames {
		if r.Old == r.New {
			continue
		}
		if _, occupied := names[r.New]; occupied {
			if _, isMoved := moved[r.New]; !isMoved {
				return nil, &ValidationError{
					Reason:      ReasonNewOccupied,
					Detail:      fmt.Sprintf("new name %q already exists and is not moved by this batch", r.New),
					Rename:      r,
					Conflicting: r.New,
				}
			}
		}
	}

	// 丢弃无操作映射，建立 old -> new 的功能图。
	next := make(map[string]string, len(renames))
	for _, r := range renames {
		if r.Old != r.New {
			next[r.Old] = r.New
		}
	}

	components := decompose(next)

	// 临时名候选既不能被当前命名空间占用，也不能与本批出现的任何名字相同
	// （旧名与新名都算）。多个环按组件最小名升序依次领取最小的可用序号。
	batchNames := make(map[string]struct{})
	for _, r := range renames {
		batchNames[r.Old] = struct{}{}
		batchNames[r.New] = struct{}{}
	}
	tempTaken := func(candidate string) bool {
		if _, ok := names[candidate]; ok {
			return true
		}
		if _, ok := batchNames[candidate]; ok {
			return true
		}
		return false
	}

	p := &plan{}
	tempByCycleStart := make(map[string]string)
	nextTempIndex := 0
	allocTemp := func() string {
		for {
			candidate := tempPrefix + strconv.Itoa(nextTempIndex)
			nextTempIndex++
			if !tempTaken(candidate) {
				p.temp = append(p.temp, candidate)
				return candidate
			}
		}
	}

	for _, c := range components {
		if c.cyclic {
			// c.nodes[0] 是环中字典序最小的名字。
			tmp := allocTemp()
			tempByCycleStart[c.nodes[0]] = tmp
		}
	}

	for _, c := range components {
		if c.cyclic {
			start := c.nodes[0]
			tmp := tempByCycleStart[start]
			// 先把最小名搬到临时名破环，随后沿环依次搬入空出的名字。
			p.steps = append(p.steps, step{From: start, To: tmp})
			// nodes 顺序为 [start, next(start), ..., 前驱]。
			for i := len(c.nodes) - 1; i >= 1; i-- {
				p.steps = append(p.steps, step{From: c.nodes[i], To: next[c.nodes[i]]})
			}
			// 最后把临时名搬到 start 的后继原本占用的名字。
			p.steps = append(p.steps, step{From: tmp, To: next[start]})
			continue
		}
		// 链：nodes 为始端 -> ... -> 末端，执行时倒序，从最靠近末端的
		// 节点搬起，保证每一步的目标已空出或原本不存在。
		for i := len(c.nodes) - 2; i >= 0; i-- {
			mover := c.nodes[i]
			p.steps = append(p.steps, step{From: mover, To: next[mover]})
		}
	}

	return p, nil
}

// decompose 把功能图拆成若干链或环。
// 每个节点的出度至多为 1；入度为 0 的节点必是链的始端，
// 其余节点属于环。返回结果按各组件最小名字升序排列。
func decompose(next map[string]string) []component {
	indeg := make(map[string]int)
	for from, to := range next {
		if _, ok := indeg[from]; !ok {
			indeg[from] = 0
		}
		indeg[to]++
	}

	var comps []component

	// Kahn 剥离：入度为 0 的节点没有前驱，是一条链的“始端”（最先要搬走的）。
	// 按字典序处理同一层节点，使结果确定且与输入顺序无关。
	var frontier []string
	for node, deg := range indeg {
		if deg == 0 {
			frontier = append(frontier, node)
		}
	}
	sort.Strings(frontier)

	peeled := make(map[string]bool)
	for len(frontier) > 0 {
		node := frontier[0]
		frontier = frontier[1:]
		if peeled[node] {
			continue
		}
		peeled[node] = true
		if nxt, ok := next[node]; ok {
			indeg[nxt]--
			if indeg[nxt] == 0 {
				frontier = insertSorted(frontier, nxt)
			}
		}
	}

	// 被剥离的节点构成链；从每个仍有后继的始端（未被别人指向的已剥离节点）
	// 走到末端，得到始端 -> ... -> 末端。执行时倒序（末端先搬）。
	chainStart := make(map[string]bool)
	for node := range peeled {
		hasIncoming := false
		for _, to := range next {
			if to == node {
				hasIncoming = true
				break
			}
		}
		if !hasIncoming {
			chainStart[node] = true
		}
	}
	var starts []string
	for node := range chainStart {
		starts = append(starts, node)
	}
	sort.Strings(starts)
	for _, start := range starts {
		var ordered []string
		cur := start
		for {
			ordered = append(ordered, cur)
			nxt, ok := next[cur]
			if !ok || !peeled[nxt] {
				break
			}
			cur = nxt
		}
		// nodes 仍按始端 -> 末端存放，执行时倒序。
		comps = append(comps, component{min: sliceMin(ordered), nodes: ordered})
	}

	// 未被剥离的节点恰为环上节点。
	var onCycle []string
	for node := range indeg {
		if !peeled[node] {
			onCycle = append(onCycle, node)
		}
	}
	sort.Strings(onCycle)
	handled := make(map[string]bool)
	for _, node := range onCycle {
		if handled[node] {
			continue
		}
		var ring []string
		cur := node
		for {
			handled[cur] = true
			ring = append(ring, cur)
			cur = next[cur]
			if cur == node {
				break
			}
		}
		// 旋转环，使字典序最小的名字排在第一位（即先搬去临时名的节点）。
		minIdx := 0
		for i := 1; i < len(ring); i++ {
			if ring[i] < ring[minIdx] {
				minIdx = i
			}
		}
		nodes := append(append([]string{}, ring[minIdx:]...), ring[:minIdx]...)
		comps = append(comps, component{min: nodes[0], nodes: nodes, cyclic: true})
	}

	sort.Slice(comps, func(i, j int) bool { return comps[i].min < comps[j].min })
	return comps
}

func insertSorted(sorted []string, v string) []string {
	idx := sort.SearchStrings(sorted, v)
	sorted = append(sorted, "")
	copy(sorted[idx+1:], sorted[idx:])
	sorted[idx] = v
	return sorted
}

func sliceMin(names []string) string {
	min := names[0]
	for _, name := range names[1:] {
		if name < min {
			min = name
		}
	}
	return min
}

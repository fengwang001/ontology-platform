package rename

import (
	"fmt"
	"sort"
	"strings"
)

// component 是映射图中的一个连通分量：一条链或一个环。
type component struct {
	cycle bool
	names []string // 分量中出现的全部名字（旧名与新名）
	steps []Step   // 该分量的单步序列
	temps []string // 该分量借用的临时名（环恰一个，链为零）
}

// minName 返回分量中字典序最小的名字，用于分量间排序。
func (c *component) minName() string {
	min := c.names[0]
	for _, n := range c.names[1:] {
		if n < min {
			min = n
		}
	}
	return min
}

// planResult 是推导出的执行计划。
type planResult struct {
	steps []Step
	comps []component
}

// String 打印计划及判定依据（链/环分解与临时名选择），供日志使用。
func (p *planResult) String() string {
	var b strings.Builder
	for i, c := range p.comps {
		kind := "chain"
		if c.cycle {
			kind = "cycle"
		}
		fmt.Fprintf(&b, "component#%d kind=%s key=%q temps=%v steps=%v\n", i, kind, c.minName(), c.temps, c.steps)
	}
	fmt.Fprintf(&b, "steps=%v", p.steps)
	return b.String()
}

// plan 校验映射并推导单步序列。校验失败返回可区分的错误，不产生任何副作用。
func plan(pairs []Pair, tempPrefix string, ns *Namespace) (*planResult, error) {
	// 1. 名字为空。
	for _, p := range pairs {
		if p.Old == "" || p.New == "" {
			return nil, fmt.Errorf("%w: (%q -> %q)", ErrEmptyName, p.Old, p.New)
		}
	}
	// 2. 同一旧名出现两次。
	seenOld := make(map[string]struct{}, len(pairs))
	for _, p := range pairs {
		if _, ok := seenOld[p.Old]; ok {
			return nil, fmt.Errorf("%w: %q", ErrDuplicateOld, p.Old)
		}
		seenOld[p.Old] = struct{}{}
	}
	// 3. 映射到自身视为无操作，先行剔除。
	f := make(map[string]string, len(pairs))
	for _, p := range pairs {
		if p.Old != p.New {
			f[p.Old] = p.New
		}
	}
	// 4. 两个旧名映射到同一新名。
	seenNew := make(map[string]string, len(f))
	for old, new := range f {
		if prev, ok := seenNew[new]; ok {
			return nil, fmt.Errorf("%w: %q and %q both map to %q", ErrConflictingNew, prev, old, new)
		}
		seenNew[new] = old
	}
	// 5. 旧名必须存在。
	olds := make([]string, 0, len(f))
	for old := range f {
		if !ns.Has(old) {
			return nil, fmt.Errorf("%w: %q", ErrOldNotFound, old)
		}
		olds = append(olds, old)
	}
	sort.Strings(olds)
	// 6. 新名已存在且不在本批被搬走的旧名中。
	for _, old := range olds {
		new := f[old]
		if _, moved := f[new]; !moved && ns.Has(new) {
			return nil, fmt.Errorf("%w: %q", ErrNewNameExists, new)
		}
	}

	// 占用集合：命名空间现有名字 ∪ 本批出现的所有名字 ∪ 已分配的临时名。
	used := make(map[string]struct{}, len(ns.names)+2*len(f))
	for name := range ns.names {
		used[name] = struct{}{}
	}
	for old, new := range f {
		used[old] = struct{}{}
		used[new] = struct{}{}
	}
	allocTemp := func() string {
		for i := 0; ; i++ {
			cand := fmt.Sprintf("%s%d", tempPrefix, i)
			if _, taken := used[cand]; !taken {
				used[cand] = struct{}{}
				return cand
			}
		}
	}

	// 分解映射图：入度为 0 的旧名是链头，其余节点构成环。
	isTarget := make(map[string]struct{}, len(f))
	for _, new := range f {
		isTarget[new] = struct{}{}
	}
	var comps []component
	inComponent := make(map[string]struct{}, len(f))

	// 链：从链头走到链尾，再自链尾向链头倒序改名。
	for _, head := range olds {
		if _, ok := isTarget[head]; ok {
			continue
		}
		var chain []string // 链上的旧名，head 在前
		for cur := head; ; cur = f[cur] {
			chain = append(chain, cur)
			inComponent[cur] = struct{}{}
			next, ok := f[cur]
			if !ok {
				break
			}
			if _, isOld := f[next]; !isOld {
				chain = append(chain, next) // 链尾的新名（不是任何旧名）
				break
			}
		}
		c := component{names: chain}
		for i := len(chain) - 1; i > 0; i-- {
			c.steps = append(c.steps, Step{Old: chain[i-1], New: chain[i]})
		}
		comps = append(comps, c)
	}

	// 环：从环中字典序最小的名字先改为临时名破环，再沿链倒序，最后临时名归位。
	for _, start := range olds {
		if _, ok := inComponent[start]; ok {
			continue
		}
		var cyc []string
		for cur := start; ; cur = f[cur] {
			cyc = append(cyc, cur)
			inComponent[cur] = struct{}{}
			if f[cur] == start {
				break
			}
		}
		// 旋转使字典序最小的名字在 cyc[0]。
		minIdx := 0
		for i, name := range cyc {
			if name < cyc[minIdx] {
				minIdx = i
			}
		}
		rot := make([]string, 0, len(cyc))
		for i := 0; i < len(cyc); i++ {
			rot = append(rot, cyc[(minIdx+i)%len(cyc)])
		}
		temp := allocTemp()
		c := component{cycle: true, names: rot, temps: []string{temp}}
		m := len(rot)
		c.steps = append(c.steps, Step{Old: rot[0], New: temp})
		for i := m - 1; i >= 1; i-- {
			next := rot[0]
			if i < m-1 {
				next = rot[i+1]
			}
			c.steps = append(c.steps, Step{Old: rot[i], New: next})
		}
		c.steps = append(c.steps, Step{Old: temp, New: rot[1%m]})
		comps = append(comps, c)
	}

	// 各链与环按其中字典序最小的名字升序处理，与映射项给出顺序无关。
	sort.SliceStable(comps, func(i, j int) bool {
		return comps[i].minName() < comps[j].minName()
	})

	p := &planResult{comps: comps}
	for i := range comps {
		p.steps = append(p.steps, comps[i].steps...)
	}
	return p, nil
}

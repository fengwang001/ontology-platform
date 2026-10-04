// Package nhgroup 维护下一跳组的成员、权重与存活状态，并计算目标桶数。
package nhgroup

import "sort"

// Spec 描述建组时的一个成员（下一跳编号，权重）。
type Spec struct {
	NH     int
	Weight int
}

// Group 是组的成员视图：纯数据，不加锁，由上层串行调用。
type Group struct {
	nh     []int
	weight map[int]int
	alive  map[int]bool
}

// New 按给定成员创建组；调用方保证编号不重复、参数合法。
func New(specs []Spec) *Group {
	g := &Group{weight: map[int]int{}, alive: map[int]bool{}}
	for _, s := range specs {
		g.nh = append(g.nh, s.NH)
		g.weight[s.NH] = s.Weight
		g.alive[s.NH] = true
	}
	sort.Ints(g.nh)
	return g
}

// Add 加入成员；存活状态由调用方给定。
func (g *Group) Add(nh, weight int, alive bool) {
	i := sort.SearchInts(g.nh, nh)
	g.nh = append(g.nh, 0)
	copy(g.nh[i+1:], g.nh[i:])
	g.nh[i] = nh
	g.weight[nh] = weight
	g.alive[nh] = alive
}

// Remove 删除成员。
func (g *Group) Remove(nh int) {
	i := sort.SearchInts(g.nh, nh)
	if i == len(g.nh) || g.nh[i] != nh {
		return
	}
	g.nh = append(g.nh[:i], g.nh[i+1:]...)
	delete(g.weight, nh)
	delete(g.alive, nh)
}

// SetWeight 修改成员权重。
func (g *Group) SetWeight(nh, weight int) { g.weight[nh] = weight }

// SetAlive 设置成员存活状态。
func (g *Group) SetAlive(nh int, alive bool) { g.alive[nh] = alive }

// Has 报告成员是否存在。
func (g *Group) Has(nh int) bool {
	_, ok := g.weight[nh]
	return ok
}

// Members 按编号升序返回全部成员（含失效成员）。
func (g *Group) Members() []int { return append([]int(nil), g.nh...) }

// AliveMembers 按编号升序返回存活成员。
func (g *Group) AliveMembers() []int {
	out := make([]int, 0, len(g.nh))
	for _, nh := range g.nh {
		if g.alive[nh] {
			out = append(out, nh)
		}
	}
	return out
}

// Weight 返回成员权重。
func (g *Group) Weight(nh int) int { return g.weight[nh] }

// Alive 返回成员是否存活。
func (g *Group) Alive(nh int) bool { return g.alive[nh] }

// Targets 用最大余数法计算 n 个桶在存活成员间的目标分配：
// 先分 floor(n*w/W)，剩余桶按余数降序、并列编号升序各补 1。
func (g *Group) Targets(n int) map[int]int {
	alive := g.AliveMembers()
	targets := make(map[int]int, len(alive))
	if len(alive) == 0 {
		return targets
	}
	total := 0
	for _, nh := range alive {
		total += g.weight[nh]
	}
	type rem struct {
		nh int
		r  int
	}
	rems := make([]rem, 0, len(alive))
	left := n
	for _, nh := range alive {
		scaled := n * g.weight[nh]
		base := scaled / total
		targets[nh] = base
		left -= base
		rems = append(rems, rem{nh: nh, r: scaled % total})
	}
	sort.SliceStable(rems, func(i, j int) bool {
		if rems[i].r != rems[j].r {
			return rems[i].r > rems[j].r
		}
		return rems[i].nh < rems[j].nh
	})
	for k := 0; k < left; k++ {
		targets[rems[k].nh]++
	}
	return targets
}

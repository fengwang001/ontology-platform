package ontology

import (
	"sort"
	"sync"
)

// DedupCounter 在撤回式变更流上按分组增量维护去重计数。
//
// 对每个 (组, 值) 维护净多重性 = 插入次数 - 撤回次数；
// 组的去重计数 = 该组内净多重性严格为正的值的个数。
//
// Apply 以批为单位原子执行：任一条目不合法则整批拒绝，
// 多重性与计数视图保持批前状态不变。所有方法均可被并发调用。
type DedupCounter struct {
	mu sync.RWMutex
	// mult[g][v] 为 (g,v) 的净多重性；零条目会被删除，空组映射会被删除。
	mult map[string]map[string]int
	// counts[g] 为组 g 当前的去重计数；计数归零的组会被删除。
	counts map[string]int
	// maxEntries 为单批允许的最大条目数。
	maxEntries int
}

// NewDedupCounter 创建一个单批次条目数上限为 maxEntries 的计数器。
// 非正上限会被归一为 0：仅空批可被接受。
func NewDedupCounter(maxEntries int) *DedupCounter {
	if maxEntries < 0 {
		maxEntries = 0
	}
	return &DedupCounter{
		mult:       make(map[string]map[string]int),
		counts:     make(map[string]int),
		maxEntries: maxEntries,
	}
}

// Apply 按条目顺序原子地应用一批变更，返回判定结果。
//
// 校验顺序：先检查批次条目数上限，再按顺序逐条检查空组名、空值、
// 非法符号；撤回在“批前已提交状态叠加批内此前各条已生效变更”的
// 临时状态上校验，撤回当前净多重性为零的值会拒绝整批。
//
// 接受时 Changes 按组名升序给出每个被触及组的批前/批后去重计数及净变化；
// 拒绝时 Accepted 为 false，Reason 给出可区分原因，EntryIndex 为首个
// 违法条目的下标（条目数超限为 -1），内部状态不发生任何改变。
func (c *DedupCounter) Apply(changes []Change) ApplyResult {
	c.mu.Lock()
	defer c.mu.Unlock()

	if len(changes) > c.maxEntries {
		return ApplyResult{Accepted: false, Reason: ReasonTooManyEntries, EntryIndex: -1}
	}

	// 批内临时净变化：prov[g][v] 相对已提交多重性的偏移，只记录被触及的键。
	prov := make(map[string]map[string]int)
	groups := make([]string, 0)

	for i, ch := range changes {
		if ch.Group == "" {
			return ApplyResult{Accepted: false, Reason: ReasonEmptyGroup, EntryIndex: i}
		}
		if ch.Value == "" {
			return ApplyResult{Accepted: false, Reason: ReasonEmptyValue, EntryIndex: i}
		}
		if ch.Kind != KindInsert && ch.Kind != KindRetract {
			return ApplyResult{Accepted: false, Reason: ReasonInvalidKind, EntryIndex: i}
		}

		gp, ok := prov[ch.Group]
		if !ok {
			gp = make(map[string]int)
			prov[ch.Group] = gp
			groups = append(groups, ch.Group)
		}

		cur := c.mult[ch.Group][ch.Value] + gp[ch.Value]
		switch ch.Kind {
		case KindInsert:
			gp[ch.Value]++
		case KindRetract:
			// 撤回在批内此前各条已生效的状态上校验：当前净多重性为零即拒绝。
			if cur <= 0 {
				return ApplyResult{Accepted: false, Reason: ReasonRetractZero, EntryIndex: i}
			}
			gp[ch.Value]--
		}
	}

	// 整批合法：折叠出各组批前/批后去重计数并一次性提交。
	sort.Strings(groups)
	deltas := make([]GroupDelta, 0, len(groups))
	for _, g := range groups {
		before := c.counts[g]
		after := before
		for v, d := range prov[g] {
			old := c.mult[g][v]
			after += posOne(old+d) - posOne(old)
		}
		deltas = append(deltas, GroupDelta{Group: g, Before: before, After: after, Delta: after - before})
	}

	for _, g := range groups {
		gm := c.mult[g]
		if gm == nil {
			gm = make(map[string]int)
			c.mult[g] = gm
		}
		for v, d := range prov[g] {
			newMult := gm[v] + d
			if newMult == 0 {
				delete(gm, v)
			} else {
				gm[v] = newMult
			}
		}
		if len(gm) == 0 {
			delete(c.mult, g)
			delete(c.counts, g)
		} else {
			c.counts[g] = len(gm)
		}
	}

	return ApplyResult{Accepted: true, Reason: ReasonNone, EntryIndex: -1, Changes: deltas}
}

// View 返回各组当前去重计数的一致快照（深拷贝），
// 仅包含去重计数为正的组。并发调用得到的快照逐字段一致。
func (c *DedupCounter) View() map[string]int {
	c.mu.RLock()
	defer c.mu.RUnlock()

	out := make(map[string]int, len(c.counts))
	for g, n := range c.counts {
		out[g] = n
	}
	return out
}

// Multiplicity 返回指定 (组, 值) 当前的净多重性；不存在时为 0。
func (c *DedupCounter) Multiplicity(group, value string) int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.mult[group][value]
}

// posOne 在净多重性严格为正时计 1，否则计 0。
func posOne(m int) int {
	if m > 0 {
		return 1
	}
	return 0
}

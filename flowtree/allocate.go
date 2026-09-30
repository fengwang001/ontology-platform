package flowtree

import "sort"

// Allocate 在给定就绪集合下，把 quota 字节按树分配，返回 流编号 -> 份额。
//
// 拒绝顺序：就绪流不存在 -> 额度为负。被拒绝时不产生任何结果。
//
// 分配自虚拟根向下递归：
//   - 只考虑子树中含就绪流的子；
//   - 子树根自身就绪时，额度全归它，其后代得 0；
//   - 否则在其含就绪流的子之间按下式切分：
//     基数 base(w) = floor(T*w / Σw)，Σw 只含含就绪流的兄弟；
//     剩余 r = T - Σbase 字节，按 (T*w mod Σw) 从大到小取前 r 个
//     各加 1，余数并列时取编号小者。
//
// 无就绪流时所有份额均为 0，返回空映射。
func (a *Allocator) Allocate(quota int, ready []int) (map[int]int, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	readySet := make(map[int]bool, len(ready))
	for _, id := range ready {
		if _, ok := a.nodes[id]; !ok {
			return nil, ErrReadyMissing
		}
		readySet[id] = true
	}
	if quota < 0 {
		return nil, ErrNegativeQuota
	}

	shares := make(map[int]int)
	if quota == 0 || len(readySet) == 0 {
		return shares, nil
	}

	// subtreeHasReady 标记含就绪流的节点（含就绪节点自身）。
	hasReady := make(map[int]bool, len(a.nodes))
	var mark func(id int) bool
	mark = func(id int) bool {
		n := a.nodes[id]
		hit := readySet[id]
		for _, kid := range n.children {
			if mark(kid) {
				hit = true
			}
		}
		hasReady[id] = hit
		return hit
	}
	for _, id := range a.rootCh {
		mark(id)
	}

	var split func(parent, amount int)
	split = func(parent, amount int) {
		if amount <= 0 {
			return
		}

		// 仅保留子树中含就绪流的子；children 已按编号升序。
		var eligible []int
		for _, kid := range a.childrenOf(parent) {
			if hasReady[kid] {
				eligible = append(eligible, kid)
			}
		}
		if len(eligible) == 0 {
			return
		}

		sumW := 0
		for _, kid := range eligible {
			sumW += a.nodes[kid].weight
		}

		type part struct {
			id   int
			base int
			rem  int
		}
		parts := make([]part, len(eligible))
		baseSum := 0
		for i, kid := range eligible {
			w := a.nodes[kid].weight
			// base = floor(amount*w/sumW)，拆开 amount 防止乘积溢出。
			q, rem := amount/sumW, amount%sumW
			base := q*w + rem*w/sumW
			mod := rem * w % sumW
			parts[i] = part{id: kid, base: base, rem: mod}
			baseSum += base
		}

		leftover := amount - baseSum

		// 余数大者多得 1；余数并列取编号小者。parts 已按编号升序，
		// 稳定排序按余数降序即可同时满足两条规则。
		order := make([]int, len(parts))
		for i := range parts {
			order[i] = i
		}
		sort.SliceStable(order, func(i, j int) bool {
			return parts[order[i]].rem > parts[order[j]].rem
		})
		for i := 0; i < leftover; i++ {
			parts[order[i]].base++
		}

		for _, p := range parts {
			if readySet[p.id] {
				// 子树根就绪：额度全归它，后代一律 0。
				shares[p.id] += p.base
			} else {
				split(p.id, p.base)
			}
		}
	}

	split(0, quota)
	return shares, nil
}

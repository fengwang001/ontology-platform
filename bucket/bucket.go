// Package bucket 维护组的桶表，提供立即指派与惰性迁移。
package bucket

import "sort"

// Entry 是一个桶当前的主人与最近使用时间。
type Entry struct {
	Owner    int
	LastUsed int64
}

// Table 是固定桶数的桶表。
type Table struct {
	n       int
	buckets []Entry
	touched int
}

// New 创建 n 个空桶的桶表。
func New(n int) *Table {
	return &Table{n: n, buckets: make([]Entry, n)}
}

// N 返回桶数。
func (t *Table) N() int { return t.n }

// Snapshot 返回桶表副本。
func (t *Table) Snapshot() []Entry { return append([]Entry(nil), t.buckets...) }

// ResetTouched 清零触碰计数。
func (t *Table) ResetTouched() { t.touched = 0 }

// Touched 返回自上次 ResetTouched 以来触碰的桶数。
func (t *Table) Touched() int { return t.touched }

func (t *Table) counts() map[int]int {
	counts := make(map[int]int, 8)
	for _, b := range t.buckets {
		counts[b.Owner]++
	}
	return counts
}

// Balanced 报告当前桶表对给定目标是否平衡（无空桶且无成员盈余；无存活成员时全空）。
func (t *Table) Balanced(targets map[int]int) bool {
	return balanced(targets, t.counts())
}

// pickDeficit 返回当前亏额最大的存活成员（并列编号小）；无亏额成员时 ok=false。
func pickDeficit(targets, counts map[int]int) (int, bool) {
	members := make([]int, 0, len(targets))
	for nh := range targets {
		members = append(members, nh)
	}
	sort.Ints(members)
	best, bestDef := 0, 0
	for _, nh := range members {
		def := targets[nh] - counts[nh]
		if def > bestDef {
			best, bestDef = nh, def
		}
	}
	return best, bestDef > 0
}

// balanced 报告“无空桶且无成员盈余”（targets 为空时要求全部桶为空）。
func balanced(targets, counts map[int]int) bool {
	if len(targets) == 0 {
		for nh, c := range counts {
			if nh != 0 && c > 0 {
				return false
			}
		}
		return true
	}
	if counts[0] > 0 {
		return false
	}
	for nh, have := range counts {
		if nh != 0 && have > targets[nh] {
			return false
		}
	}
	return true
}

// Assign 立即把 indices（升序）中的桶逐个指给当前亏额最大的存活成员；
// 找不到亏额成员的桶置空。lastUsed 保持不变。
func (t *Table) Assign(indices []int, targets map[int]int) {
	idx := append([]int(nil), indices...)
	sort.Ints(idx)
	counts := t.counts()
	for _, i := range idx {
		t.touched++
		dest, ok := pickDeficit(targets, counts)
		old := t.buckets[i].Owner
		counts[old]--
		if counts[old] == 0 {
			delete(counts, old)
		}
		if !ok {
			t.buckets[i].Owner = 0
			counts[0]++
			continue
		}
		t.buckets[i].Owner = dest
		counts[dest]++
	}
}

// AssignAll 初始化整表：把全部桶按立即指派规则分配，lastUsed 取 now。
func (t *Table) AssignAll(targets map[int]int, now int64) {
	idx := make([]int, t.n)
	for i := range idx {
		idx[i] = i
	}
	t.Assign(idx, targets)
	for i := range t.buckets {
		t.buckets[i].LastUsed = now
	}
}

// Reconcile 做一趟升序整理，返回整理后是否平衡。
func (t *Table) Reconcile(targets map[int]int, now, ti, unbalancedSince int64, tu int64) bool {
	if len(targets) == 0 {
		return true
	}
	forced := tu > 0 && now-unbalancedSince >= tu
	counts := t.counts()
	for i := 0; i < t.n; i++ {
		t.touched++
		owner := t.buckets[i].Owner
		if owner == 0 || counts[owner] <= targets[owner] {
			continue
		}
		if !forced && now-t.buckets[i].LastUsed < ti {
			continue
		}
		dest, ok := pickDeficit(targets, counts)
		if !ok {
			continue
		}
		counts[owner]--
		t.buckets[i].Owner = dest
		counts[dest]++
	}
	return balanced(targets, counts)
}

// Lookup 返回 hash 对应桶的主人；空桶第二返回值为 false，并更新 lastUsed。
func (t *Table) Lookup(hash uint64, now int64) (int, bool) {
	i := int(hash % uint64(t.n))
	t.touched++
	b := &t.buckets[i]
	b.LastUsed = now
	if b.Owner == 0 {
		return 0, false
	}
	return b.Owner, true
}

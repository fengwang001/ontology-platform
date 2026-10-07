package snapshot

import "hash/fnv"

// countingIndex 是带探测计数的开放寻址哈希集合。
//
// 规模无关性：Contains 的期望探测次数由负载因子（<=0.5）决定，
// 与表中总记录数 N 无关（线性探测期望探测约 1/(1-alpha) 次）。
// TotalProbes/MaxProbes 暴露真实探测次数，测试用它在
// 多个数量级规模上验证“检查一条引用的代价不随 N 线性增长”。
type countingIndex struct {
	slots       []string
	used        []bool
	mask        uint64
	totalProbes int
	maxProbes   int
}

func newCountingIndex(ids []string) *countingIndex {
	size := 8
	for size < len(ids)*2 { // 负载因子 <= 0.5
		size <<= 1
	}
	return &countingIndex{
		slots: make([]string, size),
		used:  make([]bool, size),
		mask:  uint64(size - 1),
	}
}

func (i *countingIndex) hash(id string) uint64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(id))
	return h.Sum64()
}

func (i *countingIndex) insert(id string) {
	pos := i.hash(id) & i.mask
	for i.used[pos] {
		if i.slots[pos] == id {
			return
		}
		pos = (pos + 1) & i.mask
	}
	i.used[pos] = true
	i.slots[pos] = id
}

// Contains 返回 id 是否存在以及本次实际探测的槽位数。
// 空槽即终止：开放寻址语义下该元素不可能存在。
// 期望代价 O(1)，不随目标块总记录数线性增长；
// 最坏情形以表长为界，由负载因子与 FNV 分布共同约束。
func (i *countingIndex) Contains(id string) (found bool, probes int) {
	pos := i.hash(id) & i.mask
	for {
		probes++
		if !i.used[pos] {
			i.record(probes)
			return false, probes
		}
		if i.slots[pos] == id {
			i.record(probes)
			return true, probes
		}
		pos = (pos + 1) & i.mask
	}
}

func (i *countingIndex) record(probes int) {
	i.totalProbes += probes
	if probes > i.maxProbes {
		i.maxProbes = probes
	}
}

func (i *countingIndex) MaxProbes() int   { return i.maxProbes }
func (i *countingIndex) TotalProbes() int { return i.totalProbes }

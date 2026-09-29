package prefixsum

import "sort"

// naiveModel 是测试用朴素模型：map + 全量重算，作为正确性基准。
type naiveModel struct {
	min, max int64
	data     map[int64]int64
}

func newNaive(min, max int64) *naiveModel {
	return &naiveModel{min: min, max: max, data: map[int64]int64{}}
}

// put 朴素执行写入，并返回与实现相同口径的受影响键数。
func (m *naiveModel) put(key, val int64) (affected int) {
	old, existed := m.data[key]
	if existed {
		if old == val {
			return 0
		}
	}
	m.data[key] = val
	if !existed {
		affected++ // 新插入键一律计一
	}
	keys := m.sortedKeys()
	for _, k := range keys {
		switch {
		case !existed && k > key:
			affected++
		case existed && k >= key:
			affected++
		}
	}
	return affected
}

// del 朴素执行删除，返回与实现相同口径的受影响键数（被删键不计）。
func (m *naiveModel) del(key int64) (affected int) {
	if _, ok := m.data[key]; !ok {
		return -1
	}
	delete(m.data, key)
	for _, k := range m.sortedKeys() {
		if k > key {
			affected++
		}
	}
	return affected
}

func (m *naiveModel) sortedKeys() []int64 {
	keys := make([]int64, 0, len(m.data))
	for k := range m.data {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	return keys
}

// recompute 全量重算每个存在键的前缀和，同时判断是否溢出。
func (m *naiveModel) recompute() ([]Entry, bool) {
	var running int64
	out := make([]Entry, 0, len(m.data))
	for _, k := range m.sortedKeys() {
		v := m.data[k]
		if v > 0 && running > (1<<63-1)-v {
			return nil, false
		}
		if v < 0 && running < (-1<<63)-v {
			return nil, false
		}
		running += v
		out = append(out, Entry{Key: k, Value: v, PrefixSum: running})
	}
	return out, true
}

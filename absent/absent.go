// Package absent 维护单个分区的目标表：键到值的映射与每键缺席计数 a。
package absent

// Effect 表示一次写入对目标表的影响。
type Effect int

const (
	Insert Effect = iota
	Update
	Same
)

// Table 是单分区的目标表。不是并发安全的，由调用方串行化。
type Table struct {
	values map[int64]int64
	absent map[int64]int
}

func NewTable() *Table {
	return &Table{
		values: make(map[int64]int64),
		absent: make(map[int64]int),
	}
}

// Put 写入键 k 的值 v，返回 Insert/Update/Same，并把缺席计数 a 归零：
// 见到即存在，与会话最终是否完整无关。
func (t *Table) Put(k, v int64) Effect {
	old, ok := t.values[k]
	t.values[k] = v
	t.absent[k] = 0
	if !ok {
		return Insert
	}
	if old != v {
		return Update
	}
	return Same
}

// AgeExcept 把不在 seen 中的所有存活键的缺席计数加 1。
func (t *Table) AgeExcept(seen map[int64]struct{}) {
	for k := range t.values {
		if _, ok := seen[k]; !ok {
			t.absent[k]++
		}
	}
}

// Deletable 返回当前缺席计数不小于 k 的全部键。
func (t *Table) Deletable(k int) []int64 {
	var out []int64
	for key, a := range t.absent {
		if a >= k {
			out = append(out, key)
		}
	}
	return out
}

// Delete 从表中移除键及其缺席计数，返回实际删除的键数。
func (t *Table) Delete(keys []int64) int {
	n := 0
	for _, k := range keys {
		if _, ok := t.values[k]; ok {
			delete(t.values, k)
			delete(t.absent, k)
			n++
		}
	}
	return n
}

// Len 返回表中存活键数。
func (t *Table) Len() int {
	return len(t.values)
}

// Get 返回键 k 的值、缺席计数与是否存在。
func (t *Table) Get(k int64) (v int64, a int, ok bool) {
	v, ok = t.values[k]
	if !ok {
		return 0, 0, false
	}
	return v, t.absent[k], true
}

// Keys 返回表中全部存活键。
func (t *Table) Keys() []int64 {
	keys := make([]int64, 0, len(t.values))
	for k := range t.values {
		keys = append(keys, k)
	}
	return keys
}

// Package mem 是 LSM 的写缓冲：一个按 key 有序的内存表，
// 容量达 maxMem 即报告 Full，由上层冻结（Drain）后清空。
package mem

import "sort"

// Entry 是一条写：值、全局序列号 seq、是否墓碑（Del）。
type Entry struct {
	Value   int64
	Seq     int64
	Deleted bool
}

// KV 是冻结时导出的一条有序键值对。
type KV struct {
	Key   string
	Entry Entry
}

// Memtable 不可并发使用，由持有它的上层加锁。
type Memtable struct {
	m      map[string]Entry
	maxMem int
}

// New 创建容量为 maxMem 条的 memtable。
func New(maxMem int) *Memtable {
	return &Memtable{m: make(map[string]Entry), maxMem: maxMem}
}

// Put 写入或覆盖一条（墓碑同样走这里）。
func (t *Memtable) Put(key string, e Entry) {
	t.m[key] = e
}

// Get 命中返回条目与 true。
func (t *Memtable) Get(key string) (Entry, bool) {
	e, ok := t.m[key]
	return e, ok
}

// Len 返回当前条目数。
func (t *Memtable) Len() int { return len(t.m) }

// Full 报告条目数是否已达容量阈值——为 true 时再写必须先冻结。
func (t *Memtable) Full() bool { return len(t.m) >= t.maxMem }

// Drain 按 key 升序导出全部条目并清空自身，产出不可变 SSTable 的输入。
func (t *Memtable) Drain() []KV {
	out := make([]KV, 0, len(t.m))
	for k, e := range t.m {
		out = append(out, KV{Key: k, Entry: e})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	t.m = make(map[string]Entry)
	return out
}

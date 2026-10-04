// Package idmap 维护单张目标表的源键到目标主键（tid）的映射。
//
// 映射在键第一次落库时分配（取 next 并加 1），此后永不改变也不回收；
// 因此 tid 在表内连续无空洞，且分配顺序恰为落库顺序。
package idmap

// Key 是源侧键：分片号 + 源 id。表（kind）由 Mapper 实例本身区分。
type Key struct {
	Shard int
	ID    int64
}

// Mapper 是单张目标表的映射与计数器。不是并发安全的，由调用方串行化。
type Mapper struct {
	next int64
	m    map[Key]int64
}

// New 返回 next 初值为 1 的 Mapper。
func New() *Mapper {
	return &Mapper{next: 1, m: make(map[Key]int64)}
}

// Tid 返回键已分配的 tid；ok 为 false 表示尚未映射。
func (x *Mapper) Tid(k Key) (tid int64, ok bool) {
	tid, ok = x.m[k]
	return tid, ok
}

// Assign 返回键的 tid：已映射则原样返回，否则取 next 分配并加 1。
func (x *Mapper) Assign(k Key) int64 {
	if tid, ok := x.m[k]; ok {
		return tid
	}
	tid := x.next
	x.next++
	x.m[k] = tid
	return tid
}

// Next 返回下一个将分配的 tid（即已分配个数 + 1），供不变量检查。
func (x *Mapper) Next() int64 {
	return x.next
}

// Len 返回已映射的键数。
func (x *Mapper) Len() int {
	return len(x.m)
}

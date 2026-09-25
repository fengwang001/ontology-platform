// Package rtab 维护右表哈希索引：map[Key]Val 的增删查。
// 不依赖其他包；自身不加锁，并发安全由上层 rjoin 保证。
package rtab

import "errors"

// ErrEmptyKey 是 key 为空串时的可判定哨兵错误。
var ErrEmptyKey = errors.New("rtab: empty key")

// Table 是右表哈希索引。
type Table struct {
	m map[string]string
}

// New 返回一张空右表。
func New() *Table {
	return &Table{m: make(map[string]string)}
}

// Upsert 写入 right[key]=val；key 为空则拒绝且不改状态。
func (t *Table) Upsert(key, val string) error {
	if key == "" {
		return ErrEmptyKey
	}
	t.m[key] = val
	return nil
}

// Delete 删除 right[key]；key 为空则拒绝；key 不存在时幂等。
func (t *Table) Delete(key string) error {
	if key == "" {
		return ErrEmptyKey
	}
	delete(t.m, key)
	return nil
}

// Get 查询 right[key]，返回 (Val, 是否存在)。
func (t *Table) Get(key string) (string, bool) {
	v, ok := t.m[key]
	return v, ok
}

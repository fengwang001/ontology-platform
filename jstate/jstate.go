// Package jstate 按连接键索引 L/R 两张表：每个键下的有序 ID 集合、
// n(k)（右表按键计数）以及按 ID 查行。本包不依赖其他任何包。
package jstate

import "sort"

// Side 是变更来源表：左表 L 或右表 R。
type Side string

// 两侧表的标识。
const (
	SideL Side = "L"
	SideR Side = "R"
)

// Tables 是 L/R 两表的按键索引（进程内存、非并发安全，由上层加锁）。
type Tables struct {
	lByID map[string]string // 左表 ID -> Key
	rByID map[string]string // 右表 ID -> Key
	lKey  map[string]map[string]struct{}
	rKey  map[string]map[string]struct{}
	total int
}

// New 返回空表。
func New() *Tables {
	return &Tables{
		lByID: map[string]string{},
		rByID: map[string]string{},
		lKey:  map[string]map[string]struct{}{},
		rKey:  map[string]map[string]struct{}{},
	}
}

func (t *Tables) idMap(side Side) map[string]string {
	if side == SideL {
		return t.lByID
	}
	return t.rByID
}

func (t *Tables) keyMap(side Side) map[string]map[string]struct{} {
	if side == SideL {
		return t.lKey
	}
	return t.rKey
}

// Clone 返回深拷贝，供批量变更「先试算后提交」。
func (t *Tables) Clone() *Tables {
	c := New()
	for id, k := range t.lByID {
		c.lByID[id] = k
	}
	for id, k := range t.rByID {
		c.rByID[id] = k
	}
	for k, ids := range t.lKey {
		m := make(map[string]struct{}, len(ids))
		for id := range ids {
			m[id] = struct{}{}
		}
		c.lKey[k] = m
	}
	for k, ids := range t.rKey {
		m := make(map[string]struct{}, len(ids))
		for id := range ids {
			m[id] = struct{}{}
		}
		c.rKey[k] = m
	}
	c.total = t.total
	return c
}

// Has 报告某侧表中是否存在指定 ID 的行。
func (t *Tables) Has(side Side, id string) bool {
	_, ok := t.idMap(side)[id]
	return ok
}

// KeyOf 按 ID 查行的连接键。
func (t *Tables) KeyOf(side Side, id string) (string, bool) {
	k, ok := t.idMap(side)[id]
	return k, ok
}

// Add 插入一行（调用方须保证 ID 不重复）。
func (t *Tables) Add(side Side, id, key string) {
	t.idMap(side)[id] = key
	km := t.keyMap(side)
	if km[key] == nil {
		km[key] = map[string]struct{}{}
	}
	km[key][id] = struct{}{}
	t.total++
}

// Remove 按 ID 删除一行，返回该行原来的连接键。
func (t *Tables) Remove(side Side, id string) (string, bool) {
	im := t.idMap(side)
	key, ok := im[id]
	if !ok {
		return "", false
	}
	delete(im, id)
	km := t.keyMap(side)[key]
	delete(km, id)
	if len(km) == 0 {
		delete(t.keyMap(side), key)
	}
	t.total--
	return key, true
}

// Count 返回某侧表中连接键为 key 的行数；对右表即 n(k)。
func (t *Tables) Count(side Side, key string) int {
	return len(t.keyMap(side)[key])
}

// SortedIDs 返回某侧表中连接键为 key 的全部 ID，按字典序升序。
func (t *Tables) SortedIDs(side Side, key string) []string {
	ids := make([]string, 0, len(t.keyMap(side)[key]))
	for id := range t.keyMap(side)[key] {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// AllIDs 返回某侧表全部行 ID，按字典序升序。
func (t *Tables) AllIDs(side Side) []string {
	ids := make([]string, 0, len(t.idMap(side)))
	for id := range t.idMap(side) {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// Total 返回 L/R 两表行数合计。
func (t *Tables) Total() int { return t.total }

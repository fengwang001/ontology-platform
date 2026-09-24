// Package api 是对外门面：大小写不敏感的字符串键查找表。
package api

import "ontology/keymap"

// 可判定的哨兵错误（与 keymap 同源，三者互不相同）。
var (
	ErrEmptyKey = keymap.ErrEmptyKey
	ErrKeyLong  = keymap.ErrKeyLong
	ErrFull     = keymap.ErrFull
)

// Table 按折叠键命中记录，保留首次插入的原始键写法。
type Table struct{ m *keymap.Map }

// New 建表；maxKey 为键长上限（rune 数），maxN 为表项数上限。
func New(maxKey, maxN int) *Table { return &Table{m: keymap.New(maxKey, maxN)} }

// Put 写入记录；空键、超长键、表满会被拒绝且不留下任何痕迹。
func (t *Table) Put(key, val string) error { return t.m.Put(key, val) }

// Get 按折叠键命中记录，大小写不同的等价键返回同一条记录。
func (t *Table) Get(key string) (string, bool) { return t.m.Get(key) }

// Keys 按折叠键字典序返回首次插入的原始键。
func (t *Table) Keys() []string { return t.m.Keys() }

// SelfCheck 核验表项与折叠键的一致性与计数。
func (t *Table) SelfCheck() error { return t.m.SelfCheck() }

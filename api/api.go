// Package api 是协同文本序列 CRDT 对外的唯一入口，仅依赖 doc。
package api

import (
	"ontology/doc"
)

// ID 是元素全局标识 (Lamport, Replica)，零值为 ∅。
type ID = doc.ID

// Empty 是特殊前驱 ∅。
var Empty = doc.Empty

// 三类可判定哨兵错误（彼此互不相同），另含删除相关错误。
var (
	ErrInvalidID      = doc.ErrInvalidID
	ErrDuplicateID    = doc.ErrDuplicateID
	ErrPrevNotFound   = doc.ErrPrevNotFound
	ErrIDNotFound     = doc.ErrIDNotFound
	ErrAlreadyDeleted = doc.ErrAlreadyDeleted
)

// Doc 是一份对外的协同文本。
type Doc struct{ d *doc.Doc }

// New 创建空文档。
func New() *Doc { return &Doc{d: doc.New()} }

// Insert 把字符 ch（标识 id）插到 prev 之后；∅ 表示最前。
func (x *Doc) Insert(prev, id ID, ch rune) error { return x.d.Insert(prev, id, ch) }

// Delete 给 id 打墓碑，不物理移除。
func (x *Doc) Delete(id ID) error { return x.d.Delete(id) }

// Text 返回当前可见文本，可并发只读。
func (x *Doc) Text() string { return x.d.Text() }

// SelfCheck 对四条不变量与复杂度做内置核验，通过返回 nil，可并发调用。
func (x *Doc) SelfCheck() error { return x.d.SelfCheck() }

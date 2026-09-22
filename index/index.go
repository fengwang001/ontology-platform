// Package index 是文档 Add/Update/Delete 的索引器，维护三层一致性。
package index

import (
	"errors"

	"ontology/dict"
	"ontology/postings"
)

// MaxPositionsPerTerm 是单文档单 term 的位置数量上限。
const MaxPositionsPerTerm = 1024

var (
	// ErrDuplicateDoc 重复 Add 同一 docID。
	ErrDuplicateDoc = errors.New("index: duplicate docID")
	// ErrDocNotFound Update/Delete 不存在的 docID。
	ErrDocNotFound = errors.New("index: docID not found")
	// ErrTooManyPositions 单文档单 term 位置数超上限。
	ErrTooManyPositions = errors.New("index: too many positions for one term")
	// ErrEmptyDocument 空文档（零长度正文）。
	ErrEmptyDocument = errors.New("index: empty document")
)

// Index 是内存倒排索引。
type Index struct {
	d dict.Dict
	// docs 为活跃文档视图：docID → term → 位置列表。
	docs map[uint64]map[string][]int
}

// View 是读锁内的只读快照视图，仅在 ReadView 闭包执行期间有效。
type View struct {
	i *Index
}

// New 创建空索引。
func New() *Index {
	return &Index{d: *dict.New(), docs: make(map[uint64]map[string][]int)}
}

// Add 新增文档。
func (i *Index) Add(docID uint64, text string) error { return nil }

// Update 按差量更新文档，整体原子生效。
func (i *Index) Update(docID uint64, text string) error { return nil }

// Delete 删除文档及其全部倒排信息。
func (i *Index) Delete(docID uint64) error { return nil }

// ReadView 在一致读快照上执行 fn。
func (i *Index) ReadView(fn func(*View) error) error { return nil }

// Postings 精确取 term 的倒排表。
func (v *View) Postings(term string) (*postings.List, bool) { return nil, false }

// PrefixTerms 前缀查找 term。
func (v *View) PrefixTerms(prefix string) []string { return nil }

// AllDocIDs 返回全部活跃 docID（升序）。
func (v *View) AllDocIDs() []uint64 { return nil }

// SelfCheck 一次性核验不变量 1、2、3。
func (i *Index) SelfCheck() error { return nil }

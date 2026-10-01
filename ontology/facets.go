package ontology

import "sync"

// ErrInvalidArgument 表示参数非法（空 docID、空维度名、非法路径、数量超限等）。
var ErrInvalidArgument = invalidArgumentError{}

// ErrDuplicateDocument 表示 Add 时 docID 已存在。
var ErrDuplicateDocument = duplicateDocumentError{}

// ErrDocumentNotFound 表示 Replace 或 Delete 时 docID 不存在。
var ErrDocumentNotFound = documentNotFoundError{}

// ErrDuplicateLink 表示 child 已经声明过 parent。
var ErrDuplicateLink = duplicateLinkError{}

// ErrCyclicDependency 表示 Link 形成自环或循环依赖。
var ErrCyclicDependency = cyclicDependencyError{}

type invalidArgumentError struct{}

func (invalidArgumentError) Error() string { return "invalid argument" }

type duplicateDocumentError struct{}

func (duplicateDocumentError) Error() string { return "duplicate document" }

type documentNotFoundError struct{}

func (documentNotFoundError) Error() string { return "document not found" }

type duplicateLinkError struct{}

func (duplicateLinkError) Error() string { return "duplicate dependency declaration" }

type cyclicDependencyError struct{}

func (cyclicDependencyError) Error() string { return "cyclic dependency" }

// FacetItem 是单个维度下的一个候选值。
type FacetItem struct {
	Value    string
	Count    int
	Selected bool
}

// FacetResult 是单个生效维度的计数结果。
type FacetResult struct {
	Dimension string
	Items     []FacetItem
}

// FacetsResult 是一次 Facets 调用的完整结果。
type FacetsResult struct {
	Total int
	// Facets 按维度名字节序升序排列。
	Facets []FacetResult
}

// document 保存一篇文档在各维度上的层级节点集合。
type document struct {
	// nodes 为“维度名 -> 节点集合”。
	nodes map[string]map[string]struct{}
}

// Store 是并发安全的文档分面计数器。
// 所有状态只能在 mu（或读锁）保护下访问。
type Store struct {
	mu     sync.RWMutex
	docs   map[string]*document
	parent map[string]string
}

// New 创建一个空的 Store。
func New() *Store { return &Store{} }

// Package snippets 提供带文档登记的搜索结果片段选取器。
package snippets

import (
	"errors"
	"sync"
	"unicode/utf8"
)

// MaxTextLen 是 Register 允许的最大文本字节数。
const MaxTextLen = 1 << 20

// MaxHits 是 Snippets 单次调用允许的命中数上限（去重前）。
const MaxHits = 10000

var (
	// ErrInvalidArgument 表示参数非法（text 非法/超长、W/K 越界、hits 超限或命中非法）。
	ErrInvalidArgument = errors.New("snippets: invalid argument")
	// ErrDuplicateDocument 表示 Register 的 docID 已登记。
	ErrDuplicateDocument = errors.New("snippets: document already exists")
	// ErrDocumentNotFound 表示 Unregister/Snippets 的 docID 未登记。
	ErrDocumentNotFound = errors.New("snippets: document not found")
)

// Hit 是一个字节半开区间 [Start, End)。
type Hit struct {
	Start int
	End   int
}

// Range 是片段内一个高亮字节半开区间。
type Range struct {
	Start int
	End   int
}

// Snippet 是被选中的一个片段。
type Snippet struct {
	Start      int
	End        int
	Highlights []Range
	Score      int
}

// Registry 是并发安全的文档登记表。
//
// 零值即可使用；登记、删除与片段选取可并发调用，
// 其执行结果等价于某种串行顺序。
type Registry struct {
	mu sync.RWMutex
	// docs 保存每份登记文本的独立副本；不向外暴露底层切片。
	docs map[string]document
}

type document struct {
	text []byte
	// boundary[i] 为 true 表示字节偏移 i 是某个 UTF-8 字符的起始边界，
	// 长度为 len(text)+1，len(text) 恒为合法边界。
	boundary []bool
}

// NewRegistry 创建一个空的登记表。
func NewRegistry() *Registry {
	return &Registry{docs: make(map[string]document)}
}

// Register 登记一份文档。
//
// 拒绝顺序（被拒绝不改变登记表）：docID 为空串或 text 非法/超长 → ErrInvalidArgument；
// docID 已存在 → ErrDuplicateDocument。
// 保存的是 text 的独立副本，调用方之后修改 text 不影响登记表。
func (r *Registry) Register(docID string, text []byte) error {
	if docID == "" || !utf8.Valid(text) || len(text) > MaxTextLen {
		return ErrInvalidArgument
	}
	copied := make([]byte, len(text))
	copy(copied, text)

	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.docs[docID]; ok {
		return ErrDuplicateDocument
	}
	r.docs[docID] = document{
		text:     copied,
		boundary: characterBoundaries(copied),
	}
	return nil
}

// Unregister 删除一份已登记文档。
// docID 不存在时返回 ErrDocumentNotFound。
func (r *Registry) Unregister(docID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.docs[docID]; !ok {
		return ErrDocumentNotFound
	}
	delete(r.docs, docID)
	return nil
}

// characterBoundaries 返回长度 n+1 的边界标记。
func characterBoundaries(text []byte) []bool {
	n := len(text)
	boundary := make([]bool, n+1)
	boundary[n] = true
	for i := 0; i < n; {
		boundary[i] = true
		_, size := utf8.DecodeRune(text[i:])
		i += size
	}
	return boundary
}

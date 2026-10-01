// Package cache 实现编译结果的直接模式清单缓存。
//
// 缓存以「动作键」组织条目，同一动作键下可保存多份
// 「实际读取文件摘要清单 -> 结果摘要」的条目。Lookup 命中
// 只取决于条目清单中记录过的文件当前的内容摘要，与清单外
// 的文件无关。
package cache

import (
	"errors"
	"sync"
)

// 可区分的拒绝原因。Put 按以下顺序只报告第一个命中的原因：
// 键为空、清单为空、空路径、路径未严格递增、空摘要、结果为空。
var (
	ErrInvalidM        = errors.New("cache: 每键条目上限 M 必须 >= 1")
	ErrInvalidCap      = errors.New("cache: 全局条目上限 Cap 必须 >= 1")
	ErrEmptyKey        = errors.New("cache: 动作键不能为空")
	ErrEmptyManifest   = errors.New("cache: 读取清单不能为空")
	ErrEmptyPath       = errors.New("cache: 读取清单含空路径")
	ErrPathsNotOrdered = errors.New("cache: 读取清单路径未按字节序严格递增")
	ErrEmptyDigest     = errors.New("cache: 读取清单含空内容摘要")
	ErrEmptyResult     = errors.New("cache: 结果摘要不能为空")
)

// Pair 是读取清单中的一项：路径与其内容摘要。
type Pair struct {
	Path   string
	Digest string
}

// Entry 是 Dump 返回的条目视图，按 Last 降序排列。
type Entry struct {
	Manifest []Pair
	Result   string
	Last     uint64
}

// Cache 是并发安全的直接模式清单缓存。
type Cache struct {
	mu       sync.Mutex
	m        int
	capLimit int
	tick     uint64
	total    int
	keys     map[string]*keyGroup
	gc       entryHeap
	examined int // 非导出计数器：最近一次 Lookup 考察的条目数
}

// New 构造缓存。maxPerKey 为每键条目上限 M，maxTotal 为全局条目
// 上限 Cap；M 先于 Cap 校验。
func New(maxPerKey, maxTotal int) (*Cache, error) {
	if maxPerKey < 1 {
		return nil, ErrInvalidM
	}
	if maxTotal < 1 {
		return nil, ErrInvalidCap
	}
	return &Cache{
		m:        maxPerKey,
		capLimit: maxTotal,
		keys:     make(map[string]*keyGroup),
	}, nil
}

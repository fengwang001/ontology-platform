// Package idx 按规范等价键（NFC）索引原始字符串，支持并发访问。
package idx

import (
	"errors"
	"sync"

	"ontology/norm"
)

var (
	ErrBadMaxKeys  = errors.New("idx: maxKeys must be positive")
	ErrTooManyKeys = errors.New("idx: distinct key limit exceeded")
)

// Index 维护 规范键 → 该键下所有原始串 的映射。
type Index struct {
	mu      sync.RWMutex
	max     int
	m       map[string][]string
	checked int // 最近一次 Get 检查过的已存原始串个数（非导出，证明哈希定位）
}

// New 创建最多容纳 maxKeys 个不同规范键的索引。maxKeys 非正整体失败。
func New(maxKeys int) (*Index, error) {
	if maxKeys <= 0 {
		return nil, ErrBadMaxKeys
	}
	return &Index{max: maxKeys, m: make(map[string][]string)}, nil
}

// Put 归一化 s 后按规范键归档。重复串去重；任何失败都不改变状态。
func (i *Index) Put(s string) error {
	k, err := norm.Key(s) // 先校验（非法 UTF-8 / 不支持码点），再动状态
	if err != nil {
		return err
	}
	i.mu.Lock()
	defer i.mu.Unlock()
	b, ok := i.m[k]
	if !ok {
		if len(i.m) >= i.max {
			return ErrTooManyKeys // 未写入，状态不变
		}
	} else {
		for _, e := range b {
			if e == s {
				return nil // 已存在，幂等
			}
		}
	}
	i.m[k] = append(b, s)
	return nil
}

// Get 返回所有与 s 规范等价的已存原始串（无重复，可能为空）。
func (i *Index) Get(s string) ([]string, error) {
	k, err := norm.Key(s)
	if err != nil {
		return nil, err
	}
	i.mu.Lock() // 写 checked 计数器，需写锁
	defer i.mu.Unlock()
	b := i.m[k] // 哈希定位：不扫描其它键下的串
	i.checked = len(b)
	out := make([]string, len(b))
	copy(out, b)
	return out, nil
}

// Distinct 返回当前不同规范键的个数。
func (i *Index) Distinct() int {
	i.mu.RLock()
	defer i.mu.RUnlock()
	return len(i.m)
}

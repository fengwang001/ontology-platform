// Package schema 管理同名直方图的边界版本链，并定义直方图值与采集器。
package schema

import (
	"errors"
	"sync"
)

// 错误哨兵，供 schema、merge、quantile 三包共用，便于调用方用 errors.Is 判定。
var (
	ErrInvalid      = errors.New("invalid argument") // 参数非法
	ErrNoChange     = errors.New("no change")        // 与最新版本相等
	ErrIncompatible = errors.New("incompatible")     // 既非真子集也非真超集 / 无公共边界
	ErrNotFound     = errors.New("no such schema")   // 名字或版本不存在
	ErrNameMismatch = errors.New("name mismatch")    // 合并两侧名字不同
	ErrOverflow     = errors.New("overflow")         // 计数或 Sum 超出 int64
	ErrEmpty        = errors.New("empty histogram")  // 总计数为 0
)

const (
	maxNameBytes = 64
	maxBounds    = 64
	maxBound     = int64(1_000_000_000_000) // 10^12
)

// Registry 是并发安全的直方图边界注册表。
type Registry struct {
	mu       sync.RWMutex
	versions map[string][][]int64 // versions[name][version-1] = 边界副本
}

// NewRegistry 创建空注册表。
func NewRegistry() *Registry {
	return &Registry{versions: make(map[string][][]int64)}
}

// validBounds 校验边界：1 到 64 个、严格递增、取值 1 到 10^12。
func validBounds(bounds []int64) bool {
	if len(bounds) < 1 || len(bounds) > maxBounds {
		return false
	}
	prev := int64(0)
	for _, b := range bounds {
		if b < 1 || b > maxBound || b <= prev {
			return false
		}
		prev = b
	}
	return true
}

func equalBounds(a, b []int64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// isSubset 报告 a 是否为 b 的子集（两者均严格递增）。
func isSubset(a, b []int64) bool {
	j := 0
	for _, x := range a {
		for j < len(b) && b[j] < x {
			j++
		}
		if j == len(b) || b[j] != x {
			return false
		}
	}
	return true
}

// Register 登记 name 的新边界版本，返回新版本号（首次为 1）。
// 拒绝顺序：参数非法、无变化、不兼容。
func (r *Registry) Register(name string, bounds []int64) (int, error) {
	if name == "" || len(name) > maxNameBytes || !validBounds(bounds) {
		return 0, ErrInvalid
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	chain := r.versions[name]
	if len(chain) > 0 {
		latest := chain[len(chain)-1]
		switch {
		case equalBounds(latest, bounds):
			return 0, ErrNoChange
		case len(bounds) < len(latest) && isSubset(bounds, latest),
			len(bounds) > len(latest) && isSubset(latest, bounds):
			// 真子集或真超集，接受
		default:
			return 0, ErrIncompatible
		}
	}
	cp := append([]int64(nil), bounds...)
	r.versions[name] = append(chain, cp)
	return len(chain) + 1, nil
}

// Bounds 返回 name 的 version 版本边界副本。
func (r *Registry) Bounds(name string, version int) ([]int64, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	chain := r.versions[name]
	if version < 1 || version > len(chain) {
		return nil, ErrNotFound
	}
	return append([]int64(nil), chain[version-1]...), nil
}

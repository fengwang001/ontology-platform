package schema

import (
	"errors"
	"sync"
)

// 哨兵错误：全项目共用的错误类别。
var (
	ErrInvalidArgument = errors.New("invalid argument")
	ErrNotFound        = errors.New("no such schema")
	ErrUnchanged       = errors.New("bounds unchanged")
	ErrIncompatible    = errors.New("incompatible bounds")
	ErrOverflow        = errors.New("arithmetic overflow")
	ErrNameMismatch    = errors.New("histogram name mismatch")
	ErrEmpty           = errors.New("empty histogram")
)

// Registry 保存具名直方图的边界版本链，并发安全。
type Registry struct {
	mu      sync.RWMutex
	schemas map[string][][]int64
}

// NewRegistry 创建空登记表。
func NewRegistry() *Registry {
	return &Registry{schemas: make(map[string][][]int64)}
}

const (
	maxNameLen  = 64
	maxBounds   = 64
	maxBoundVal = int64(1_000_000_000_000)
)

// validBounds 校验 1..64 个严格递增、取值 1..1e12 的整数边界。
func validBounds(bounds []int64) bool {
	if len(bounds) < 1 || len(bounds) > maxBounds {
		return false
	}
	prev := int64(0)
	for _, b := range bounds {
		if b < 1 || b > maxBoundVal || b <= prev {
			return false
		}
		prev = b
	}
	return true
}

// subset 报告 x 是否为 y 的真子集；x、y 均为严格递增切片。
func properSubset(x, y []int64) bool {
	if len(x) >= len(y) {
		return false
	}
	ix, found := 0, 0
	for _, v := range x {
		for ix < len(y) && y[ix] < v {
			ix++
		}
		if ix == len(y) || y[ix] != v {
			return false
		}
		found++
		ix++
	}
	return found == len(x)
}

// Register 登记新边界版本，返回版本号。
func (r *Registry) Register(name string, bounds []int64) (int, error) {
	if len(name) == 0 || len(name) > maxNameLen || !validBounds(bounds) {
		return 0, ErrInvalidArgument
	}
	cp := append([]int64(nil), bounds...)
	r.mu.Lock()
	defer r.mu.Unlock()
	chain := r.schemas[name]
	if len(chain) > 0 {
		latest := chain[len(chain)-1]
		same := len(cp) == len(latest)
		if same {
			for i := range cp {
				if cp[i] != latest[i] {
					same = false
					break
				}
			}
		}
		if same {
			return 0, ErrUnchanged
		}
		if !properSubset(cp, latest) && !properSubset(latest, cp) {
			return 0, ErrIncompatible
		}
	}
	r.schemas[name] = append(chain, cp)
	return len(r.schemas[name]), nil
}

// Bounds 返回指定版本边界的独立副本。
func (r *Registry) Bounds(name string, version int) ([]int64, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	chain := r.schemas[name]
	if version < 1 || version > len(chain) {
		return nil, ErrNotFound
	}
	return append([]int64(nil), chain[version-1]...), nil
}

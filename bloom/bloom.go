package bloom

import (
	"errors"
	"fmt"
	"math"
	"sync"
)

// 可判定错误：调用方用 errors.Is 匹配。
var (
	// ErrInvalidParam 表示 n 或 p 非法（n==0、p<=0、p>=1）。
	ErrInvalidParam = errors.New("bloom: invalid parameter")
	// ErrMismatch 表示 Merge 两侧参数 (m, k) 不一致。
	ErrMismatch = errors.New("bloom: parameter mismatch")
)

// Filter 是并发安全的布隆过滤器。
type Filter struct {
	m    uint64 // 位数组长度（位）
	k    uint64 // 哈希个数
	bits []uint64
	mu   sync.RWMutex
}

// New 按目标元素数 n 与目标假阳性率 p 构造过滤器。
// n==0、p<=0 或 p>=1 时返回包装了 ErrInvalidParam 的错误。
func New(n uint64, p float64) (*Filter, error) {
	if n == 0 {
		return nil, fmt.Errorf("%w: n must be > 0, got %d", ErrInvalidParam, n)
	}
	if !(p > 0 && p < 1) {
		return nil, fmt.Errorf("%w: p must be in (0,1), got %v", ErrInvalidParam, p)
	}
	m := uint64(math.Ceil(-float64(n) * math.Log(p) / (math.Ln2 * math.Ln2)))
	k := uint64(math.Round(float64(m) / float64(n) * math.Ln2))
	if k < 1 {
		k = 1
	}
	return &Filter{m: m, k: k, bits: make([]uint64, (m+63)/64)}, nil
}

// M 返回位数组长度（位）。
func (f *Filter) M() uint64 { return f.m }

// K 返回哈希个数。
func (f *Filter) K() uint64 { return f.k }

// Package sample 提供按追踪 ID 的一致性采样与 error 强制保留。
package sample

import (
	"errors"
	"hash/fnv"
	"sync/atomic"

	"ontology/record"
)

// ErrBadRate 表示采样率超出 [0,1]。
var ErrBadRate = errors.New("sample: rate must be within [0,1]")

const (
	denom    = 10000
	noTrace  = "\x00no-trace"
	hashOnce = 1
)

// Sampler 是不可变的一致性采样器。
type Sampler struct {
	threshold uint64

	// HashCount 记录哈希计算次数，用于证明 O(1)：每次决策恰好 1 次。
	hashCount atomic.Uint64
}

// HashCount 返回累计哈希次数。
func (s *Sampler) HashCount() uint64 { return s.hashCount.Load() }

// New 创建采样器；rate 0=全丢、1=全留。
func New(rate float64) (*Sampler, error) {
	if rate < 0 || rate > 1 {
		return nil, ErrBadRate
	}
	return &Sampler{threshold: uint64(rate * denom)}, nil
}

// KeepTrace 返回某追踪链的纯哈希决策（不含 error 规则）。
func (s *Sampler) KeepTrace(traceID string) bool {
	s.hashCount.Add(hashOnce)
	if traceID == "" {
		traceID = noTrace
	}
	h := fnv.New64a()
	h.Write([]byte(traceID))
	return h.Sum64()%denom < s.threshold
}

// Decide 对一条记录给出是否保留，并在需要时设置链不完整标记。
func (s *Sampler) Decide(r *record.Record) bool {
	keepChain := s.KeepTrace(r.TraceID)
	force := r.Level >= record.Error
	if keepChain {
		r.Incomplete = false
		return true
	}
	if force {
		r.Incomplete = true
		return true
	}
	r.Incomplete = false
	return false
}

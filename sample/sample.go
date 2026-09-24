// Package sample 提供按追踪 ID 的确定性一致采样。
package sample

import (
	"hash/fnv"
	"sync/atomic"

	"ontology/record"
)

const modulus = 1_000_000

// Sampler 仅由采样率决定；不可变、可并发使用。
type Sampler struct {
	threshold uint64 // rate*1e6
	hashes    atomic.Int64
}

// New 构造采样器，rate 截断到 [0,1]。
func New(rate float64) *Sampler {
	if rate < 0 {
		rate = 0
	}
	if rate > 1 {
		rate = 1
	}
	s := &Sampler{threshold: uint64(rate * modulus)}
	if rate >= 1 {
		s.threshold = modulus
	}
	return s
}

// HashCount 返回决策过程中哈希计算的总次数（每次决策恰为 1）。
func (s *Sampler) HashCount() int64 { return s.hashes.Load() }

// Decide 判定记录是否保留。同 traceID 永远同结论；error 及以上无条件保留，
// 且当其链本该被丢弃时把 Incomplete 置为 true。空 traceID 用记录内容指纹
// 做逐条确定性判定（无链可一致，策略见 DESIGN.md）。
func (s *Sampler) Decide(r *record.Record) bool {
	r.Incomplete = false
	chainKeep := s.kept(r)
	if chainKeep {
		return true
	}
	if r.Level >= record.LevelError {
		r.Incomplete = true
		return true
	}
	return false
}

func (s *Sampler) kept(r *record.Record) bool {
	if s.threshold == 0 {
		return false
	}
	if s.threshold >= modulus {
		return true
	}
	var key []byte
	if r.TraceID != "" {
		key = []byte(r.TraceID)
	} else {
		b, _ := r.Encode()
		key = b
	}
	s.hashes.Add(1)
	h := fnv.New64a()
	_, _ = h.Write(key)
	return h.Sum64()%modulus < s.threshold
}

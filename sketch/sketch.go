// Package sketch 提供固定内存的频率估计：小计数器数组 + 周期性减半老化。
package sketch

import (
	"encoding/binary"
	"hash/fnv"
)

// Sketch 是固定大小的频率估计器，内存占用与键的数量无关。
// 并发安全由调用方（store 的互斥锁）保证。
type Sketch struct {
	counters []uint32
}

// New 创建一个含 n 个计数器的 Sketch。
func New(n int) *Sketch {
	if n < 1 {
		n = 1
	}
	return &Sketch{counters: make([]uint32, n)}
}

func (s *Sketch) index(key string) uint64 {
	h := fnv.New64a()
	var buf [8]byte
	binary.LittleEndian.PutUint64(buf[:], uint64(len(key)))
	h.Write(buf[:])
	h.Write([]byte(key))
	return h.Sum64() % uint64(len(s.counters))
}

// Increment 把键的估计频率加一（饱和，不回绕）。
func (s *Sketch) Increment(key string) {
	i := s.index(key)
	if s.counters[i] < ^uint32(0) {
		s.counters[i]++
	}
}

// Estimate 返回键的估计频率（含哈希碰撞带来的上偏）。
func (s *Sketch) Estimate(key string) int64 {
	return int64(s.counters[s.index(key)])
}

// Halve 把所有计数器减半（一个老化周期）。
func (s *Sketch) Halve() {
	for i := range s.counters {
		s.counters[i] >>= 1
	}
}

// Bytes 返回结构的固定内存占用字节数，与观测过的键数无关。
func (s *Sketch) Bytes() int {
	return len(s.counters) * 4
}

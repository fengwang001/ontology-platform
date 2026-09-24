// Package hll 实现计数型 HyperLogLog 草图：每个寄存器维护「秩→计数」多集，
// 支持增量加入与撤回，寄存器秩取现存计数 > 0 的最大秩。
package hll

import (
	"errors"
	"math"
)

var (
	// ErrInvalidP：精度 p < 1。
	ErrInvalidP = errors.New("hll: p must be >= 1")
	// ErrNotPresent：Remove 的键对应桶已空。
	ErrNotPresent = errors.New("hll: key not present")
)

// Sketch 是计数型 HLL 草图，m = 2^p 个寄存器。
type Sketch struct {
	m   int
	reg []map[int]int // 每寄存器：秩 -> 计数
}

// New 创建 m = 2^p 寄存器的草图；p < 1 报 ErrInvalidP。
func New(p int) (*Sketch, error) {
	if p < 1 {
		return nil, ErrInvalidP
	}
	m := 1 << uint(p)
	return &Sketch{m: m, reg: make([]map[int]int, m)}, nil
}

// slot 计算确定性哈希：x = 字节和，j = x mod m，w = (x/m) mod 4 + 1。
func (s *Sketch) slot(key string) (j, w int) {
	x := 0
	for i := 0; i < len(key); i++ {
		x += int(key[i])
	}
	return x % s.m, (x/s.m)%4 + 1
}

// Add 把 key 的 (j,w) 桶计数 +1。
func (s *Sketch) Add(key string) {
	j, w := s.slot(key)
	b := s.reg[j]
	if b == nil {
		b = make(map[int]int)
		s.reg[j] = b
	}
	b[w]++
}

// Remove 把 key 的 (j,w) 桶计数 -1（归零删桶）；桶已空报 ErrNotPresent。
func (s *Sketch) Remove(key string) error {
	j, w := s.slot(key)
	b := s.reg[j]
	if b[w] == 0 {
		return ErrNotPresent
	}
	b[w]--
	if b[w] == 0 {
		delete(b, w)
	}
	return nil
}

// Ranks 返回每寄存器秩：桶内计数 > 0 的最大秩，空寄存器为 0。
func (s *Sketch) Ranks() []int {
	out := make([]int, s.m)
	for j, b := range s.reg {
		r := 0
		for w, c := range b {
			if c > 0 && w > r {
				r = w
			}
		}
		out[j] = r
	}
	return out
}

// Card 用原始 HLL 公式 m² / Σ_j 2^(−rank[j]) 估计基数。
func (s *Sketch) Card() float64 {
	sum := 0.0
	for _, r := range s.Ranks() {
		sum += math.Ldexp(1, -r)
	}
	return float64(s.m) * float64(s.m) / sum
}

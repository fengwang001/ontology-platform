// Package hll 实现计数型 HyperLogLog 草图：支持增量加入与撤回。
package hll

import "errors"

var (
	// ErrBadP 表示精度 p 非法（p < 1）。
	ErrBadP = errors.New("hll: p must be >= 1")
	// ErrEmptyKey 表示键为空串。
	ErrEmptyKey = errors.New("hll: empty key")
	// ErrNotFound 表示撤回的键不存在（对应桶已空）。
	ErrNotFound = errors.New("hll: key not found")
)

// Sketch 是计数型 HLL：每个寄存器维护一张「秩 → 计数」多集。
type Sketch struct {
	m      int
	bucket []map[int]int // bucket[j][w] = 寄存器 j 内秩为 w 的键数
}

// New 创建 m = 2^p 个寄存器的草图。
func New(p int) (*Sketch, error) {
	if p < 1 {
		return nil, ErrBadP
	}
	m := 1 << uint(p)
	b := make([]map[int]int, m)
	for i := range b {
		b[i] = make(map[int]int)
	}
	return &Sketch{m: m, bucket: b}, nil
}

// locate 用确定性哈希把键映射到 (寄存器, 秩)。
func (s *Sketch) locate(key string) (j, w int) {
	x := 0
	for i := 0; i < len(key); i++ {
		x += int(key[i])
	}
	return x % s.m, x/s.m%4 + 1
}

// Add 把键的 (j,w) 桶计数 +1。
func (s *Sketch) Add(key string) error {
	if key == "" {
		return ErrEmptyKey
	}
	j, w := s.locate(key)
	s.bucket[j][w]++
	return nil
}

// Remove 把键的 (j,w) 桶计数 -1（归零则删）；桶已空报 ErrNotFound，且不改任何状态。
func (s *Sketch) Remove(key string) error {
	if key == "" {
		return ErrEmptyKey
	}
	j, w := s.locate(key)
	if s.bucket[j][w] == 0 {
		return ErrNotFound
	}
	s.bucket[j][w]--
	if s.bucket[j][w] == 0 {
		delete(s.bucket[j], w)
	}
	return nil
}

// Ranks 返回每个寄存器当前计数 > 0 的最大秩（空寄存器为 0）。
func (s *Sketch) Ranks() []int {
	r := make([]int, s.m)
	for j := range s.bucket {
		for w := range s.bucket[j] {
			if w > r[j] {
				r[j] = w
			}
		}
	}
	return r
}

// Card 用原始 HLL 公式 m² / Σ_j 2^(−rank[j]) 估计基数。
func (s *Sketch) Card() float64 {
	sum := 0.0
	for _, r := range s.Ranks() {
		sum += 1.0 / float64(int(1)<<uint(r))
	}
	return float64(s.m) * float64(s.m) / sum
}

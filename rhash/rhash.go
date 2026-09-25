// Package rhash 计算多项式滚动哈希的前缀哈希表与底幂表，
// 并以 O(1) 提取任意子串的双哈希。不依赖其他包。
package rhash

import (
	"errors"
	"fmt"
)

// 固定参数：底 B 与两个大素数模（B 与两者互素）。
const (
	B  = 911382323
	M1 = 1000000007
	M2 = 1000000009
)

// Hasher 持有前缀哈希表与底幂表；构造后只读，可并发查询。
type Hasher struct {
	s         []byte   // 输入的私有副本，供自检重算
	p1, p2    []uint64 // 前缀哈希：P[0]=0，P[i]=(P[i-1]*B+c[i-1]) mod M
	pw1, pw2  []uint64 // 底幂表：pw[i]=B^i mod M
	rescanned int64    // 非导出计数器：提取子串哈希时重扫的字符数；O(1) 路径恒为 0
}

// New 用 O(n) 预处理 s。复制输入，之后外部修改不影响。
func New(s []byte) *Hasher {
	n := len(s)
	h := &Hasher{
		s:   append([]byte(nil), s...),
		p1:  make([]uint64, n+1),
		p2:  make([]uint64, n+1),
		pw1: make([]uint64, n+1),
		pw2: make([]uint64, n+1),
	}
	h.pw1[0], h.pw2[0] = 1, 1
	for i := 1; i <= n; i++ {
		c := uint64(s[i-1])
		h.p1[i] = (h.p1[i-1]*B + c) % M1
		h.p2[i] = (h.p2[i-1]*B + c) % M2
		h.pw1[i] = h.pw1[i-1] * B % M1
		h.pw2[i] = h.pw2[i-1] * B % M2
	}
	return h
}

// Len 返回字符串长度。
func (h *Hasher) Len() int { return len(h.s) }

// Hash 返回 s[l..r) 的双哈希：(P[r]-P[l]*B^(r-l)) mod M，调整到 [0,M)。
// O(1)，不重新扫描字符。调用方保证 0 <= l <= r <= Len()。
func (h *Hasher) Hash(l, r int) (uint64, uint64) {
	return sub(h.p1[r], h.p1[l], h.pw1[r-l], M1),
		sub(h.p2[r], h.p2[l], h.pw2[r-l], M2)
}

// sub 计算 (pr - pl*pow) mod m 并调整到 [0, m)。
func sub(pr, pl, pow, m uint64) uint64 {
	return (pr + m - pl*pow%m) % m
}

// SelfCheck 核验：内置区间的 Hash 与逐字节重算一致；计数器存活
// （重扫必增长）且 O(1) 的 Hash 路径上重扫字符数恒为 0。
func (h *Hasher) SelfCheck() error {
	n := len(h.s)
	for _, iv := range [][2]int{{0, n}, {0, 0}, {n, n}, {0, n / 2}, {n / 2, n}} {
		a1, a2 := h.Hash(iv[0], iv[1])
		b1, b2 := naiveHash(h.s, iv[0], iv[1])
		if a1 != b1 || a2 != b2 {
			return fmt.Errorf("rhash: 区间 [%d,%d) 哈希与重算不一致", iv[0], iv[1])
		}
	}
	if h.rescanned != 0 {
		return errors.New("rhash: O(1) 提取路径出现重扫")
	}
	probe := New(h.s) // 独立副本：证明计数器确实在计数，而非恒零的死字段
	if probe.rescanned != 0 {
		return errors.New("rhash: 新计数器初值非 0")
	}
	probe.hashByRescan(0, n)
	if probe.rescanned != int64(n) {
		return errors.New("rhash: 重扫计数未随扫描字符数增长")
	}
	return nil
}

// naiveHash 逐字节重算 s[l..r) 的双哈希（自检用，不计入 rescanned）。
func naiveHash(s []byte, l, r int) (uint64, uint64) {
	var a, b uint64
	for i := l; i < r; i++ {
		c := uint64(s[i])
		a = (a*B + c) % M1
		b = (b*B + c) % M2
	}
	return a, b
}

// hashByRescan 逐字节重算并把扫描字符数计入 rescanned；仅测试用，
// 证明计数器是活的（O(1) 的 Hash 路径从不触碰它）。
func (h *Hasher) hashByRescan(l, r int) (uint64, uint64) {
	h.rescanned += int64(r - l)
	return naiveHash(h.s, l, r)
}

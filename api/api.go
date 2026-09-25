// Package api 是对外门面：序号空间构造、四态比较、单调展开与自检。依赖 unwrap（进而依赖 sar）。
package api

import (
	"fmt"

	"ontology/sar"
	"ontology/unwrap"
)

// Rel 是 sar.Rel 的别名；四类可判定哨兵错误见 unwrap 包（ErrWidth/ErrOutOfRange/ErrIncomparable/ErrMovedBackward）。
type Rel = sar.Rel

const Equal, Less, Greater, Incomparable = sar.Equal, sar.Less, sar.Greater, sar.Incomparable

// Space 是一个位宽 N 的序号空间。Cmp/Last/Width/SelfCheck 可并发；Feed 改状态，须外部串行化。
type Space struct {
	n   int
	mod uint64
	u   *unwrap.Unwrapper
}

// New 构造位宽 n 的序号空间；n 非法（n < 1 或 n > 63）时整体失败返回 unwrap.ErrWidth。
func New(n int) (*Space, error) {
	u, err := unwrap.New(n)
	if err != nil {
		return nil, err
	}
	return &Space{n: n, mod: sar.Mod(n), u: u}, nil
}

// Cmp 返回 a、b 在本空间模 M 环上的四态相对关系。纯函数，可并发。
func (s *Space) Cmp(a, b uint64) Rel { return sar.Cmp(a, b, s.mod) }

// Feed 展开一个序号；被拒时 last 不变，返回可判定哨兵错误。
func (s *Space) Feed(v uint64) (int64, error) { return s.u.Feed(v) }

// Last 返回当前绝对序号（单调不减）；可并发。
func (s *Space) Last() int64 { return s.u.Last() }

// Width 返回位宽 N；可并发。
func (s *Space) Width() int { return s.n }

// SelfCheck 用独立的朴素参照核验四条不变量，全部通过返回 nil。可并发。
func (s *Space) SelfCheck() error {
	if err := checkAntisymmetry(s.n); err != nil {
		return err
	}
	for _, st := range selfCheckStreams(s.mod) {
		if err := s.checkAgainstNaive(st); err != nil {
			return err
		}
	}
	return nil
}

// selfCheckStreams 返回内置核验流：前进+幂等、半圈、倒退、多次回绕、边界回绕。
// 流长与绝对序号都有界（int64 不溢出）；取值由调用处掩到 [0, M)。
func selfCheckStreams(mod uint64) [][]uint64 {
	half := mod / 2
	streams := [][]uint64{{1, 2, 3, 3, 4}, {3, 3 + half}, {5, 5 + half + 1}}
	if mod <= 4096 { // 顺序两圈，强制多次回绕
		wrap := make([]uint64, 0, 2*mod)
		for i := uint64(0); i < 2*mod; i++ {
			wrap = append(wrap, i&(mod-1))
		}
		streams = append(streams, wrap)
	}
	if half <= 1<<61 { // 边界回绕，绝对序号不溢出 int64
		streams = append(streams, []uint64{mod - 1, 0, 0, 1, 1, 2}, []uint64{mod - 2, mod - 1, 0, half, 1, 2})
	}
	return streams
}

// checkAntisymmetry 核验不变量 2：小宽度穷举全部有序对，大宽度确定性抽样。
func checkAntisymmetry(n int) error {
	mod := sar.Mod(n)
	var pairs [][2]uint64
	if mod <= 1<<12 {
		for i := uint64(0); i < mod*mod; i++ {
			pairs = append(pairs, [2]uint64{i & (mod - 1), i >> uint(n)})
		}
	} else {
		x := uint64(0x9e3779b97f4a7c15)
		for i := 0; i < 4096; i++ {
			x ^= x<<13 ^ x>>7 ^ x<<17
			pairs = append(pairs, [2]uint64{x & (mod - 1), (x * 2862933555777941757) & (mod - 1)})
		}
	}
	for _, p := range pairs {
		fwd, rev := sar.Cmp(p[0], p[1], mod), sar.Cmp(p[1], p[0], mod)
		if (fwd == Less) != (rev == Greater) || (fwd == Incomparable) != (rev == Incomparable) {
			return fmt.Errorf("api: antisymmetry violated at a=%d b=%d", p[0], p[1])
		}
	}
	return nil
}

// checkAgainstNaive 把流（先掩到 [0, M)）同时喂给 unwrap 与朴素参照，逐步比对（不变量 1、3、4）。
func (s *Space) checkAgainstNaive(stream []uint64) error {
	u, _ := unwrap.New(s.n)
	refLast, refHas, prevAbs := int64(0), false, int64(-1)
	for _, v := range stream {
		v &= s.mod - 1
		abs, err := u.Feed(v)
		refAbs, refErr := naiveFeed(s.mod, v, refLast, refHas)
		if (err == nil) != (refErr == nil) {
			return fmt.Errorf("api: error mismatch at s=%d: %v vs %v", v, err, refErr)
		}
		if err != nil { // 不变量 4：失败不留痕
			if u.Last() != refLast {
				return fmt.Errorf("api: last moved on rejection at s=%d", v)
			}
			continue
		}
		if abs != refAbs || u.Last() != refAbs { // 不变量 1：与参照一致
			return fmt.Errorf("api: mismatch at s=%d: got %d want %d", v, abs, refAbs)
		}
		if abs < prevAbs { // 不变量 3：绝对序号单调不减
			return fmt.Errorf("api: absolute decreased at s=%d", v)
		}
		prevAbs = abs
		refLast, refHas = refAbs, true
	}
	return nil
}

// naiveFeed 是刻意独立的朴素参照：线性扫描候选 v, v+M, v+2M, …判定。
func naiveFeed(mod, v uint64, last int64, has bool) (int64, error) {
	if !has {
		return int64(v), nil
	}
	c := v
	for c < uint64(last) {
		c += mod
	}
	switch delta := c - uint64(last); {
	case delta == 0:
		return last, nil
	case delta < mod/2:
		return int64(c), nil
	case delta == mod/2:
		return 0, unwrap.ErrIncomparable
	default:
		return 0, unwrap.ErrMovedBackward
	}
}

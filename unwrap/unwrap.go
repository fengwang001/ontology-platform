// Package unwrap 把模 M 的序号流单调展开为绝对单调递增的 int64。
// 依赖 sar，不依赖 api。
package unwrap

import (
	"errors"

	"ontology/sar"
)

// 可判定的哨兵错误，互不相同。
var (
	ErrWidth         = errors.New("unwrap: width N must satisfy 1 <= N <= 63") // n 非法
	ErrOutOfRange    = errors.New("unwrap: serial number out of range")        // s >= M
	ErrIncomparable  = errors.New("unwrap: half-circle, incomparable")         // d == M/2
	ErrMovedBackward = errors.New("unwrap: serial moved backward, stale")      // d > M/2
)

// Unwrapper 维护绝对序号 last（单调不减），逐个展开模 M 序号。
// Feed 会修改状态，需外部串行化；Last 为只读。
type Unwrapper struct {
	mod  uint64
	mask uint64
	last int64
	has  bool
	// checked 记录最近一次 Feed 展开时检查过的历史序号个数。
	// 非导出，不出现在任何公开接口；仅同包测试可读，用于证明每步 O(1)。
	checked int
}

// New 构造位宽 n 的展开器；n 非法（n < 1 或 n > 63）时整体失败返回 ErrWidth。
func New(n int) (*Unwrapper, error) {
	if n < 1 || n > 63 {
		return nil, ErrWidth
	}
	mod := sar.Mod(n)
	return &Unwrapper{mod: mod, mask: mod - 1}, nil
}

// Feed 把序号 s 展开为绝对序号。被拒（超范围/半圈/倒退）时 last 不变。
func (u *Unwrapper) Feed(s uint64) (int64, error) {
	u.checked = 0
	if s >= u.mod {
		return 0, ErrOutOfRange
	}
	if !u.has { // 首个序号：absolute = s
		u.checked = 1
		u.has = true
		u.last = int64(s)
		return u.last, nil
	}
	u.checked = 1 // 只与 last 比较一次，不扫描历史
	d := sar.Diff(uint64(u.last)&u.mask, s, u.mod)
	half := u.mod >> 1
	switch {
	case d == 0: // Equal：幂等重复，不前进
		return u.last, nil
	case d < half: // Less：前方半圈，前进 d
		u.last += int64(d)
		return u.last, nil
	case d == half: // Incomparable：半圈，拒绝
		return 0, ErrIncomparable
	default: // Greater：倒退/过期，拒绝
		return 0, ErrMovedBackward
	}
}

// Last 返回当前绝对序号；尚无序号时为 0。
func (u *Unwrapper) Last() int64 { return u.last }

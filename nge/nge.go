// Package nge 对外提供「下一个更大元素」计算与逐位自检。
package nge

import (
	"errors"
	"fmt"

	"ontology/mono"
)

// None 是无解位置的可判定表示（-1），与任何合法下标（含 0）不混淆。
const None = mono.None

// 三类可判定且互不相同的哨兵错误。
var (
	ErrNilInput = mono.ErrNilInput
	ErrTooLong  = mono.ErrTooLong
	ErrBadLimit = mono.ErrBadLimit
)

// ErrSelfCheck 表示自检发现答案违反结果不变量。
var ErrSelfCheck = errors.New("nge: self check failed")

// Solver 计算下一个更大元素；maxLen 为可配置的序列长度上限。
type Solver struct {
	sc *mono.Scanner
}

// New 创建 Solver；上限为 0 或负数时拒绝并返回 ErrBadLimit。
func New(maxLen int) (*Solver, error) {
	sc, err := mono.NewScanner(maxLen)
	if err != nil {
		return nil, err
	}
	return &Solver{sc: sc}, nil
}

// NextGreater 返回下标切片 ans：ans[i] 是 i 右侧第一个满足
// a[ans[i]] > a[i] 的位置（严格更大，相等不算）；不存在时为 None。
// 只读操作，可并发调用；nil 输入与超限输入被拒绝且不产生半截结果。
func (s *Solver) NextGreater(a []int) ([]int, error) {
	return s.sc.Scan(a)
}

// SelfCheck 对任意输入与输出逐位核验：ans[i] 为 None 时右侧确无更大者；
// 否则 ans[i] > i、a[ans[i]] > a[i]，且 (i, ans[i]) 之间没有更早的满足者。
// 只读操作，可并发调用。
func (s *Solver) SelfCheck(a, ans []int) error {
	if len(a) != len(ans) {
		return fmt.Errorf("%w: length mismatch %d vs %d", ErrSelfCheck, len(a), len(ans))
	}
	for i := range a {
		j := ans[i]
		if j == None {
			for k := i + 1; k < len(a); k++ {
				if a[k] > a[i] {
					return fmt.Errorf("%w: %d marked none but %d is greater", ErrSelfCheck, i, k)
				}
			}
			continue
		}
		if j <= i || j >= len(a) || a[j] <= a[i] {
			return fmt.Errorf("%w: invalid answer %d for position %d", ErrSelfCheck, j, i)
		}
		for k := i + 1; k < j; k++ {
			if a[k] > a[i] {
				return fmt.Errorf("%w: earlier greater at %d before %d", ErrSelfCheck, k, j)
			}
		}
	}
	return nil
}

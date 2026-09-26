// Package api 对外暴露蓄水池采样能力。
package api

import (
	"errors"
	"fmt"
	"slices"

	"ontology/rsv"
	"ontology/sampler"
)

// 四类可判定哨兵错误，互不相同。
var (
	ErrBadCapacity  = rsv.ErrBadCapacity
	ErrNilRNG       = sampler.ErrNilRNG
	ErrBadDraw      = rsv.ErrBadDraw
	ErrEmptyElement = sampler.ErrEmptyElement
)

// Sampler 是对外的采样器句柄。
type Sampler struct{ s *sampler.Sampler }

// New 创建采样器；k<=0 或 rng 为 nil 时返回可判定错误。
func New(k int, rng func(i int) int) (*Sampler, error) {
	s, err := sampler.New(k, rng)
	if err != nil {
		return nil, err
	}
	return &Sampler{s: s}, nil
}

// Feed 原子地喂入一批元素：任一条被拒则整批不生效。
func (a *Sampler) Feed(es []string) error { return a.s.FeedAll(es) }

// Sample 返回当前样本。
func (a *Sampler) Sample() []string { return a.s.Sample() }

// Size 返回已见元素总数。
func (a *Sampler) Size() int { return a.s.Size() }

// naiveReplay 朴素重放：保留全部历史，按同一 rng 重放第 k+1..N 步决策。
func naiveReplay(es []string, k int, rng func(int) int) []string {
	var slots []string
	for idx, e := range es {
		if i := idx + 1; i <= k {
			slots = append(slots, e)
		} else if j := rng(i); j <= k {
			slots[j-1] = e
		}
	}
	return slots
}

// SelfCheck 对内置元素序列核验四条不变量，全部通过返回 nil。
func (a *Sampler) SelfCheck() error {
	rngs := []func(int) int{
		func(i int) int { return 1 },
		func(i int) int { return i },
		func(i int) int { return int((uint(i)*2654435761)>>16)%i + 1 },
	}
	for _, k := range []int{1, 3, 5} {
		for _, n := range []int{0, 1, k, k + 1, 4*k + 2} {
			for _, rng := range rngs {
				es := make([]string, n)
				for idx := range es {
					es[idx] = fmt.Sprintf("e%d", idx+1)
				}
				s, err := New(k, rng)
				if err != nil {
					return err
				}
				if err := s.Feed(es); err != nil {
					return err
				}
				got := s.Sample()
				if len(got) != min(k, n) { // 不变量1：大小恒 min(k,N)
					return fmt.Errorf("selfcheck: k=%d n=%d size=%d", k, n, len(got))
				}
				if n <= k && !slices.Equal(got, es) { // 不变量2：前 k 必留且保序
					return fmt.Errorf("selfcheck: k=%d n=%d kept %v", k, n, got)
				}
				if !slices.Equal(got, naiveReplay(es, k, rng)) { // 不变量3：与朴素重放一致
					return fmt.Errorf("selfcheck: k=%d n=%d replay mismatch", k, n)
				}
			}
		}
	}
	// 不变量4：失败不留痕，且之后仍可正常使用。
	s, err := New(3, func(i int) int { return 1 })
	if err != nil {
		return err
	}
	if err := s.Feed([]string{"a", "b", "c", "d"}); err != nil {
		return err
	}
	before, n := s.Sample(), s.Size()
	if err := s.Feed([]string{"x", "", "y"}); !errors.Is(err, ErrEmptyElement) {
		return fmt.Errorf("selfcheck: empty element err=%v", err)
	}
	if !slices.Equal(s.Sample(), before) || s.Size() != n {
		return errors.New("selfcheck: rejected feed changed state")
	}
	if err := s.Feed([]string{"z"}); err != nil || s.Size() != n+1 {
		return errors.New("selfcheck: unusable after rejection")
	}
	bad, _ := New(2, func(i int) int { return i + 1 })
	if err := bad.Feed([]string{"a", "b", "c"}); !errors.Is(err, ErrBadDraw) || bad.Size() != 0 {
		return errors.New("selfcheck: bad draw left trace")
	}
	if _, err := New(0, func(i int) int { return 1 }); !errors.Is(err, ErrBadCapacity) {
		return errors.New("selfcheck: bad capacity not detected")
	}
	if _, err := New(1, nil); !errors.Is(err, ErrNilRNG) {
		return errors.New("selfcheck: nil rng not detected")
	}
	return nil
}

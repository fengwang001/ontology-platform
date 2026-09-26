// Package api 是蓄水池采样的对外门面：构造、批量喂入、取样、
// 已见计数与内置自检。它只依赖 sampler，依赖方向 api -> sampler -> rsv。
package api

import (
	"errors"
	"fmt"
	"reflect"

	"ontology/sampler"
)

// 四类可判定哨兵错误，彼此不同（透传 sampler/rsv 的定义）。
var (
	ErrBadCapacity  = sampler.ErrBadCapacity  // k<=0
	ErrNilRNG       = sampler.ErrNilRNG       // rng 为 nil
	ErrJOutOfRange  = sampler.ErrJOutOfRange  // rng(i) 返回 <1 或 >i
	ErrEmptyElement = sampler.ErrEmptyElement // 元素为空串

	// ErrSelfCheckFailed：内置自检发现某条不变量被破坏。
	ErrSelfCheckFailed = errors.New("api: self-check failed")
)

// API 是线程安全的对外采样器。零值不可用，须经 New 构造。
type API struct{ s *sampler.Sampler }

// New 以容量 k 与可注入随机源构造采样器。
// rng(i) 为第 i 步（i>k）返回 j∈[1,i]。
func New(k int, rng func(i int) int) (*API, error) {
	s, err := sampler.New(k, rng)
	if err != nil {
		return nil, err
	}
	return &API{s: s}, nil
}

// Feed 批量喂入元素。任一条被拒（空串或某步随机数越界）则整批不生效，
// 已存槽位与已见计数保持不变，之后仍可继续使用。
func (a *API) Feed(es []string) error { return a.s.FeedMany(es) }

// Sample 返回当前样本（槽 1..min(k,N)）的副本。
func (a *API) Sample() []string { return a.s.Sample() }

// Size 返回已见元素总数 N。
func (a *API) Size() int { return a.s.Size() }

// SelfCheck 用一组内置确定性序列核验第二节四条不变量，不改动接收者状态。
// 全部成立返回 nil；否则返回包装了 ErrSelfCheckFailed 的错误说明失败项。
func (a *API) SelfCheck() error {
	// 第三节序列：k=3，A B C D E F，第4/5/6 步 j=2/4/1。
	js := map[int]int{4: 2, 5: 4, 6: 1}
	rng := func(i int) int { return js[i] }
	es := []string{"A", "B", "C", "D", "E", "F"}
	want := [][]string{
		{"A"}, {"A", "B"}, {"A", "B", "C"},
		{"A", "D", "C"}, {"A", "D", "C"}, {"F", "D", "C"},
	}

	s, err := sampler.New(3, rng) // 独立实例：自检不影响接收者
	if err != nil {
		return fmt.Errorf("%w: construct: %v", ErrSelfCheckFailed, err)
	}
	for i, e := range es { // 不变量 1（大小=min(k,N)）与 2（前 k 保序）
		if err := s.FeedMany([]string{e}); err != nil {
			return fmt.Errorf("%w: step %d: %v", ErrSelfCheckFailed, i+1, err)
		}
		n := i + 1
		size := n
		if size > 3 {
			size = 3
		}
		got := s.Sample()
		if len(got) != size || !reflect.DeepEqual(got, want[i]) {
			return fmt.Errorf("%w: invariant 1/2 at step %d: got %v want %v",
				ErrSelfCheckFailed, n, got, want[i])
		}
	}

	// 不变量 3：与朴素重放逐槽一致。
	naive, err := sampler.NaiveReplay(3, func(i int) int { return js[i] }, es)
	if err != nil {
		return fmt.Errorf("%w: naive replay: %v", ErrSelfCheckFailed, err)
	}
	if !reflect.DeepEqual(s.Sample(), naive) {
		return fmt.Errorf("%w: invariant 3: online %v naive %v",
			ErrSelfCheckFailed, s.Sample(), naive)
	}

	// 不变量 4：坏批（空串 + 越界 j）不得留痕，四类拒绝均可判定。
	if _, err := sampler.New(0, rng); !errors.Is(err, ErrBadCapacity) {
		return fmt.Errorf("%w: invariant 4: k<=0 not rejected", ErrSelfCheckFailed)
	}
	if _, err := sampler.New(3, nil); !errors.Is(err, ErrNilRNG) {
		return fmt.Errorf("%w: invariant 4: nil rng not rejected", ErrSelfCheckFailed)
	}
	saved := s.Sample()
	savedN := s.Size()
	if err := s.FeedMany([]string{"G", ""}); !errors.Is(err, ErrEmptyElement) {
		return fmt.Errorf("%w: invariant 4: empty element err=%v", ErrSelfCheckFailed, err)
	}
	out, err := sampler.New(3, func(int) int { return 99 }) // j=99>i
	if err != nil {
		return fmt.Errorf("%w: construct: %v", ErrSelfCheckFailed, err)
	}
	if err := out.FeedMany(es); !errors.Is(err, ErrJOutOfRange) {
		return fmt.Errorf("%w: invariant 4: j out of range err=%v", ErrSelfCheckFailed, err)
	}
	if !reflect.DeepEqual(s.Sample(), saved) || s.Size() != savedN {
		return fmt.Errorf("%w: invariant 4: rejected batch left a trace", ErrSelfCheckFailed)
	}
	return nil
}

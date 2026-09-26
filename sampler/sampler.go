// Package sampler 驱动蓄水池：逐元素喂入、维护已见计数、产出当前样本。
package sampler

import (
	"errors"
	"sync"

	"ontology/rsv"
)

var (
	// ErrNilRNG 表示随机源为 nil。
	ErrNilRNG = errors.New("sampler: rng must not be nil")
	// ErrEmptyElement 表示元素为空串。
	ErrEmptyElement = errors.New("sampler: element must not be empty")
)

// Sampler 在线驱动一个 rsv.Reservoir。
type Sampler struct {
	mu         sync.Mutex
	r          *rsv.Reservoir
	rng        func(i int) int
	n          int // 已见元素总数
	lastAccess int // 最近一次 Sample 访问的已存元素个数
}

// New 创建采样器；k<=0 或 rng 为 nil 时失败。
func New(k int, rng func(i int) int) (*Sampler, error) {
	if rng == nil {
		return nil, ErrNilRNG
	}
	r, err := rsv.New(k)
	if err != nil {
		return nil, err
	}
	return &Sampler{r: r, rng: rng}, nil
}

// Feed 喂入单个元素。
func (s *Sampler) Feed(e string) error { return s.FeedAll([]string{e}) }

// FeedAll 原子地喂入一批元素：任一条被拒则整批不生效。
func (s *Sampler) FeedAll(es []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.feedAll(es)
}

// feedAll 先校验全部元素与全部 rng 抽取，通过后才改动任何状态（失败不留痕）。
func (s *Sampler) feedAll(es []string) error {
	for _, e := range es {
		if e == "" {
			return ErrEmptyElement
		}
	}
	k := s.r.K()
	draws := make([]int, len(es))
	for idx := range es {
		if i := s.n + idx + 1; i > k {
			j := s.rng(i)
			if j < 1 || j > i {
				return rsv.ErrBadDraw
			}
			draws[idx] = j
		}
	}
	for idx, e := range es {
		i := s.n + 1
		if err := s.r.Step(i, e, draws[idx]); err != nil {
			return err // 预校验后不可达
		}
		s.n = i
	}
	return nil
}

// Sample 返回当前样本（槽 1..min(k,n) 顺序的副本）。
func (s *Sampler) Sample() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := s.r.Slots()
	s.lastAccess = len(out) // 只访问已存的 min(k,n) 个元素，不扫历史
	return out
}

// Size 返回已见元素总数。
func (s *Sampler) Size() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.n
}

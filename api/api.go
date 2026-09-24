// Package api 是加权蓄水池抽样 A-Res 的对外入口：New/Offer/Sample/Consumed/SelfCheck。
// 一批 Offer 先全行预校验（ID → W==0 → W 非法 → 随机源用尽，按行序报第一条），
// 全部通过后才落池并推进随机源游标，因此任何被拒批次都不留痕。
package api

import (
	"errors"
	"math"
	"sort"
	"sync"

	"ontology/resv"
	"ontology/rnd"
)

// 四类可判定、互不相同的哨兵错误。
var (
	// ErrInvalidInput：ID 空/重复（含同批内）、k<=0、u 不在 (0,1)。
	ErrInvalidInput = errors.New("api: invalid input")
	// ErrZeroWeight：W 恰好为 0（不消耗随机数）。
	ErrZeroWeight = errors.New("api: weight must not be zero")
	// ErrBadWeight：W<0、NaN 或 ±Inf。
	ErrBadWeight = errors.New("api: weight must be finite and positive")
	// ErrExhausted：需要取数时随机源已用尽（即 rnd.ErrExhausted）。
	ErrExhausted = rnd.ErrExhausted
)

// Item 是一个带权元素。
type Item struct {
	ID string
	W  float64
}

// SampleItem 是样本中的一个元素，键为 u^(1/W)。
type SampleItem = resv.Entry

type accepted struct {
	id string
	w  float64
	ui int // 使用的随机数在 us 中的下标
}

// Sampler 是并发安全的加权蓄水池抽样器。
type Sampler struct {
	mu   sync.RWMutex
	k    int
	src  *rnd.Source
	pool *resv.Pool
	seen map[string]bool
	hist []accepted // 全部被接受（通过校验）的元素，供 SelfCheck 做批量参照
}

// New 创建容量 k、随机源为 us 的抽样器；k<=0 或存在 u∉(0,1) 返回 ErrInvalidInput。
func New(k int, us []float64) (*Sampler, error) {
	if k <= 0 {
		return nil, ErrInvalidInput
	}
	for _, u := range us {
		if !rnd.ValidU(u) {
			return nil, ErrInvalidInput
		}
	}
	return &Sampler{
		k: k, src: rnd.New(us), pool: resv.New(k), seen: map[string]bool{},
	}, nil
}

// Offer 按序处理一批元素。任一条被拒则整批不生效（池与游标都不变）。
func (s *Sampler) Offer(items []Item) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	batch := map[string]bool{}
	need := 0
	for _, it := range items { // 预校验：不触碰任何状态
		if it.ID == "" || s.seen[it.ID] || batch[it.ID] {
			return ErrInvalidInput
		}
		if it.W == 0 {
			return ErrZeroWeight
		}
		if it.W < 0 || math.IsNaN(it.W) || math.IsInf(it.W, 0) {
			return ErrBadWeight
		}
		need++
		if need > s.src.Remaining() {
			return ErrExhausted
		}
		batch[it.ID] = true
	}

	base := s.src.Consumed()
	for i, it := range items { // 应用期：每条合法元素恰好消耗一个 u
		u, err := s.src.Take()
		if err != nil {
			return err // 预校验已保证不可能发生
		}
		s.pool.Offer(it.ID, it.W, u)
		s.seen[it.ID] = true
		s.hist = append(s.hist, accepted{id: it.ID, w: it.W, ui: base + i})
	}
	return nil
}

// Sample 按排名从高到低（键大优先；键相等先到优先）返回当前样本副本。
func (s *Sampler) Sample() []SampleItem {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.pool.Entries()
}

// Consumed 返回已消耗的随机数个数（== 被接受元素个数）。
func (s *Sampler) Consumed() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.src.Consumed()
}

// SelfCheck 核验第二节四条不变量；全部成立返回 nil。
func (s *Sampler) SelfCheck() error {
	s.mu.RLock()
	defer s.mu.RUnlock()

	got := s.pool.Entries()
	want := make([]SampleItem, len(s.hist))
	for i, a := range s.hist {
		want[i] = SampleItem{ID: a.id, W: a.w, Key: math.Pow(s.src.At(a.ui), 1.0/a.w)}
	}
	sort.SliceStable(want, func(i, j int) bool { return want[i].Key > want[j].Key })
	if len(want) > s.k {
		want = want[:s.k]
	}
	if len(got) != len(want) || s.src.Consumed() != len(s.hist) || len(got) != min(s.k, len(s.hist)) {
		return errors.New("selfcheck: size/consumed invariant violated")
	}
	ids := map[string]bool{}
	for i, e := range got {
		if got[i].ID != want[i].ID || got[i].Key != want[i].Key || ids[e.ID] {
			return errors.New("selfcheck: sample/order/uniqueness invariant violated")
		}
		ids[e.ID] = true
	}
	return nil
}

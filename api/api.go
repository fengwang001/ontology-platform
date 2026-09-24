// Package api 对外提供加权蓄水池抽样 A-Res（Efraimidis–Spirakis）。
package api

import (
	"errors"
	"fmt"
	"math"
	"sort"
	"sync"

	"ontology/resv"
	"ontology/rnd"
)

// 四类可判定错误，互不相同。
var (
	ErrZeroWeight = errors.New("api: weight is zero")
	ErrBadWeight  = errors.New("api: weight not finite positive")
	ErrExhausted  = rnd.ErrExhausted
	ErrBadInput   = errors.New("api: invalid input")
)

// Item 是上游待抽元素。
type Item struct {
	ID string
	W  float64
}

// Sample 是抽样结果：ID、权重与键。
type Sample struct {
	ID     string
	W, Key float64
}

// Sampler 并发安全：Offer 串行化，Sample/Consumed/SelfCheck 可并发。
type Sampler struct {
	mu   sync.Mutex
	src  *rnd.Source
	pool *resv.Pool
	ids  map[string]bool // 全部被接受元素的 ID（含已被替换出池者）
}

// New 创建容量 k 的抽样器；k<=0 或任一 u 不在 (0,1) 返回 ErrBadInput。
func New(k int, us []float64) (*Sampler, error) {
	if k <= 0 {
		return nil, ErrBadInput
	}
	src, err := rnd.New(us)
	if err != nil {
		return nil, ErrBadInput
	}
	return &Sampler{src: src, pool: resv.NewPool(k), ids: map[string]bool{}}, nil
}

// Offer 按序处理一批；任一条被拒则整批不生效（池与游标不变）。
// 每行先查元素、再查权重（先 0 后非法）、最后查随机源；按行序报第一条出错行。
func (s *Sampler) Offer(items []Item) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	seen, need := make(map[string]bool, len(items)), 0
	for _, it := range items { // 第一阶段：全量预检，失败即返、不留痕
		if it.ID == "" || s.ids[it.ID] || seen[it.ID] {
			return ErrBadInput
		}
		seen[it.ID] = true
		if it.W == 0 {
			return ErrZeroWeight
		}
		if it.W < 0 || math.IsNaN(it.W) || math.IsInf(it.W, 0) {
			return ErrBadWeight
		}
		if need++; need > s.src.Remaining() {
			return ErrExhausted
		}
	}
	for _, it := range items { // 第二阶段：预检已通过，不会失败
		if _, err := s.pool.Consider(s.src, it.ID, it.W); err != nil {
			return err
		}
		s.ids[it.ID] = true
	}
	return nil
}

// Sample 按排名从高到低返回池中样本。
func (s *Sampler) Sample() []Sample {
	s.mu.Lock()
	defer s.mu.Unlock()
	items := s.pool.Items()
	out := make([]Sample, len(items))
	for i, it := range items {
		out[i] = Sample{ID: it.ID, W: it.W, Key: it.Key}
	}
	return out
}

// Consumed 返回已消耗的随机数个数，等于被接受元素个数。
func (s *Sampler) Consumed() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.src.Consumed()
}

// reference 批量参照：全部被接受元素按序取 u 算键，按排名全序整体排序取前 k。
func reference(items []Item, us []float64, k int) []Sample {
	out := make([]Sample, len(items))
	for i, it := range items {
		out[i] = Sample{ID: it.ID, W: it.W, Key: math.Pow(us[i], 1/it.W)}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Key > out[j].Key })
	if len(out) > k {
		out = out[:k]
	}
	return out
}

// SelfCheck 用内置序列核验第二节四条不变量；不改动接收器状态，可并发调用。
func (s *Sampler) SelfCheck() error {
	us, ws := []float64{0.30, 0.49, 0.81, 0.0625, 0.72, 0.64, 0.9, 0.25}, []float64{1, 2, 0.5, 0, 4, 1, 2, 0.5}
	ids := []string{"a", "b", "c", "d", "e", "f", "g", "h"}
	sm, err := New(2, us)
	if err != nil {
		return err
	}
	var valid []Item
	for i, id := range ids { // 不变量 4：d(W=0) 被拒且不留痕
		err := sm.Offer([]Item{{id, ws[i]}})
		if id == "d" {
			if !errors.Is(err, ErrZeroWeight) || sm.Consumed() != 3 {
				return fmt.Errorf("selfcheck: zero weight not rejected cleanly")
			}
			continue
		}
		if err != nil {
			return fmt.Errorf("selfcheck: offer %s: %w", id, err)
		}
		valid = append(valid, Item{id, ws[i]})
	}
	used := us[:sm.Consumed()]                          // 第 i 个被接受元素用的恰是 us[i-1]
	got, want := sm.Sample(), reference(valid, used, 2) // 不变量 1；参照结果即第三节的 {h, g}
	if sm.Consumed() != 7 || len(got) != 2 {            // 不变量 2、3
		return fmt.Errorf("selfcheck: consumed=%d len=%d", sm.Consumed(), len(got))
	}
	for i := range want {
		if got[i] != want[i] {
			return fmt.Errorf("selfcheck: sample[%d]=%v want %v", i, got[i], want[i])
		}
	}
	return nil
}

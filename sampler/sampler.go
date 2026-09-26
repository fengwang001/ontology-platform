// Package sampler 驱动单个蓄水池：逐元素喂入、维护已见元素计数，
// 并返回当前样本。它只依赖 rsv，不保存全部历史，内存中只留 k 个元素。
package sampler

import (
	"errors"
	"sync"
	"sync/atomic"

	"ontology/rsv"
)

// 哨兵错误（rsv 的三个在此重导出，保证整库可判定且彼此不同）。
var (
	ErrBadCapacity  = rsv.ErrBadCapacity
	ErrEmptyElement = rsv.ErrEmptyElement
	ErrJOutOfRange  = rsv.ErrJOutOfRange
	ErrNilRNG       = errors.New("sampler: rng func must not be nil")
)

// Sampler 是算法 R 的在线驱动器。零值不可用，须经 New 构造。
type Sampler struct {
	mu sync.RWMutex

	r    *rsv.Reservoir
	rng  func(i int) int // 第 i 步（i>k）返回 j∈[1,i]
	seen int             // 已见元素总数 N

	// sampleVisits：最近一次 Sample 为生成结果而访问的已存元素个数。
	// 刻意非导出：不属于公开接口，只由同包测试白盒核验。
	// 用原子类型：Sample 是只读操作，可被多 goroutine 并发调用。
	sampleVisits atomic.Int64
}

// New 以容量 k 与可注入随机源构造采样器。
func New(k int, rng func(i int) int) (*Sampler, error) {
	if k <= 0 {
		return nil, ErrBadCapacity
	}
	if rng == nil {
		return nil, ErrNilRNG
	}
	r, err := rsv.New(k)
	if err != nil {
		return nil, err
	}
	return &Sampler{r: r, rng: rng}, nil
}

// Feed 喂入单个元素；失败（如空串、随机数越界）不改变任何状态。
func (s *Sampler) Feed(e string) error {
	return s.FeedMany([]string{e})
}

// FeedMany 整批喂入：任一条被拒则整批不生效（槽位与已见计数都不变）。
func (s *Sampler) FeedMany(es []string) error {
	for _, e := range es { // 预检：空串拒绝发生在任何状态变更之前
		if e == "" {
			return ErrEmptyElement
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	shadow := s.r.Clone() // 影子执行：全部成功才提交
	for idx, e := range es {
		i := s.seen + idx + 1
		j := 0
		if i > s.r.Cap() {
			j = s.rng(i)
			if j < 1 || j > i {
				return ErrJOutOfRange // 丢弃 shadow，原蓄水池与 seen 均不变
			}
		}
		if err := shadow.Offer(i, e, j); err != nil {
			return err
		}
	}
	s.r = shadow
	s.seen += len(es)
	return nil
}

// Sample 返回当前样本（槽 1..min(k,N)）的副本，并记录本行为生成结果
// 而访问的已存元素个数（恰好等于已填槽位数，与历史总长无关）。
func (s *Sampler) Sample() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	out := s.r.Snapshot() // Snapshot 逐个访问已存槽位，每个恰好一次
	s.sampleVisits.Store(int64(len(out)))
	return out
}

// Size 返回已见元素总数 N。
func (s *Sampler) Size() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.seen
}

// NaiveReplay 是参照实现：先收齐全部元素，再用同一 rng 重放
// 第 k+1..N 步的替换/丢弃决策。在线结果必须与它逐槽相同。
func NaiveReplay(k int, rng func(i int) int, es []string) ([]string, error) {
	if k <= 0 {
		return nil, ErrBadCapacity
	}
	if rng == nil {
		return nil, ErrNilRNG
	}
	for _, e := range es {
		if e == "" {
			return nil, ErrEmptyElement
		}
	}
	r, err := rsv.New(k)
	if err != nil {
		return nil, err
	}
	for i, e := range es {
		step := i + 1
		j := 0
		if step > k {
			j = rng(step)
			if j < 1 || j > step {
				return nil, ErrJOutOfRange
			}
		}
		if err := r.Offer(step, e, j); err != nil {
			return nil, err
		}
	}
	return r.Snapshot(), nil
}

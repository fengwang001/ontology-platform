// Package head 实现指标样本的内存存储：序列内样本按 ts 严格升序且唯一，
// 支持乱序窗口、陈旧标记与原子批量提交。
package head

import (
	"errors"
	"sort"
	"sync"
)

var (
	ErrInvalidParam  = errors.New("head: invalid parameter")
	ErrTooManySeries = errors.New("head: series count limit reached")
	ErrConflict      = errors.New("head: conflicting sample at timestamp")
	ErrTooOld        = errors.New("head: sample too old")
)

const maxNameLen = 128
const maxTs = int64(1e12)

// Sample 是序列中的一个样本；Stale 为真表示陈旧标记（占用 ts、无值）。
type Sample struct {
	Ts    int64
	V     int64
	Stale bool
}

// Item 是一次批量写入中的一项。
type Item struct {
	Series string
	Ts     int64
	V      int64
	Stale  bool
}

type series struct{ samples []Sample } // samples 按 Ts 严格升序

// Head 保存全部序列。写操作（含整批提交）互斥，读操作可并发；
// 读者看到的一次批量提交要么全部可见，要么全部不可见。
type Head struct {
	mu            sync.RWMutex
	lookback, ooo int64
	smax, lim     int
	series        map[string]*series
	dups          int64
}

// NewHead 校验构造参数：L∈[1,1e9]，Ooo∈[0,1e9]，Smax∈[1,1e6]，Lim∈[1,1e5]。
func NewHead(lookback, ooo int64, smax, lim int) (*Head, error) {
	if lookback < 1 || lookback > 1e9 || ooo < 0 || ooo > 1e9 ||
		smax < 1 || smax > 1e6 || lim < 1 || lim > 1e5 {
		return nil, ErrInvalidParam
	}
	return &Head{lookback: lookback, ooo: ooo, smax: smax, lim: lim, series: make(map[string]*series)}, nil
}

func (h *Head) Lookback() int64 { return h.lookback } // 回看窗口 L（毫秒）
func (h *Head) Limit() int      { return h.lim }      // 单次抓取样本上限 Lim

// Dups 返回被判定为重复（同 ts、同种类、同值）的样本数。
func (h *Head) Dups() int64 {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.dups
}

// Append 写入一个 int64 样本；AppendStale 写入一个陈旧标记。
func (h *Head) Append(name string, ts, v int64) error {
	return h.ApplyBatch([]Item{{Series: name, Ts: ts, V: v}})
}
func (h *Head) AppendStale(name string, ts int64) error {
	return h.ApplyBatch([]Item{{Series: name, Ts: ts, Stale: true}})
}

// ApplyBatch 先逐项预校验（重复不算失败，新序列按集合内累计计入 Smax），
// 任一项失败则全部不写；否则作为一个整体原子提交。
func (h *Head) ApplyBatch(items []Item) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	pending := map[string][]Item{} // 本批次内已通过的待写项（按序列分组）
	newCount := 0
	var dups int64
	for _, it := range items {
		if err := checkParam(it); err != nil {
			return err
		}
		s := h.series[it.Series]
		pend := pending[it.Series]
		if s == nil && pend == nil {
			if len(h.series)+newCount >= h.smax {
				return ErrTooManySeries
			}
			newCount++
		}
		dup, err := checkInsert(s, pend, it, h.ooo)
		if err != nil {
			return err
		}
		if dup {
			dups++
		} else {
			pending[it.Series] = append(pend, it)
		}
	}
	for _, it := range items {
		h.applyOne(it)
	}
	h.dups += dups
	return nil
}

func checkParam(it Item) error {
	if it.Series == "" || len(it.Series) > maxNameLen || it.Ts < 0 || it.Ts > maxTs {
		return ErrInvalidParam
	}
	return nil
}

// checkInsert 在“已提交序列 + 本批次已通过项”上校验一个写入项。
func checkInsert(s *series, pend []Item, it Item, ooo int64) (dup bool, err error) {
	if s != nil {
		if old, ok := findAt(s.samples, it.Ts); ok {
			return classify(old, it)
		}
	}
	for _, p := range pend {
		if p.Ts == it.Ts {
			return classify(Sample{Ts: p.Ts, V: p.V, Stale: p.Stale}, it)
		}
	}
	maxTs, has := int64(0), false
	if s != nil && len(s.samples) > 0 {
		maxTs, has = s.samples[len(s.samples)-1].Ts, true
	}
	for _, p := range pend {
		if !has || p.Ts > maxTs {
			maxTs, has = p.Ts, true
		}
	}
	if has && it.Ts < maxTs && maxTs-it.Ts > ooo {
		return false, ErrTooOld
	}
	return false, nil
}

// classify 处理同 ts 已有样本的情况：种类与值都相同为重复，否则冲突。
func classify(old Sample, it Item) (bool, error) {
	if old.Stale == it.Stale && (it.Stale || old.V == it.V) {
		return true, nil
	}
	return false, ErrConflict
}

func findAt(samples []Sample, ts int64) (Sample, bool) {
	i := sort.Search(len(samples), func(i int) bool { return samples[i].Ts >= ts })
	if i < len(samples) && samples[i].Ts == ts {
		return samples[i], true
	}
	return Sample{}, false
}

// applyOne 应用一个已通过预校验的写入项；重复项跳过（已在预校验阶段计数）。
func (h *Head) applyOne(it Item) {
	s := h.series[it.Series]
	if s == nil {
		s = &series{}
		h.series[it.Series] = s
	}
	i := sort.Search(len(s.samples), func(i int) bool { return s.samples[i].Ts >= it.Ts })
	if i < len(s.samples) && s.samples[i].Ts == it.Ts {
		return
	}
	s.samples = append(s.samples, Sample{})
	copy(s.samples[i+1:], s.samples[i:])
	s.samples[i] = Sample{Ts: it.Ts, V: it.V, Stale: it.Stale}
}

// RLock/RUnlock 供 query 包在一个读临界区内做多次查找。
func (h *Head) RLock()   { h.mu.RLock() }
func (h *Head) RUnlock() { h.mu.RUnlock() }

// SeriesSamples 返回序列的共享样本切片（只读），调用方须持有读锁。
func (h *Head) SeriesSamples(name string) []Sample {
	if s := h.series[name]; s != nil {
		return s.samples
	}
	return nil
}

// Snapshot 返回全部序列的副本，供测试对照。
func (h *Head) Snapshot() map[string][]Sample {
	h.mu.RLock()
	defer h.mu.RUnlock()
	out := make(map[string][]Sample, len(h.series))
	for name, s := range h.series {
		out[name] = append([]Sample(nil), s.samples...)
	}
	return out
}

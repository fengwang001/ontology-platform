// Package head 实现并发安全的指标样本存储：序列内样本按 ts 唯一且升序，
// 支持乱序窗口内的迟到写入、陈旧标记，以及可预校验的原子批量写入。
package head

import (
	"errors"
	"sort"
	"sync"
)

const (
	maxSeriesLen = 128
	maxTS        = int64(1e12)
	maxWindow    = int64(1e9)
)

var (
	ErrInvalid       = errors.New("head: invalid argument")
	ErrTooManySeries = errors.New("head: series limit reached")
	ErrConflict      = errors.New("head: conflicting sample at timestamp")
	ErrTooOld        = errors.New("head: sample too old")
)

// Sample 是序列中的一个样本；Stale 为真表示陈旧标记（没有值）。
type Sample struct {
	Ts    int64
	V     int64
	Stale bool
}

// Item 是批量写入中的单项。
type Item struct {
	Series string
	Ts     int64
	V      int64
	Stale  bool
}

type series struct {
	samples []Sample // 按 Ts 严格升序
}

func (s *series) clone() *series {
	c := &series{samples: make([]Sample, len(s.samples))}
	copy(c.samples, s.samples)
	return c
}

// Head 是样本库。L、Ooo、Smax、Lim 为构造参数，Dups 统计重复样本数。
type Head struct {
	mu     sync.RWMutex
	L      int64
	Ooo    int64
	Smax   int
	Lim    int
	series map[string]*series
	Dups   int64
}

// New 校验构造参数并创建空样本库。
func New(l, ooo int64, smax, lim int) (*Head, error) {
	if l < 1 || l > maxWindow || ooo < 0 || ooo > maxWindow ||
		smax < 1 || smax > 1e6 || lim < 1 || lim > 1e5 {
		return nil, ErrInvalid
	}
	return &Head{L: l, Ooo: ooo, Smax: smax, Lim: lim, series: map[string]*series{}}, nil
}

// Append 写入一个值样本。
func (h *Head) Append(name string, ts int64, v int64) error {
	return h.ApplyBatch([]Item{{Series: name, Ts: ts, V: v}})
}

// AppendStale 写入一个陈旧标记（占用 ts、没有值）。
func (h *Head) AppendStale(name string, ts int64) error {
	return h.ApplyBatch([]Item{{Series: name, Ts: ts, Stale: true}})
}

// ApplyBatch 先逐项校验整批样本（重复不算失败，仅计入 Dups），
// 任一项失败则全部不写；否则在同一把写锁内整体提交，对并发读原子可见。
func (h *Head) ApplyBatch(items []Item) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	touched := map[string]*series{} // 本批被修改的序列（写时复制）
	added := 0                      // 本批累计新序列数
	var dups int64
	// editable 返回本批可就地修改的序列副本：首次触碰时写时复制。
	editable := func(name string) *series {
		if s, ok := touched[name]; ok {
			return s
		}
		if s := h.series[name]; s != nil {
			touched[name] = s.clone()
			return touched[name]
		}
		return nil
	}
	for _, it := range items {
		if it.Series == "" || len(it.Series) > maxSeriesLen || it.Ts < 0 || it.Ts > maxTS {
			return ErrInvalid
		}
		s := editable(it.Series)
		if s == nil {
			if len(h.series)+added >= h.Smax {
				return ErrTooManySeries
			}
			touched[it.Series] = &series{samples: []Sample{{Ts: it.Ts, V: it.V, Stale: it.Stale}}}
			added++
			continue
		}
		n := len(s.samples)
		last := s.samples[n-1]
		if it.Ts > last.Ts {
			s.samples = append(s.samples, Sample{Ts: it.Ts, V: it.V, Stale: it.Stale})
			continue
		}
		i := sort.Search(n, func(i int) bool { return s.samples[i].Ts >= it.Ts })
		if i < n && s.samples[i].Ts == it.Ts {
			cur := s.samples[i]
			if cur.Stale == it.Stale && (it.Stale || cur.V == it.V) {
				dups++
				continue
			}
			return ErrConflict
		}
		if last.Ts-it.Ts > h.Ooo {
			return ErrTooOld
		}
		s.samples = append(s.samples, Sample{})
		copy(s.samples[i+1:], s.samples[i:])
		s.samples[i] = Sample{Ts: it.Ts, V: it.V, Stale: it.Stale}
	}
	for name, s := range touched {
		h.series[name] = s
	}
	h.Dups += dups
	return nil
}

// RLock/RUnlock 供 query 包在多次查找之间保持读一致性（对整次批量写入原子）。
func (h *Head) RLock()   { h.mu.RLock() }
func (h *Head) RUnlock() { h.mu.RUnlock() }

// SeriesSamples 返回序列的样本切片。调用方必须持有读锁，且不得修改返回值。
func (h *Head) SeriesSamples(name string) ([]Sample, bool) {
	s, ok := h.series[name]
	if !ok {
		return nil, false
	}
	return s.samples, true
}

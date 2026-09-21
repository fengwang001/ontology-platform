package ontology

import (
	"errors"
	"fmt"
	"math"
	"sort"
	"sync"
)

// ErrInvalidN 表示 N 小于等于 0，可用 errors.Is 判定。
var ErrInvalidN = errors.New("ontology: N must be positive")

// Config 是选择器的配置。
type Config struct {
	// N 是每组保留的最大行数，必须为正。
	N int
	// GroupColumn 是分组键列名。
	GroupColumn string
	// ScoreColumn 是分数列名，值必须为数值。
	ScoreColumn string
	// TieColumn 是分数相同时用于打破并列的字符串列名。
	TieColumn string
}

// GroupSnapshot 是一个分组在某一时刻的快照。
type GroupSnapshot struct {
	Key     GroupKey
	Rows    []Row
	Skipped int64 // 分数缺失或非数值的行数
	NaN     int64 // 分数为 NaN 的行数
}

// Selector 是并发安全的分组内 Top-N 选择器。
type Selector struct {
	cfg Config

	mu        sync.Mutex
	groups    map[GroupKey]*group
	processed int64
}

// New 创建一个选择器；N 小于等于 0 时返回 ErrInvalidN。
func New(cfg Config) (*Selector, error) {
	if cfg.N <= 0 {
		return nil, fmt.Errorf("%w, got %d", ErrInvalidN, cfg.N)
	}
	return &Selector{
		cfg:    cfg,
		groups: make(map[GroupKey]*group),
	}, nil
}

// Add 喂入一行。分数缺失/非数值/为 NaN 的行不参与排名，
// 但仍计入对应分组的跳过计数。Add 会拷贝行内容。
func (s *Selector) Add(fields map[string]any) {
	gv, gok := fields[s.cfg.GroupColumn]
	key := keyOf(gv, gok)

	sv, sok := fields[s.cfg.ScoreColumn]
	score, numericOK := numeric(sv)
	if !sok {
		numericOK = false
	}

	tie, _ := fields[s.cfg.TieColumn].(string)

	s.mu.Lock()
	defer s.mu.Unlock()
	s.processed++

	g := s.groups[key]
	if g == nil {
		g = &group{}
		s.groups[key] = g
	}

	switch {
	case !numericOK:
		g.skipped++
	case math.IsNaN(score):
		g.nan++
	default:
		copied := make(map[string]any, len(fields))
		for k, v := range fields {
			copied[k] = v
		}
		g.add(Row{Fields: copied, Score: score, Tie: tie}, s.cfg.N)
	}
}

// Stats 返回当前内部持有的行数与当前组数。
// 持有行数恒不超过 组数 × N。
func (s *Selector) Stats() (heldRows, groupCount int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, g := range s.groups {
		heldRows += len(g.rows)
	}
	return heldRows, len(s.groups)
}

// Processed 返回已处理（Add 调用）的总行数，含被跳过的行。
func (s *Selector) Processed() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.processed
}

// Snapshot 返回所有组的快照：组间按 GroupKey 升序（缺失 < nil <
// 空字符串 < 普通键），组内按复合次序。返回值与内部状态互不影响。
func (s *Selector) Snapshot() []GroupSnapshot {
	s.mu.Lock()
	defer s.mu.Unlock()

	keys := make([]GroupKey, 0, len(s.groups))
	for k := range s.groups {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return lessKey(keys[i], keys[j]) })

	out := make([]GroupSnapshot, len(keys))
	for i, k := range keys {
		g := s.groups[k]
		out[i] = GroupSnapshot{
			Key:     k,
			Rows:    g.snapshot(),
			Skipped: g.skipped,
			NaN:     g.nan,
		}
	}
	return out
}

// Package scd 维护缓慢变化维（SCD type-2）每个键的取值历史区间，
// 支持乱序到达的变更事件、整批原子提交与并发只读访问。
package scd

import (
	"io"
	"log"
	"math"
	"os"
	"sync"
)

// OpenEnd 是左闭右开区间的无界右端哨兵时间。
const OpenEnd int64 = math.MaxInt64

// MinEffectiveTime 是允许的最小生效时间。
const MinEffectiveTime int64 = math.MinInt64

// MaxEffectiveTime 是允许的最大生效时间（须小于 OpenEnd，保证区间非空）。
const MaxEffectiveTime int64 = math.MaxInt64 - 1

// Event 表示一条维度变更事件。Deleted 为 true 时表示删除点（不产生区间）。
type Event struct {
	Key           string
	EffectiveTime int64
	Value         string
	Deleted       bool
}

// Interval 表示一个左闭右开 [Start, End) 的历史区间；End==OpenEnd 表示无界。
type Interval struct {
	Key   string
	Start int64
	End   int64
	Value string
}

// Store 增量维护每个键的变更点与历史区间。
type Store struct {
	maxPointsPerKey int

	mu   sync.Mutex
	keys map[string]*keyState

	logMu sync.Mutex
	log   *log.Logger
}

type keyState struct {
	// points 是权威变更点集合，严格按生效时间升序且时间互不相同。
	points []point
	// intervals 由 points 增量派生，按 Start 升序，任意两行不重叠。
	intervals []Interval
}

type point struct {
	at      int64
	value   string
	deleted bool
}

// NewStore 创建一个每键变更点数上限为 maxPointsPerKey 的存储。
func NewStore(maxPointsPerKey int) *Store {
	if maxPointsPerKey <= 0 {
		panic("scd: maxPointsPerKey must be positive")
	}
	return &Store{
		maxPointsPerKey: maxPointsPerKey,
		keys:            map[string]*keyState{},
		log:             log.New(io.Discard, "", 0),
	}
}

// SetLogger 设置步骤日志输出；传 nil 回退到标准错误并带时间前缀。
func (s *Store) SetLogger(w io.Writer) {
	s.logMu.Lock()
	defer s.logMu.Unlock()
	if w == nil {
		w = os.Stderr
	}
	s.log = log.New(w, "scd ", log.LstdFlags|log.Lmicroseconds)
}

func (s *Store) debugf(format string, args ...any) {
	s.logMu.Lock()
	logger := s.log
	s.logMu.Unlock()
	logger.Printf(format, args...)
}

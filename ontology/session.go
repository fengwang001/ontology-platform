package ontology

import (
	"errors"
	"sync"
	"time"
)

// ErrInvalidGap 表示会话间隙阈值不是正数。
var ErrInvalidGap = errors.New("ontology: session gap must be positive")

// ErrEmptyKey 表示事件携带了空键。
var ErrEmptyKey = errors.New("ontology: event key must not be empty")

// ErrTooManyKeys 表示事件引入的新键会使键数量超过容量上限。
var ErrTooManyKeys = errors.New("ontology: number of distinct keys exceeds capacity")

// Event 是一个带键的时间点事件。
type Event struct {
	Key  string
	Time time.Time
}

// Session 表示同一键下由相连事件构成的一个会话。
type Session struct {
	Start time.Time
	End   time.Time
	Count int
}

// SessionSplitter 按活动间隙把每个键的事件切分为会话。
// 同一时间只允许一个 goroutine 写入，查询与自检可并发调用。
type SessionSplitter struct {
	gap      time.Duration
	maxKeys  int
	mu       sync.RWMutex
	keys     map[string]*keyState
	keyOrder []string
}

// keyState 保存单个键的全部事件时间（升序）与增量维护的会话（升序）。
type keyState struct {
	times    []time.Time
	sessions []Session
}

// NewSessionSplitter 创建切分器。gap 为相连判定的间隙阈值，
// maxKeys 为允许的不同键数量上限（<=0 表示不限）。
func NewSessionSplitter(gap time.Duration, maxKeys int) (*SessionSplitter, error) {
	return nil, nil
}

// AddEvents 原子地加入一批事件；任意一个事件非法则整批拒绝、状态不变。
func (s *SessionSplitter) AddEvents(events []Event) error {
	return nil
}

// AddEvent 加入单个事件。
func (s *SessionSplitter) AddEvent(key string, t time.Time) error {
	return nil
}

// Sessions 返回指定键当前的会话副本，按开始时间升序排列。
func (s *SessionSplitter) Sessions(key string) []Session {
	return nil
}

// Snapshot 返回某键的事件时间与会话副本，用于对照验证。
func (s *SessionSplitter) Snapshot(key string) (times []time.Time, sessions []Session) {
	return nil, nil
}

// Keys 按首次出现顺序返回所有非空键。
func (s *SessionSplitter) Keys() []string {
	return nil
}

// Check 自检：对每个键用排序后批量切分重建会话并与增量结果比对。
func (s *SessionSplitter) Check() error {
	return nil
}

// SplitSessions 是无状态的批量切分：时间必须已升序排列。
func SplitSessions(times []time.Time, gap time.Duration) []Session {
	return nil
}

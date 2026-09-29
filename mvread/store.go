// Package mvread 提供多视图的全局一致读：
// 各视图独立推进、各自只保留有限历史版本，
// 读取时返回所有视图在同一时间点上的、彼此一致的快照。
package mvread

import (
	"fmt"
	"sort"
	"sync"
)

// Reason 表示一次操作被拒绝的可区分原因。
type Reason string

const (
	// ReasonInvalidArgument 参数非法（视图未知、时间戳非正、保留数非法等）。
	ReasonInvalidArgument Reason = "invalid_argument"
	// ReasonTimestampNotAdvanced 时间戳没有相对该视图上一次推进严格前进。
	ReasonTimestampNotAdvanced Reason = "timestamp_not_advanced"
	// ReasonNotReady 尚未准备好：时间点超过了各视图进度的最小值。
	ReasonNotReady Reason = "not_ready"
	// ReasonTooOld 太旧：某视图最旧保留版本的时间戳已晚于该时间点。
	ReasonTooOld Reason = "too_old"
)

// Error 描述一次被拒绝的操作及其原因。
type Error struct {
	Reason Reason
	msg    string
}

func (e *Error) Error() string { return string(e.Reason) + ": " + e.msg }

func errf(r Reason, format string, args ...any) *Error {
	return &Error{Reason: r, msg: fmt.Sprintf(format, args...)}
}

// Snapshot 是一次全局一致读的结果：时间点 ts 上各视图的值。
type Snapshot struct {
	TS     int64
	Values map[string]string
}

// version 是一个带时间戳的不可变版本。
type version struct {
	ts    int64
	value string
}

// viewState 是单个视图的进度与有限历史版本环（按时间戳严格递增的切片）。
type viewState struct {
	name     string
	progress int64 // 已应用/心跳到的最大时间戳；0 表示从未推进
	lastData int64 // 最后一次产生版本的时间戳；0 表示尚无版本
	versions []version
}

// Logger 用于打印每一步输入、时间点与判定依据。
type Logger interface {
	Printf(format string, args ...any)
}

// Store 是多视图存储。零值不可用，请使用 New 构造。
// 所有方法均可被多个执行体并发调用；读可与 Apply/Heartbeat 并发。
type Store struct {
	mu     sync.Mutex
	retain int
	views  map[string]*viewState
	order  []string // 视图名的稳定顺序，便于日志与快照可复现
	cursor int64    // 已成功返回过的读取时间点，保证连续读取单调不减
	log    Logger
}

// Option 配置 Store。
type Option func(*Store)

// WithLogger 设置判定日志输出。
func WithLogger(l Logger) Option {
	return func(s *Store) { s.log = l }
}

// New 创建一个多视图存储。retain 为每个视图保留的版本数（>=1）。
func New(views []string, retain int, opts ...Option) (*Store, error) {
	if retain < 1 {
		return nil, errf(ReasonInvalidArgument, "retain must be >= 1, got %d", retain)
	}
	if len(views) == 0 {
		return nil, errf(ReasonInvalidArgument, "at least one view is required")
	}
	seen := make(map[string]struct{}, len(views))
	names := make([]string, 0, len(views))
	for _, v := range views {
		if v == "" {
			return nil, errf(ReasonInvalidArgument, "view name must not be empty")
		}
		if _, dup := seen[v]; dup {
			return nil, errf(ReasonInvalidArgument, "duplicate view %q", v)
		}
		seen[v] = struct{}{}
		names = append(names, v)
	}
	sort.Strings(names)
	s := &Store{
		retain: retain,
		views:  make(map[string]*viewState, len(names)),
		order:  names,
		log:    nopLogger{},
	}
	for _, n := range names {
		s.views[n] = &viewState{name: n}
	}
	for _, o := range opts {
		o(s)
	}
	s.log.Printf("store init: views=%v retain=%d", names, retain)
	return s, nil
}

// Apply 在某视图上写入时间戳为 ts 的新版本。
func (s *Store) Apply(view string, ts int64, value string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, err := s.checkAdvance(view, ts, "apply", value)
	if err != nil {
		s.log.Printf("apply REJECT view=%q ts=%d reason=%s detail=%q", view, ts, err.Reason, err.msg)
		return err
	}
	v.versions = append(v.versions, version{ts: ts, value: value})
	if drop := len(v.versions) - s.retain; drop > 0 {
		// 只保留最近 retain 个版本；更旧的永久删除、不可读。
		v.versions = append([]version(nil), v.versions[drop:]...)
	}
	v.lastData = ts
	v.progress = ts
	s.log.Printf("apply OK view=%q ts=%d kept=%d oldest=%d progress=%d",
		view, ts, len(v.versions), v.versions[0].ts, v.progress)
	return nil
}

// Heartbeat 只推进某视图的进度（存活心跳），不产生新版本。
func (s *Store) Heartbeat(view string, ts int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, err := s.checkAdvance(view, ts, "heartbeat", "")
	if err != nil {
		s.log.Printf("heartbeat REJECT view=%q ts=%d reason=%s detail=%q", view, ts, err.Reason, err.msg)
		return err
	}
	v.progress = ts
	s.log.Printf("heartbeat OK view=%q ts=%d versions=%d progress=%d",
		view, ts, len(v.versions), v.progress)
	return nil
}

// checkAdvance 校验 view 已知、ts 为正且严格大于该视图当前进度。
// 任何失败都在调用方落日志前返回，不触碰任何状态（失败不留痕）。
func (s *Store) checkAdvance(view string, ts int64, op, value string) (*viewState, *Error) {
	v, ok := s.views[view]
	if !ok {
		return nil, errf(ReasonInvalidArgument, "unknown view %q", view)
	}
	if ts <= 0 {
		return nil, errf(ReasonInvalidArgument, "%s requires positive timestamp, got %d", op, ts)
	}
	if ts <= v.progress {
		return nil, errf(ReasonTimestampNotAdvanced,
			"%s view=%q ts=%d must be > progress=%d", op, view, ts, v.progress)
	}
	return v, nil
}

// ReadAt 在指定时间点读取各视图一致快照。
//
// 判定顺序：
//  1. 参数合法（ts 为正）；
//  2. 不低于上一次成功读取的时间点（连续读取单调不减）；
//  3. 不超过各视图进度的最小值（否则 NotReady）；
//  4. 不存在“最旧保留版本晚于 ts”的视图（否则 TooOld）；
//
// 两关（进度关、保留关）都过才返回各视图在 ts 点的值。
func (s *Store) ReadAt(ts int64) (*Snapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if ts <= 0 {
		err := errf(ReasonInvalidArgument, "read requires positive timestamp, got %d", ts)
		s.log.Printf("read REJECT ts=%d reason=%s detail=%q", ts, err.Reason, err.msg)
		return nil, err
	}
	return s.readLocked(ts)
}

// readLocked 在已持锁的情况下执行两关判定并产生快照。
func (s *Store) readLocked(ts int64) (*Snapshot, error) {
	if ts < s.cursor {
		err := errf(ReasonTimestampNotAdvanced,
			"read ts=%d is before last served ts=%d", ts, s.cursor)
		s.log.Printf("read REJECT ts=%d reason=%s detail=%q", ts, err.Reason, err.msg)
		return nil, err
	}

	// 第一关：全局进度最小值。
	minProgress := int64(-1)
	for _, name := range s.order {
		p := s.views[name].progress
		if minProgress < 0 || p < minProgress {
			minProgress = p
		}
	}
	if minProgress == 0 || ts > minProgress {
		err := errf(ReasonNotReady,
			"ts=%d exceeds min progress=%d across views=%v", ts, minProgress, s.progressList())
		s.log.Printf("read REJECT ts=%d reason=%s detail=%q", ts, err.Reason, err.msg)
		return nil, err
	}

	// 第二关：各视图最旧保留版本不得晚于 ts。
	for _, name := range s.order {
		v := s.views[name]
		// 进度可能只由心跳推进。ts <= minProgress <= v.progress，
		// 若该视图从无版本，则该时间点没有任何数据可读。
		if len(v.versions) == 0 {
			err := errf(ReasonTooOld, "view=%q has no retained version at ts=%d", name, ts)
			s.log.Printf("read REJECT ts=%d reason=%s detail=%q", ts, err.Reason, err.msg)
			return nil, err
		}
		if oldest := v.versions[0].ts; oldest > ts {
			err := errf(ReasonTooOld,
				"view=%q oldest retained ts=%d > requested ts=%d", name, oldest, ts)
			s.log.Printf("read REJECT ts=%d reason=%s detail=%q", ts, err.Reason, err.msg)
			return nil, err
		}
	}

	values := make(map[string]string, len(s.order))
	for _, name := range s.order {
		values[name] = s.views[name].valueAt(ts)
	}
	s.cursor = ts
	s.log.Printf("read OK ts=%d minProgress=%d snapshot=%v cursor=%d", ts, minProgress, values, s.cursor)
	return &Snapshot{TS: ts, Values: values}, nil
}

// Read 读取“最新的全局一致时间点”（各视图进度的最小值）。
func (s *Store) Read() (*Snapshot, error) {
	s.mu.Lock()
	minProgress := s.minProgressLocked()
	s.mu.Unlock()
	return s.ReadAt(minProgress)
}

func (s *Store) minProgressLocked() int64 {
	min := int64(-1)
	for _, name := range s.order {
		p := s.views[name].progress
		if min < 0 || p < min {
			min = p
		}
	}
	return min
}

func (s *Store) progressList() []int64 {
	out := make([]int64, 0, len(s.order))
	for _, name := range s.order {
		out = append(out, s.views[name].progress)
	}
	return out
}

// valueAt 返回时间戳不超过 ts 的最大版本的值。
// 调用前必须保证至少存在一个 ts' <= ts 的保留版本。
func (v *viewState) valueAt(ts int64) string {
	lo, hi := 0, len(v.versions)
	for lo < hi {
		mid := int(uint(lo+hi) >> 1)
		if v.versions[mid].ts <= ts {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	return v.versions[lo-1].value
}

// Progress 返回某视图当前进度（主要用于测试与可观测）。
func (s *Store) Progress(view string) (int64, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.views[view]
	if !ok {
		return 0, false
	}
	return v.progress, true
}

// RetainedCount 返回某视图当前保留的版本数。
func (s *Store) RetainedCount(view string) (int, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.views[view]
	if !ok {
		return 0, false
	}
	return len(v.versions), true
}

type nopLogger struct{}

func (nopLogger) Printf(string, ...any) {}

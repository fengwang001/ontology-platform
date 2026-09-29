// Package consistency 提供多视图（view）的全局一致读。
//
// 每个视图各自推进进度并只保留有限历史版本；读取只能发生在所有视图
// 都已经应用到的时间点，且该点对每个视图都仍落在保留的历史窗口内。
package consistency

import (
	"context"
	"fmt"
	"math"
	"sort"
	"sync"
	"sync/atomic"
)

// Timestamp 是单调前进的逻辑时间戳。
type Timestamp = int64

// Value 是视图在某个时间点的值。
type Value = any

type version struct {
	ts    Timestamp
	value Value
}

type viewState struct {
	progress Timestamp
	versions []version
}

// 失败原因互不相同，调用方可通过 errors.Is 区分。
var (
	// ErrInvalidArgument：参数非法（未知视图、nil 值、负数时间戳等）。
	ErrInvalidArgument = invalidArgumentError{}
	// ErrTimestampNotAdvancing：应用或心跳的时间戳没有严格前进。
	ErrTimestampNotAdvancing = timestampNotAdvancingError{}
	// ErrNotReady：尚未准备好（有视图尚未产生任何版本，或时间点超过各视图进度最小值）。
	ErrNotReady = notReadyError{}
	// ErrTooOld：太旧（存在视图其最旧保留版本仍晚于该时间点）。
	ErrTooOld = tooOldError{}
)

// ViewSnapshot 是单个视图在某个时间点的取值。
type ViewSnapshot struct {
	View      string
	At        Timestamp
	VersionTs Timestamp
	Value     Value
}

// Snapshot 是一次全局一致读的结果，对应一个所有视图都已应用的时间点。
type Snapshot struct {
	At    Timestamp
	Views []ViewSnapshot
}

// Store 是多视图版本存储。
type Store struct {
	mu     sync.RWMutex
	retain int
	order  []string
	views  map[string]*viewState

	// last 是历史返回过的最大一致时间点，保证连续读取单调不减。
	last atomic.Int64

	logf func(format string, args ...any)
}

// NewStore 创建一个保留各视图最近 retain 个版本的存储。
func NewStore(retain int, views ...string) (*Store, error) {
	if retain < 1 {
		return nil, fmt.Errorf("consistency: retain must be >= 1, got %d: %w", retain, ErrInvalidArgument)
	}
	if len(views) == 0 {
		return nil, fmt.Errorf("consistency: at least one view required: %w", ErrInvalidArgument)
	}
	seen := make(map[string]struct{}, len(views))
	for _, name := range views {
		if name == "" {
			return nil, fmt.Errorf("consistency: view name must not be empty: %w", ErrInvalidArgument)
		}
		if _, dup := seen[name]; dup {
			return nil, fmt.Errorf("consistency: duplicate view %q: %w", name, ErrInvalidArgument)
		}
		seen[name] = struct{}{}
	}
	vm := make(map[string]*viewState, len(views))
	for _, name := range views {
		vm[name] = &viewState{progress: -1}
	}
	return &Store{retain: retain, order: append([]string(nil), views...), views: vm, logf: func(string, ...any) {}}, nil
}

// SetLogger 注入步骤日志（如 log.Printf 或 testing.T.Logf）。
func (s *Store) SetLogger(logf func(format string, args ...any)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if logf == nil {
		s.logf = func(string, ...any) {}
		return
	}
	s.logf = logf
}

// Apply 在视图 view 上应用一个版本，并把该视图进度推进到 ts。
func (s *Store) Apply(ctx context.Context, view string, ts Timestamp, value Value) error {
	if err := ctxErr(ctx); err != nil {
		return err
	}
	if view == "" || value == nil || ts < 0 {
		return fmt.Errorf("consistency: apply view=%q ts=%d: %w", view, ts, ErrInvalidArgument)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	vs, ok := s.views[view]
	if !ok {
		return fmt.Errorf("consistency: apply unknown view %q: %w", view, ErrInvalidArgument)
	}
	if ts <= vs.progress {
		s.logf("APPLY  reject view=%s ts=%d progress=%d reason=timestamp-not-advancing", view, ts, vs.progress)
		return fmt.Errorf("consistency: apply view %q ts %d <= progress %d: %w", view, ts, vs.progress, ErrTimestampNotAdvancing)
	}

	vs.versions = append(vs.versions, version{ts: ts, value: value})
	if len(vs.versions) > s.retain {
		drop := len(vs.versions) - s.retain
		copy(vs.versions, vs.versions[drop:])
		for i := s.retain; i < len(vs.versions); i++ {
			vs.versions[i] = version{}
		}
		vs.versions = vs.versions[:s.retain]
	}
	vs.progress = ts
	s.logf("APPLY  ok view=%s ts=%d kept=%d progress=%d", view, ts, len(vs.versions), vs.progress)
	return nil
}

// Heartbeat 只把视图 view 的进度推进到 ts，不产生新版本。
func (s *Store) Heartbeat(ctx context.Context, view string, ts Timestamp) error {
	if err := ctxErr(ctx); err != nil {
		return err
	}
	if view == "" || ts < 0 {
		return fmt.Errorf("consistency: heartbeat view=%q ts=%d: %w", view, ts, ErrInvalidArgument)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	vs, ok := s.views[view]
	if !ok {
		return fmt.Errorf("consistency: heartbeat unknown view %q: %w", view, ErrInvalidArgument)
	}
	if ts <= vs.progress {
		s.logf("HEARTB reject view=%s ts=%d progress=%d reason=timestamp-not-advancing", view, ts, vs.progress)
		return fmt.Errorf("consistency: heartbeat view %q ts %d <= progress %d: %w", view, ts, vs.progress, ErrTimestampNotAdvancing)
	}
	vs.progress = ts
	s.logf("HEARTB ok view=%s ts=%d versions=%d progress=%d", view, ts, len(vs.versions), vs.progress)
	return nil
}

// ReadAt 读取所有视图在时间点 at 的一致快照。
//
// 判定顺序固定：先参数，再过“进度关”（所有视图已有版本且 at 不超过
// 各视图进度最小值），最后过“保留关”（每个视图最旧保留版本不晚于 at）。
// 任何一关失败都不会改变进度、版本或保留情况。
func (s *Store) ReadAt(ctx context.Context, at Timestamp) (*Snapshot, error) {
	if err := ctxErr(ctx); err != nil {
		return nil, err
	}
	if at < 0 {
		return nil, fmt.Errorf("consistency: read at=%d: %w", at, ErrInvalidArgument)
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	minProgress, err := s.gateProgress(at, "READ-AT")
	if err != nil {
		return nil, err
	}
	if err := s.gateRetention(at, minProgress, "READ-AT"); err != nil {
		return nil, err
	}
	snap := s.buildSnapshot(at)
	s.logf("READ-AT ok at=%d minProgress=%d views=%d", at, minProgress, len(snap.Views))
	s.advance(at)
	return snap, nil
}

// Read 返回一个时间点不小于历史返回点的一致快照；当共享的可读窗口
// [各视图最旧版本的最大值, 各视图进度的最小值] 仍覆盖游标时可读。
func (s *Store) Read(ctx context.Context) (*Snapshot, error) {
	if err := ctxErr(ctx); err != nil {
		return nil, err
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	minProgress, err := s.gateProgress(-1, "READ  ")
	if err != nil {
		return nil, err
	}

	oldest := s.maxOldest()
	// 取最新的一致时间点；进度最小值只增不减，故天然不小于历史游标。
	at := minProgress
	if cur := s.last.Load(); cur > at {
		at = cur
	}
	if at < oldest {
		s.logf("READ   reject chosenAt=%d minProgress=%d oldest=%d reason=too-old", at, minProgress, oldest)
		return nil, fmt.Errorf("consistency: freshest point %d older than retained window bottom %d: %w", at, oldest, ErrTooOld)
	}
	snap := s.buildSnapshot(at)
	s.logf("READ   ok at=%d prevCursor=%d oldest=%d minProgress=%d views=%d", at, s.last.Load(), oldest, minProgress, len(snap.Views))
	s.advance(at)
	return snap, nil
}

// Progress 返回视图当前进度（从未推进过时返回 -1, false 仅在视图不存在时）。
func (s *Store) Progress(view string) (Timestamp, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	vs, ok := s.views[view]
	if !ok {
		return 0, false
	}
	return vs.progress, true
}

// Oldest 返回视图当前最旧保留版本的时间戳；视图存在但尚无版本时 ok 为 false。
func (s *Store) Oldest(view string) (Timestamp, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	vs, ok := s.views[view]
	if !ok || len(vs.versions) == 0 {
		return 0, false
	}
	return vs.versions[0].ts, true
}

// MinProgress 返回所有视图进度的最小值；任一视图尚无版本时 ok 为 false。
func (s *Store) MinProgress() (Timestamp, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.minProgressLocked()
}

// gateProgress 是第一关：所有视图必须已有版本，且 at 不超过进度最小值。
func (s *Store) gateProgress(at Timestamp, tag string) (Timestamp, error) {
	minProgress, ok := s.minProgressLocked()
	if !ok {
		s.logf("%s reject at=%d reason=not-ready detail=a-view-has-no-version", tag, at)
		return 0, fmt.Errorf("consistency: some view has no applied version yet: %w", ErrNotReady)
	}
	if at > minProgress {
		s.logf("%s reject at=%d minProgress=%d reason=not-ready detail=past-min-progress", tag, at, minProgress)
		return 0, fmt.Errorf("consistency: %d > min progress %d: %w", at, minProgress, ErrNotReady)
	}
	return minProgress, nil
}

// gateRetention 是第二关：每个视图最旧保留版本都不得晚于 at。
func (s *Store) gateRetention(at, minProgress Timestamp, tag string) error {
	for _, name := range s.order {
		oldest := s.views[name].versions[0].ts
		if oldest > at {
			s.logf("%s reject at=%d minProgress=%d view=%s oldest=%d reason=too-old", tag, at, minProgress, name, oldest)
			return fmt.Errorf("consistency: view %q oldest kept version %d > %d: %w", name, oldest, at, ErrTooOld)
		}
	}
	return nil
}

// buildSnapshot 取每个视图时间戳不超过 at 的最大版本；调用方须持有读锁。
func (s *Store) buildSnapshot(at Timestamp) *Snapshot {
	views := make([]ViewSnapshot, 0, len(s.order))
	for _, name := range s.order {
		versions := s.views[name].versions
		idx := sort.Search(len(versions), func(i int) bool { return versions[i].ts > at }) - 1
		views = append(views, ViewSnapshot{
			View:      name,
			At:        at,
			VersionTs: versions[idx].ts,
			Value:     versions[idx].value,
		})
	}
	return &Snapshot{At: at, Views: views}
}

func (s *Store) minProgressLocked() (Timestamp, bool) {
	minProgress := Timestamp(math.MaxInt64)
	for _, name := range s.order {
		vs := s.views[name]
		if len(vs.versions) == 0 {
			return 0, false
		}
		if vs.progress < minProgress {
			minProgress = vs.progress
		}
	}
	return minProgress, true
}

// maxOldest 返回各视图最旧保留版本时间戳的最大值，即可读窗口下界；
// 调用方须已通过 minProgressLocked（即所有视图至少有一个版本）。
func (s *Store) maxOldest() Timestamp {
	var oldest Timestamp = math.MinInt64
	for _, name := range s.order {
		if ts := s.views[name].versions[0].ts; ts > oldest {
			oldest = ts
		}
	}
	return oldest
}

// advance 把单调游标推进到 at（不回退），允许多个读取并发调用。
func (s *Store) advance(at Timestamp) {
	for {
		cur := s.last.Load()
		if at <= cur || s.last.CompareAndSwap(cur, at) {
			return
		}
	}
}

func ctxErr(ctx context.Context) error {
	if ctx == nil {
		return nil
	}
	return ctx.Err()
}

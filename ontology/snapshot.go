package ontology

import (
	"fmt"
	"sort"
	"sync"
)

// Timestamp 逻辑时间戳，按视图严格递增。
type Timestamp int64

// Version 单个视图在某个时间点写入的值。
type Version struct {
	TS    Timestamp
	Value any
}

// ViewConfig 视图配置：名称与保留的最近版本个数。
type ViewConfig struct {
	Name      string
	Retention int
}

// Snapshot 一次全局一致读的结果：时间点与各视图在该点的值。
type Snapshot struct {
	TS     Timestamp
	Values map[string]any
}

// viewState 单个视图的内部状态：进度、按时间戳严格递增的版本序列、保留窗口。
type viewState struct {
	retention int
	progress  Timestamp
	versions  []Version
}

// valueAt 返回时间点 ts 的值：时间戳不超过 ts 的最大版本。
func (v *viewState) valueAt(ts Timestamp) (any, bool) {
	i := sort.Search(len(v.versions), func(i int) bool { return v.versions[i].TS > ts })
	if i == 0 {
		return nil, false
	}
	return v.versions[i-1].Value, true
}

func (v *viewState) oldestTS() Timestamp {
	if len(v.versions) == 0 {
		return 0
	}
	return v.versions[0].TS
}

// SnapshotStore 多视图全局一致读存储，可被多个执行体并发使用。
//
// 读取规则：时间点 ts 必须为正、不早于上一次成功读取的时间点、
// 不超过各视图进度的最小值，且每个视图最旧保留版本不晚于 ts；
// 全部满足时返回各视图在 ts 的值。任何被拒绝的调用都不改变
// 进度、版本或保留情况，失败不留痕。
type SnapshotStore struct {
	mu       sync.RWMutex
	views    map[string]*viewState
	names    []string // 排序后的视图名，保证判定顺序确定
	lastRead Timestamp
}

// NewSnapshotStore 按配置创建存储；配置为空、视图名为空、重名或保留窗口小于 1 时返回 ErrInvalidArgument。
func NewSnapshotStore(configs []ViewConfig) (*SnapshotStore, error) {
	if len(configs) == 0 {
		return nil, fmt.Errorf("%w: at least one view is required", ErrInvalidArgument)
	}
	s := &SnapshotStore{views: make(map[string]*viewState, len(configs))}
	for _, c := range configs {
		if c.Name == "" {
			return nil, fmt.Errorf("%w: empty view name", ErrInvalidArgument)
		}
		if c.Retention < 1 {
			return nil, fmt.Errorf("%w: view %q retention must be >= 1", ErrInvalidArgument, c.Name)
		}
		if _, dup := s.views[c.Name]; dup {
			return nil, fmt.Errorf("%w: duplicate view %q", ErrInvalidArgument, c.Name)
		}
		s.views[c.Name] = &viewState{retention: c.Retention}
		s.names = append(s.names, c.Name)
	}
	sort.Strings(s.names)
	return s, nil
}

// Apply 在视图上应用一个新版本：要求时间戳为正且严格大于当前进度，
// 成功后写入版本、推进进度，并按保留窗口淘汰最旧版本。
// 任何校验失败都不改变存储状态。
func (s *SnapshotStore) Apply(name string, ts Timestamp, value any) error {
	if value == nil {
		return fmt.Errorf("%w: nil value", ErrInvalidArgument)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.views[name]
	if !ok {
		return fmt.Errorf("%w: unknown view %q", ErrInvalidArgument, name)
	}
	if ts <= 0 {
		return fmt.Errorf("%w: timestamp must be positive, got %d", ErrInvalidArgument, ts)
	}
	if ts <= v.progress {
		return fmt.Errorf("%w: apply view %q at %d, progress already %d", ErrNonMonotonicTimestamp, name, ts, v.progress)
	}
	v.versions = append(v.versions, Version{TS: ts, Value: value})
	v.progress = ts
	if len(v.versions) > v.retention {
		v.versions = append([]Version(nil), v.versions[len(v.versions)-v.retention:]...)
	}
	return nil
}

// Heartbeat 推进视图进度但不写入新版本：要求时间戳为正且严格大于当前进度。
func (s *SnapshotStore) Heartbeat(name string, ts Timestamp) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.views[name]
	if !ok {
		return fmt.Errorf("%w: unknown view %q", ErrInvalidArgument, name)
	}
	if ts <= 0 {
		return fmt.Errorf("%w: timestamp must be positive, got %d", ErrInvalidArgument, ts)
	}
	if ts <= v.progress {
		return fmt.Errorf("%w: heartbeat view %q at %d, progress already %d", ErrNonMonotonicTimestamp, name, ts, v.progress)
	}
	v.progress = ts
	return nil
}

// Read 在指定时间点做全局一致读。
//
// 判定顺序：
//  1. 时间戳必须为正，且不早于上一次成功读取的时间点（读取时间点单调不减）；
//  2. 时间点不得超过各视图进度的最小值，否则 ErrNotReady；
//  3. 每个视图最旧保留版本不得晚于该时间点，否则 ErrTooOld。
//
// 两关都过才返回各视图在该点的值；被拒的读取不改变任何状态。
func (s *SnapshotStore) Read(ts Timestamp) (Snapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if ts <= 0 {
		return Snapshot{}, fmt.Errorf("%w: timestamp must be positive, got %d", ErrInvalidArgument, ts)
	}
	if ts < s.lastRead {
		return Snapshot{}, fmt.Errorf("%w: read at %d, last served read at %d", ErrNonMonotonicTimestamp, ts, s.lastRead)
	}
	minProgress := s.minProgressLocked()
	if ts > minProgress {
		return Snapshot{}, fmt.Errorf("%w: read at %d, minimum progress is %d", ErrNotReady, ts, minProgress)
	}
	values := make(map[string]any, len(s.views))
	for _, name := range s.names {
		v := s.views[name]
		if len(v.versions) == 0 || v.versions[0].TS > ts {
			return Snapshot{}, fmt.Errorf("%w: view %q oldest retained version is %d, read at %d", ErrTooOld, name, v.oldestTS(), ts)
		}
		value, _ := v.valueAt(ts)
		values[name] = value
	}
	s.lastRead = ts
	return Snapshot{TS: ts, Values: values}, nil
}

// MinProgress 返回各视图进度的最小值。
func (s *SnapshotStore) MinProgress() Timestamp {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.minProgressLocked()
}

func (s *SnapshotStore) minProgressLocked() Timestamp {
	min := s.views[s.names[0]].progress
	for _, name := range s.names[1:] {
		if p := s.views[name].progress; p < min {
			min = p
		}
	}
	return min
}

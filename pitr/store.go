package pitr

import (
	"context"
	"fmt"
	"io"
	"sort"
	"sync"
)

// Store is the concurrency-safe registry of timelines, archived log
// segments and base backups. All methods may be called concurrently.
type Store struct {
	mu        sync.RWMutex
	logger    io.Writer
	timelines map[TimelineID]Timeline
	segments  map[TimelineID][]Segment
	backups   map[TimelineID][]Backup
}

// NewStore creates an empty store whose root timeline (id 1) is registered.
func NewStore(logger io.Writer) *Store {
	if logger == nil {
		logger = io.Discard
	}
	return &Store{
		logger: logger,
		timelines: map[TimelineID]Timeline{
			InitialTimeline: {ID: InitialTimeline, Parent: 0, Fork: 0},
		},
		segments: map[TimelineID][]Segment{},
		backups:  map[TimelineID][]Backup{},
	}
}

func (s *Store) logf(format string, args ...any) {
	fmt.Fprintf(s.logger, format+"\n", args...)
}

// RegisterTimeline registers an existing timeline (e.g. loaded from history).
func (s *Store) RegisterTimeline(ctx context.Context, t Timeline) error {
	_ = ctx
	s.mu.Lock()
	defer s.mu.Unlock()
	s.logf("输入 RegisterTimeline: id=%d parent=%d fork=%d", t.ID, t.Parent, t.Fork)
	if t.ID <= 0 {
		err := fmt.Errorf("pitr: invalid timeline id %d", t.ID)
		s.logf("输出 RegisterTimeline: 拒绝 判定依据=%v", err)
		return err
	}
	if _, exists := s.timelines[t.ID]; exists {
		s.logf("输出 RegisterTimeline: 拒绝 判定依据=%v (id=%d 已登记)", ErrTimelineExists, t.ID)
		return ErrTimelineExists
	}
	parent, ok := s.timelines[t.Parent]
	if !ok {
		s.logf("输出 RegisterTimeline: 拒绝 判定依据=%v (parent=%d 不存在)", ErrParentNotFound, t.Parent)
		return ErrParentNotFound
	}
	if t.Fork < parent.Fork {
		s.logf("输出 RegisterTimeline: 拒绝 判定依据=%v (fork=%d 早于父时间线 %d 的 fork=%d)",
			ErrForkBeforeParentFork, t.Fork, parent.ID, parent.Fork)
		return ErrForkBeforeParentFork
	}
	s.timelines[t.ID] = t
	s.segments[t.ID] = nil
	s.backups[t.ID] = nil
	s.logf("输出 RegisterTimeline: 成功 判定依据=父 %d 存在且 fork=%d >= 父 fork=%d",
		t.Parent, t.Fork, parent.Fork)
	return nil
}

// AddSegment archives a log segment.
func (s *Store) AddSegment(ctx context.Context, seg Segment) error {
	_ = ctx
	s.mu.Lock()
	defer s.mu.Unlock()
	s.logf("输入 AddSegment: timeline=%d [%d,%d)", seg.Timeline, seg.Start, seg.End)
	if seg.Start < 0 || seg.End <= seg.Start {
		s.logf("输出 AddSegment: 拒绝 判定依据=%v (start=%d end=%d)", ErrInvalidSegment, seg.Start, seg.End)
		return ErrInvalidSegment
	}
	if _, ok := s.timelines[seg.Timeline]; !ok {
		s.logf("输出 AddSegment: 拒绝 判定依据=%v (timeline=%d 不存在)", ErrTimelineNotFound, seg.Timeline)
		return ErrTimelineNotFound
	}
	s.segments[seg.Timeline] = append(s.segments[seg.Timeline], seg)
	sort.Slice(s.segments[seg.Timeline], func(i, j int) bool {
		a, b := s.segments[seg.Timeline][i], s.segments[seg.Timeline][j]
		if a.Start != b.Start {
			return a.Start < b.Start
		}
		return a.End < b.End
	})
	s.logf("输出 AddSegment: 成功 判定依据=区间合法且时间线 %d 已登记", seg.Timeline)
	return nil
}

// AddBackup records a base backup.
func (s *Store) AddBackup(ctx context.Context, b Backup) error {
	_ = ctx
	s.mu.Lock()
	defer s.mu.Unlock()
	s.logf("输入 AddBackup: timeline=%d end=%d", b.Timeline, b.End)
	if b.End < 0 {
		s.logf("输出 AddBackup: 拒绝 判定依据=%v (end=%d)", ErrInvalidBackup, b.End)
		return ErrInvalidBackup
	}
	if _, ok := s.timelines[b.Timeline]; !ok {
		s.logf("输出 AddBackup: 拒绝 判定依据=%v (timeline=%d 不存在)", ErrTimelineNotFound, b.Timeline)
		return ErrTimelineNotFound
	}
	s.backups[b.Timeline] = append(s.backups[b.Timeline], b)
	sort.Slice(s.backups[b.Timeline], func(i, j int) bool {
		a, b2 := s.backups[b.Timeline][i], s.backups[b.Timeline][j]
		if a.End != b2.End {
			return a.End < b2.End
		}
		return a.Timeline > b2.Timeline
	})
	s.logf("输出 AddBackup: 成功 判定依据=上界合法且时间线 %d 已登记", b.Timeline)
	return nil
}

// maxTimelineID returns the greatest registered timeline id.
func (s *Store) maxTimelineID() TimelineID {
	max := InitialTimeline
	for id := range s.timelines {
		if id > max {
			max = id
		}
	}
	return max
}

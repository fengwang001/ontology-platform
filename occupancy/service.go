package occupancy

import (
	"fmt"
	"sync"
)

// Service 是占道施工许可审查服务。所有方法可并发调用，
// 内部以单一互斥锁串行化，结果等价于某个全局串行顺序。
type Service struct {
	mu sync.Mutex

	lanes     map[string]int
	corr      map[string]string
	detour    map[string][]string
	revDetour map[string][]string
	corrCap   map[string]int

	indices map[string]*roadIndex
	permits map[int64]*Permit

	clock  int64
	nextID int64
	seq    int64

	events  []Event
	history []StatusChange
	logger  func(string)
}

// NewService 用静态路网配置构造服务；配置非法返回参数错误。
func NewService(cfg Config) (*Service, CodeError) {
	s := &Service{
		lanes:     map[string]int{},
		corr:      map[string]string{},
		detour:    map[string][]string{},
		revDetour: map[string][]string{},
		corrCap:   map[string]int{},
		indices:   map[string]*roadIndex{},
		permits:   map[int64]*Permit{},
	}
	if len(cfg.Roads) == 0 {
		return nil, fail(ErrInvalidParams, "no roads configured")
	}
	for _, r := range cfg.Roads {
		if r.ID == "" {
			return nil, fail(ErrInvalidParams, "empty road id")
		}
		if _, dup := s.lanes[r.ID]; dup {
			return nil, fail(ErrInvalidParams, "duplicate road: "+r.ID)
		}
		if r.Lanes <= 0 {
			return nil, fail(ErrInvalidParams, fmt.Sprintf("road %s lanes must be positive", r.ID))
		}
		if r.Corridor == "" {
			return nil, fail(ErrInvalidParams, fmt.Sprintf("road %s missing corridor", r.ID))
		}
		s.lanes[r.ID] = r.Lanes
		s.corr[r.ID] = r.Corridor
		s.detour[r.ID] = append([]string(nil), r.Detour...)
		s.indices[r.ID] = newRoadIndex()
	}
	for _, r := range cfg.Roads {
		seen := map[string]bool{}
		for _, d := range r.Detour {
			if _, ok := s.lanes[d]; !ok {
				return nil, fail(ErrInvalidParams, fmt.Sprintf("road %s detour references unknown road %s", r.ID, d))
			}
			if d == r.ID {
				return nil, fail(ErrInvalidParams, fmt.Sprintf("road %s detour must consist of other roads", r.ID))
			}
			if seen[d] {
				return nil, fail(ErrInvalidParams, fmt.Sprintf("road %s detour repeats %s", r.ID, d))
			}
			seen[d] = true
			s.revDetour[d] = append(s.revDetour[d], r.ID)
		}
	}
	for c, n := range cfg.CorridorCap {
		if n <= 0 {
			return nil, fail(ErrInvalidParams, fmt.Sprintf("corridor %s cap must be positive", c))
		}
		s.corrCap[c] = n
	}
	// 配置了路段但未配置上限的走廊视为不设上限（-1）。
	for _, c := range s.corr {
		if _, ok := s.corrCap[c]; !ok {
			s.corrCap[c] = -1
		}
	}
	return s, CodeError{}
}

func (s *Service) nextSeq() int64 {
	s.seq++
	return s.seq
}

func (s *Service) logf(format string, args ...interface{}) {
	if s.logger != nil {
		s.logger(fmt.Sprintf(format, args...))
	}
}

func (s *Service) recordChange(p *Permit, after Status, iv Interval, opSeq, opTime int64, note string) {
	s.history = append(s.history, StatusChange{
		Seq: opSeq, OpTime: opTime, PermitID: p.ID,
		Before: p.Status, After: after, Interval: iv, Note: note,
	})
	p.Status = after
}

func (s *Service) makePiece(p *Permit, iv Interval, key int64) piece {
	return piece{
		key:      key,
		permitID: p.ID,
		road:     p.Road,
		start:    iv.Start,
		end:      iv.End,
		lanes:    p.Lanes,
		full:     p.Lanes == s.lanes[p.Road],
		corr:     s.corr[p.Road],
		priority: p.Priority,
		approved: p.Status == Approved,
	}
}

// SetLogger 安装判定日志回调，每步判定写入一行可读文本。
func (s *Service) SetLogger(f func(string)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.logger = f
}

// Events 返回操作日志的完整副本。
func (s *Service) Events() []Event {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Event(nil), s.events...)
}

// History 返回许可状态变迁历史的完整副本。
func (s *Service) History() []StatusChange {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]StatusChange(nil), s.history...)
}

// Permit 返回一份许可的当前快照。
func (s *Service) Permit(id int64) (Permit, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.permits[id]
	if !ok {
		return Permit{}, false
	}
	return *p, true
}

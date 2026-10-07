package lifecycle

import (
	"errors"
	"fmt"
	"sync"
	"time"
)

// ErrDuplicateObject 不属于四类互斥业务错误，仅用于建对象/建边的前置冲突。
var ErrDuplicateObject = errors.New("lifecycle: object already exists")

type record struct {
	id             string
	state          State
	attrs          Attrs
	graceDeadline  time.Time
	freezeDeadline time.Time
}

// Service 是逻辑删除与可见性服务。
type Service struct {
	mu      sync.Mutex
	clock   Clock
	audit   AuditLog
	logger  Logger
	seq     int
	objects map[string]*record
	edges   map[string][]Edge
}

func NewService(clock Clock, audit AuditLog, logger Logger) *Service {
	return &Service{
		clock:   clock,
		audit:   audit,
		logger:  logger,
		objects: make(map[string]*record),
		edges:   make(map[string][]Edge),
	}
}

// settleLocked 执行惰性到期结算，调用方须持有 mu。
//
// 规则（宽限与冻结语义对称）：
//   - 宽限截止时刻恰好到达或已过（now >= deadline）→ 已归档；
//   - 冻结截止时刻恰好到达或已过（now >= deadline）→ 已归档。
//
// 该转换由系统在下一次涉及该对象的任何操作中完成，不消耗调用方
// 额外请求；撤销等被拒绝时它仍照常发生。triggerKind 为 "archive"
// 时表示由调用方的显式归档请求在到期时刻触发，审计记为调用方
// 归档；其余情形记为系统 auto-archive。
func (s *Service) settleLocked(r *record, now time.Time, triggerKind string, actor Identity) {
	switch r.state {
	case StateGrace:
		if !r.graceDeadline.After(now) {
			s.archiveLocked(r, now, triggerKind, actor)
		}
	case StateFrozen:
		if !r.freezeDeadline.After(now) {
			s.archiveLocked(r, now, triggerKind, actor)
		}
	}
}

func (s *Service) archiveLocked(r *record, now time.Time, kind string, actor Identity) {
	from := r.state
	r.state = StateArchived
	r.graceDeadline = time.Time{}
	r.freezeDeadline = time.Time{}
	s.seq++
	if s.audit != nil {
		s.audit.Append(Transition{
			Seq: s.seq, ObjectID: r.id, Time: now,
			From: from, To: StateArchived, Kind: kind, Actor: actor,
		})
	}
}

func (s *Service) transitionLocked(r *record, now time.Time, to State, kind string, actor Identity, deadline time.Time) {
	from := r.state
	r.state = to
	if to == StateGrace {
		r.graceDeadline = deadline
	}
	if to == StateAlive {
		r.graceDeadline = time.Time{}
	}
	if to == StateFrozen {
		r.freezeDeadline = deadline
	}
	s.seq++
	if s.audit != nil {
		s.audit.Append(Transition{
			Seq: s.seq, ObjectID: r.id, Time: now,
			From: from, To: to, Kind: kind, Deadline: deadline, Actor: actor,
		})
	}
}

func (s *Service) logLocked(e LogEntry) {
	if s.logger != nil {
		s.logger.Log(e)
	}
}

// mutateLocked 封装固定判定次序：
// 1) 对象是否存在；2) 惰性到期结算；3) 方向是否允许；
// 4) 时刻/时长是否合法；5) 冻结未满保护。
// 返回据以判定的（结算后）状态、最终状态与错误。
func (s *Service) mutateLocked(op, id string, actor Identity, input string,
	apply func(r *record, now time.Time, beforeSettle State) error) (State, State, error) {
	now := s.clock.Now()
	r, ok := s.objects[id]
	if !ok {
		s.logLocked(LogEntry{
			Time: now, Operation: op, ObjectID: id, Actor: actor,
			Input: input, Output: "rejected", Err: ErrObjectNotFound.Error(),
		})
		return "", "", ErrObjectNotFound
	}
	before := r.state
	triggerKind := "auto-archive"
	triggerActor := Identity("")
	if op == "archive" {
		triggerKind = "archive"
		triggerActor = actor
	}
	s.settleLocked(r, now, triggerKind, triggerActor)
	err := apply(r, now, before)
	after := r.state
	entry := LogEntry{
		Time: now, Operation: op, ObjectID: id, Actor: actor, Input: input,
		StateBefore: before, StateAfter: after,
	}
	if err != nil {
		entry.Output = "rejected"
		entry.Err = err.Error()
	} else {
		entry.Output = "accepted"
	}
	s.logLocked(entry)
	return before, after, err
}

// CreateObject 创建存活对象。
func (s *Service) CreateObject(id string, attrs Attrs) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.clock.Now()
	if _, ok := s.objects[id]; ok {
		return ErrDuplicateObject
	}
	copied := make(Attrs, len(attrs))
	for k, v := range attrs {
		copied[k] = v
	}
	s.objects[id] = &record{id: id, state: StateAlive, attrs: copied}
	s.logLocked(LogEntry{
		Time: now, Operation: "create", ObjectID: id,
		StateBefore: "", StateAfter: StateAlive, Output: "accepted",
	})
	return nil
}

// AddEdge 建立出边链接。链接自身无删除状态。
func (s *Service) AddEdge(e Edge) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.clock.Now()
	if _, ok := s.objects[e.SourceID]; !ok {
		return fmt.Errorf("%w: source %q", ErrObjectNotFound, e.SourceID)
	}
	if _, ok := s.objects[e.TargetID]; !ok {
		return fmt.Errorf("%w: target %q", ErrObjectNotFound, e.TargetID)
	}
	s.edges[e.SourceID] = append(s.edges[e.SourceID], e)
	s.logLocked(LogEntry{
		Time: now, Operation: "add-edge", ObjectID: e.SourceID, Input: e.ID,
		StateBefore: s.objects[e.SourceID].state,
		StateAfter:  s.objects[e.SourceID].state,
		Output:      "accepted",
	})
	return nil
}

// Delete 存活 → 待撤销宽限。
func (s *Service) Delete(id string, actor Identity, graceDeadline time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	input := "deadline=" + graceDeadline.Format(time.RFC3339Nano)
	_, _, err := s.mutateLocked("delete", id, actor, input, func(r *record, now time.Time, _ State) error {
		if r.state != StateAlive {
			return ErrInvalidTransition
		}
		if !graceDeadline.After(now) {
			return ErrInvalidTime
		}
		s.transitionLocked(r, now, StateGrace, "delete", actor, graceDeadline)
		return nil
	})
	return err
}

// Undo 请求撤销（仅宽限期内、截止时刻之前有效）。
func (s *Service) Undo(id string, actor Identity) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, _, err := s.mutateLocked("undo", id, actor, "", func(r *record, now time.Time, _ State) error {
		switch r.state {
		case StateGrace:
			// 能停留在 grace 即说明 now < 宽限截止时刻（截止时刻恰好
			// 到达已被 settleLocked 惰性归档），撤销必然成立。
			s.transitionLocked(r, now, StateAlive, "undo", actor, time.Time{})
			return nil
		case StateFrozen:
			// 同理，仍处于 frozen 即冻结未满。
			return ErrFrozenNotExpired
		default:
			return ErrInvalidTransition
		}
	})
	return err
}

// Freeze 存活 → 保留期冻结。
func (s *Service) Freeze(id string, actor Identity, freezeDuration time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	input := "duration=" + freezeDuration.String()
	_, _, err := s.mutateLocked("freeze", id, actor, input, func(r *record, now time.Time, _ State) error {
		if r.state != StateAlive {
			return ErrInvalidTransition
		}
		if freezeDuration <= 0 {
			return ErrInvalidTime
		}
		deadline := now.Add(freezeDuration)
		s.transitionLocked(r, now, StateFrozen, "freeze", actor, deadline)
		return nil
	})
	return err
}

// Archive 请求显式归档。
func (s *Service) Archive(id string, actor Identity) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, _, err := s.mutateLocked("archive", id, actor, "", func(r *record, now time.Time, beforeSettle State) error {
		switch r.state {
		case StateArchived:
			// 归档是终态：显式归档请求幂等成功——无论归档是
			// 早已发生还是恰好由本次请求触发结算完成。
			return nil
		case StateFrozen:
			// 仍处于 frozen 即冻结未满；恰好到期已被 settleLocked 归档。
			return ErrFrozenNotExpired
		case StateGrace:
			// grace→archive 只能由系统在截止时刻自动完成，
			// 不接受调用方提前发起的显式归档。
			return ErrInvalidTransition
		default:
			// alive 与 archived 均无调用方可发起的归档方向。
			return ErrInvalidTransition
		}
	})
	return err
}

// GetView 查询某身份下对象的可见性视图。
func (s *Service) GetView(id string, actor Identity) View {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.viewLocked(id, actor)
}

func (s *Service) viewLocked(id string, actor Identity) View {
	now := s.clock.Now()
	r, ok := s.objects[id]
	if !ok {
		return View{ID: id}
	}
	s.settleLocked(r, now, "auto-archive", "")
	view := View{ID: id, Exists: true, State: r.state}
	switch actor {
	case IdentityUser:
		view.Visible = r.state == StateAlive
	case IdentityAdmin:
		view.Visible = true
		view.FreezeDeadline = r.freezeDeadline
		view.GraceDeadline = r.graceDeadline
	default:
		view.Visible = false
		return view
	}
	if !view.Visible {
		return view
	}
	if actor == IdentityAdmin && r.state == StateFrozen {
		// 冻结对象：只暴露状态与冻结截止时刻，不暴露业务属性。
		return view
	}
	copied := make(Attrs, len(r.attrs))
	for k, v := range r.attrs {
		copied[k] = v
	}
	view.Attrs = copied
	return view
}

// ViewEdges 查询某身份下源对象的可见出边。
func (s *Service) ViewEdges(sourceID string, actor Identity) []Edge {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.edgesLocked(sourceID, actor)
}

func (s *Service) edgesLocked(sourceID string, actor Identity) []Edge {
	r, ok := s.objects[sourceID]
	if !ok {
		return nil
	}
	visible := false
	switch actor {
	case IdentityUser:
		visible = r.state == StateAlive
	case IdentityAdmin:
		visible = true
	}
	if !visible {
		return nil
	}
	src := s.edges[sourceID]
	out := make([]Edge, len(src))
	copy(out, src)
	return out
}

// ViewWithEdges 在同一临界区内同时取对象视图与其出边，
// 保证二者来自同一个线性化点（供并发一致性测试使用）。
func (s *Service) ViewWithEdges(id string, actor Identity) (View, []Edge) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v := s.viewLocked(id, actor)
	return v, s.edgesLocked(id, actor)
}

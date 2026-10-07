package ontology

import (
	"fmt"
	"sync"
)

// Service 提供对象整体删除与属性历史独立删除两套机制。
// 并发安全：所有操作在内部互斥锁下串行化，效果等价于某个全局顺序。
type Service struct {
	mu      sync.RWMutex
	objects map[ObjectID]*objectState
	log     []LogEntry
	seq     uint64

	// recordFlagWrites 统计对历史记录删除标记的写入次数，
	// 用于在测试中证明整体删除/复活的开销与记录总数无关。
	recordFlagWrites uint64
}

type objectState struct {
	deleted bool
	// props[property] 为该属性的历史记录，按 ValidFrom 升序。
	props map[string][]*HistoryRecord
	byID  map[RecordID]*HistoryRecord
}

// NewService 创建一个空的服务实例。
func NewService() *Service {
	return &Service{objects: make(map[ObjectID]*objectState)}
}

// CreateObject 创建一个存活状态的对象实例。
func (s *Service) CreateObject(id ObjectID) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.objects[id]; ok {
		s.appendLog(LogEntry{Op: OpCreateObject, ObjectID: id, Err: ErrObjectExists})
		return ErrObjectExists
	}
	s.objects[id] = &objectState{
		props: make(map[string][]*HistoryRecord),
		byID:  make(map[RecordID]*HistoryRecord),
	}
	s.appendLog(LogEntry{Op: OpCreateObject, ObjectID: id, Cond: TriCondition{ObjectAlive: true}})
	return nil
}

// AddHistory 向对象的某属性追加一条历史记录。
// 同一属性内 ValidFrom 必须严格递增（时间维度只前进）。
func (s *Service) AddHistory(id ObjectID, property string, value Value, validFrom int64) (RecordID, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	obj, ok := s.objects[id]
	if !ok {
		s.appendLog(LogEntry{Op: OpAddHistory, ObjectID: id, Property: property, Err: ErrObjectNotFound})
		return "", ErrObjectNotFound
	}
	hist := obj.props[property]
	if n := len(hist); n > 0 && validFrom <= hist[n-1].ValidFrom {
		s.appendLog(LogEntry{Op: OpAddHistory, ObjectID: id, Property: property, Err: ErrNotMonotonic})
		return "", ErrNotMonotonic
	}
	rec := &HistoryRecord{
		ID:        RecordID(fmt.Sprintf("%s/%s/%d", id, property, validFrom)),
		ObjectID:  id,
		Property:  property,
		Value:     value,
		ValidFrom: validFrom,
	}
	obj.props[property] = append(hist, rec)
	obj.byID[rec.ID] = rec
	s.appendLog(LogEntry{
		Op: OpAddHistory, ObjectID: id, RecordID: rec.ID, Property: property,
		Cond: TriCondition{ObjectAlive: !obj.deleted, RecordAlive: true, IsCurrent: true},
	})
	return rec.ID, nil
}

// DeleteObject 整体逻辑删除对象：全有或全无，O(1)，不触碰任何历史记录标记。
func (s *Service) DeleteObject(id ObjectID) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	obj, ok := s.objects[id]
	if !ok {
		s.appendLog(LogEntry{Op: OpDeleteObject, ObjectID: id, Err: ErrObjectNotFound})
		return ErrObjectNotFound
	}
	obj.deleted = true
	s.appendLog(LogEntry{Op: OpDeleteObject, ObjectID: id, Cond: TriCondition{ObjectAlive: false}})
	return nil
}

// RestoreObject 撤销整体删除，复活对象。
// 不隐式恢复/删除任何历史记录：只翻转对象级标记，O(1)。
func (s *Service) RestoreObject(id ObjectID) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	obj, ok := s.objects[id]
	if !ok {
		s.appendLog(LogEntry{Op: OpRestoreObject, ObjectID: id, Err: ErrObjectNotFound})
		return ErrObjectNotFound
	}
	obj.deleted = false
	s.appendLog(LogEntry{Op: OpRestoreObject, ObjectID: id, Cond: TriCondition{ObjectAlive: true}})
	return nil
}

// DeleteRecord 单独逻辑删除一条属性历史记录。
// 错误判定次序固定：对象不存在 → 记录不存在 → 对象整体删除中 → 记录已被单独删除。
func (s *Service) DeleteRecord(id ObjectID, rid RecordID) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	obj, rec, err := s.checkRecordOp(id, rid)
	if err != nil {
		s.appendLog(LogEntry{Op: OpDeleteRecord, ObjectID: id, RecordID: rid, Err: err,
			Cond: s.condFor(obj, rec)})
		return err
	}
	rec.Deleted = true
	s.recordFlagWrites++
	s.appendLog(LogEntry{Op: OpDeleteRecord, ObjectID: id, RecordID: rid,
		Cond: TriCondition{ObjectAlive: true, RecordAlive: false, IsCurrent: s.isCurrent(obj, rec)}})
	return nil
}

// UndeleteRecord 撤销一条历史记录的单独删除。
// 对未被单独删除的记录为幂等空操作（不报错、不改状态）。
func (s *Service) UndeleteRecord(id ObjectID, rid RecordID) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	obj, ok := s.objects[id]
	if !ok {
		s.appendLog(LogEntry{Op: OpUndeleteRecord, ObjectID: id, RecordID: rid, Err: ErrObjectNotFound})
		return ErrObjectNotFound
	}
	rec, ok := obj.byID[rid]
	if !ok {
		s.appendLog(LogEntry{Op: OpUndeleteRecord, ObjectID: id, RecordID: rid, Err: ErrRecordNotFound,
			Cond: TriCondition{ObjectAlive: !obj.deleted}})
		return ErrRecordNotFound
	}
	if obj.deleted {
		s.appendLog(LogEntry{Op: OpUndeleteRecord, ObjectID: id, RecordID: rid, Err: ErrObjectDeleted,
			Cond: TriCondition{ObjectAlive: false, RecordAlive: !rec.Deleted, IsCurrent: s.isCurrent(obj, rec)}})
		return ErrObjectDeleted
	}
	if rec.Deleted {
		rec.Deleted = false
		s.recordFlagWrites++
	}
	s.appendLog(LogEntry{Op: OpUndeleteRecord, ObjectID: id, RecordID: rid,
		Cond: TriCondition{ObjectAlive: true, RecordAlive: true, IsCurrent: s.isCurrent(obj, rec)}})
	return nil
}

// checkRecordOp 实现单独删除的固定错误判定次序。
func (s *Service) checkRecordOp(id ObjectID, rid RecordID) (*objectState, *HistoryRecord, error) {
	obj, ok := s.objects[id]
	if !ok {
		return nil, nil, ErrObjectNotFound
	}
	rec, ok := obj.byID[rid]
	if !ok {
		return obj, nil, ErrRecordNotFound
	}
	if obj.deleted {
		return obj, rec, ErrObjectDeleted
	}
	if rec.Deleted {
		return obj, rec, ErrRecordAlreadyDeleted
	}
	return obj, rec, nil
}

// QueryVisibleValue 查询某属性在 at 时间点的「当前可见取值」。
// 依次核对三层条件，任一未通过即按互斥原因归类返回。
func (s *Service) QueryVisibleValue(id ObjectID, property string, at int64) QueryResult {
	s.mu.Lock()
	defer s.mu.Unlock()
	obj, ok := s.objects[id]
	if !ok {
		res := QueryResult{Visible: false, Err: ErrObjectNotFound}
		s.appendLog(LogEntry{Op: OpQueryVisibleValue, ObjectID: id, Property: property, At: at, Err: ErrObjectNotFound, Result: &res})
		return res
	}
	// 先在时间维度上定位候选记录（最新有效记录），再按固定层序归类。
	cand := latestAt(obj.props[property], at)
	cond := TriCondition{
		ObjectAlive: !obj.deleted,
		RecordAlive: cand == nil || !cand.Deleted, // 无候选记录时第二层空虚通过
		IsCurrent:   cand != nil,
	}
	var res QueryResult
	switch {
	case !cond.ObjectAlive:
		res = QueryResult{Visible: false, Reason: ReasonObjectDeleted, Record: recordIDOf(cand)}
	case !cond.IsCurrent:
		res = QueryResult{Visible: false, Reason: ReasonNotCurrent}
	case !cond.RecordAlive:
		res = QueryResult{Visible: false, Reason: ReasonRecordDeleted, Record: cand.ID}
	default:
		res = QueryResult{Visible: true, Value: cand.Value, Record: cand.ID}
	}
	s.appendLog(LogEntry{Op: OpQueryVisibleValue, ObjectID: id, Property: property, At: at, Result: &res, Cond: cond})
	return res
}

// latestAt 返回 at 时间点生效的最新记录（ValidFrom <= at 中最大者）。
func latestAt(hist []*HistoryRecord, at int64) *HistoryRecord {
	lo, hi := 0, len(hist)
	for lo < hi {
		mid := (lo + hi) / 2
		if hist[mid].ValidFrom <= at {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	if lo == 0 {
		return nil
	}
	return hist[lo-1]
}

func (s *Service) isCurrent(obj *objectState, rec *HistoryRecord) bool {
	if rec == nil {
		return false
	}
	hist := obj.props[rec.Property]
	return len(hist) > 0 && hist[len(hist)-1] == rec
}

func (s *Service) condFor(obj *objectState, rec *HistoryRecord) TriCondition {
	c := TriCondition{}
	if obj != nil {
		c.ObjectAlive = !obj.deleted
	}
	if rec != nil {
		c.RecordAlive = !rec.Deleted
		c.IsCurrent = s.isCurrent(obj, rec)
	}
	return c
}

func recordIDOf(rec *HistoryRecord) RecordID {
	if rec == nil {
		return ""
	}
	return rec.ID
}

// appendLog 追加操作日志。调用方须持有写锁。
func (s *Service) appendLog(e LogEntry) {
	s.seq++
	e.Seq = s.seq
	s.log = append(s.log, e)
}

// Log 返回操作日志的副本（按全局顺序排列）。
func (s *Service) Log() []LogEntry {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]LogEntry, len(s.log))
	copy(out, s.log)
	return out
}

// RecordFlagWrites 返回历史记录删除标记的累计写入次数。
// 整体删除/复活对该计数零贡献，可在测试中核对 O(1) 性质。
func (s *Service) RecordFlagWrites() uint64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.recordFlagWrites
}

// Snapshot 导出全部标记状态，供与朴素实现逐条比对。
func (s *Service) Snapshot() map[ObjectID]ObjectSnapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make(map[ObjectID]ObjectSnapshot, len(s.objects))
	for id, obj := range s.objects {
		snap := ObjectSnapshot{Deleted: obj.deleted, Records: make(map[RecordID]bool, len(obj.byID))}
		for rid, rec := range obj.byID {
			snap.Records[rid] = rec.Deleted
		}
		out[id] = snap
	}
	return out
}

// ObjectSnapshot 是一个对象的整体删除标记与全部记录单独删除标记。
type ObjectSnapshot struct {
	Deleted bool
	Records map[RecordID]bool
}

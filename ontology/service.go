package ontology

import (
	"errors"
	"time"
)

// Service 是本体删除/复活/查询服务。
type Service struct {
	st  *store
	lg  *OpLogger
	seq int64
}

func NewService(logger *OpLogger) *Service {
	return &Service{st: newStore(), lg: logger}
}

func boolp(b bool) *bool { return &b }

// logLocked 在临界区内写日志，保证日志顺序与全局线性化顺序一致。
func (s *Service) logLocked(op string, in map[string]any, out map[string]any, err error, c Conditions) {
	if s.lg == nil {
		return
	}
	s.seq++
	entry := LogEntry{Seq: s.seq, Op: op, Input: in, Output: out, Conditions: c}
	if err != nil {
		entry.Error = err.Error()
	} else {
		entry.OK = true
	}
	s.lg.write(entry)
}

func (s *Service) CreateObject(id string) error {
	if id == "" {
		return ErrInvalidArgument
	}
	s.st.mu.Lock()
	defer s.st.mu.Unlock()
	created := s.st.createObjectLocked(id)
	s.logLocked("CreateObject", map[string]any{"object_id": id},
		map[string]any{"created": created}, nil, Conditions{})
	return nil
}

func (s *Service) DeleteObject(id string) error {
	s.st.mu.Lock()
	defer s.st.mu.Unlock()
	obj := s.st.getObjectLocked(id)
	if obj == nil {
		err := ErrObjectNotFound
		s.logLocked("DeleteObject", map[string]any{"object_id": id}, nil, err, Conditions{})
		return err
	}
	if obj.tombstoned {
		err := ErrObjectAlreadyTombstoned
		s.logLocked("DeleteObject", map[string]any{"object_id": id}, nil, err,
			Conditions{ObjectAlive: boolp(false)})
		return err
	}
	// 全有或全无：只翻转对象自身标记，不遍历、不触碰任何属性历史记录
	// 或其单独删除标记集合。
	obj.tombstoned = true
	s.logLocked("DeleteObject", map[string]any{"object_id": id},
		map[string]any{"alive": false}, nil, Conditions{ObjectAlive: boolp(true)})
	return nil
}

func (s *Service) ReviveObject(id string) error {
	s.st.mu.Lock()
	defer s.st.mu.Unlock()
	obj := s.st.getObjectLocked(id)
	if obj == nil {
		err := ErrObjectNotFound
		s.logLocked("ReviveObject", map[string]any{"object_id": id}, nil, err, Conditions{})
		return err
	}
	if !obj.tombstoned {
		err := ErrObjectNotTombstoned
		s.logLocked("ReviveObject", map[string]any{"object_id": id}, nil, err,
			Conditions{ObjectAlive: boolp(true)})
		return err
	}
	// 复活同样只动对象标记；单独删除标记集合原样保留。
	obj.tombstoned = false
	s.logLocked("ReviveObject", map[string]any{"object_id": id},
		map[string]any{"alive": true}, nil, Conditions{ObjectAlive: boolp(false)})
	return nil
}

func (s *Service) IsObjectAlive(id string) (bool, error) {
	s.st.mu.Lock()
	defer s.st.mu.Unlock()
	obj := s.st.getObjectLocked(id)
	if obj == nil {
		err := ErrObjectNotFound
		s.logLocked("IsObjectAlive", map[string]any{"object_id": id}, nil, err, Conditions{})
		return false, err
	}
	alive := !obj.tombstoned
	s.logLocked("IsObjectAlive", map[string]any{"object_id": id},
		map[string]any{"alive": alive}, nil, Conditions{ObjectAlive: boolp(alive)})
	return alive, nil
}

func (s *Service) AddHistory(rec HistoryRecord) error {
	if rec.ID == "" || rec.ObjectID == "" || rec.Property == "" {
		return ErrInvalidArgument
	}
	s.st.mu.Lock()
	defer s.st.mu.Unlock()
	obj := s.st.getObjectLocked(rec.ObjectID)
	if obj == nil {
		err := ErrObjectNotFound
		s.logLocked("AddHistory", recInput(rec), nil, err, Conditions{})
		return err
	}
	if obj.tombstoned {
		err := ErrObjectTombstoned
		s.logLocked("AddHistory", recInput(rec), nil, err,
			Conditions{ObjectAlive: boolp(false)})
		return err
	}
	if !s.st.putRecordLocked(rec) {
		err := ErrDuplicateHistory
		s.logLocked("AddHistory", recInput(rec), nil, err,
			Conditions{ObjectAlive: boolp(true)})
		return err
	}
	s.logLocked("AddHistory", recInput(rec), map[string]any{"record_id": rec.ID}, nil,
		Conditions{ObjectAlive: boolp(true), RecordLive: boolp(true)})
	return nil
}

func recInput(rec HistoryRecord) map[string]any {
	return map[string]any{
		"record_id":    rec.ID,
		"object_id":    rec.ObjectID,
		"property":     rec.Property,
		"value":        rec.Value,
		"effective_at": rec.EffectiveAt.Format(time.RFC3339Nano),
	}
}

// resolveHistoryTarget 按固定次序校验：对象存在 -> 历史存在且属于该对象
// -> 对象存活。调用方持锁。
func (s *Service) resolveHistoryTarget(op string, objectID, recordID string) (*objectState, *HistoryRecord, error) {
	obj := s.st.getObjectLocked(objectID)
	if obj == nil {
		return nil, nil, ErrObjectNotFound
	}
	rec := s.st.getRecordLocked(recordID)
	if rec == nil || rec.ObjectID != objectID {
		return obj, nil, ErrHistoryNotFound
	}
	if obj.tombstoned {
		return obj, rec, ErrObjectTombstoned
	}
	return obj, rec, nil
}

func historyInput(objectID, recordID string) map[string]any {
	return map[string]any{"object_id": objectID, "record_id": recordID}
}

func (s *Service) DeleteHistory(objectID, recordID string) error {
	s.st.mu.Lock()
	defer s.st.mu.Unlock()
	in := historyInput(objectID, recordID)
	obj, rec, err := s.resolveHistoryTarget("DeleteHistory", objectID, recordID)
	switch {
	case errors.Is(err, ErrObjectNotFound):
		s.logLocked("DeleteHistory", in, nil, err, Conditions{})
		return err
	case errors.Is(err, ErrHistoryNotFound):
		s.logLocked("DeleteHistory", in, nil, err,
			Conditions{ObjectAlive: boolp(!obj.tombstoned)})
		return err
	case errors.Is(err, ErrObjectTombstoned):
		s.logLocked("DeleteHistory", in, nil, err,
			Conditions{ObjectAlive: boolp(false), RecordLive: boolp(!s.st.isRecordTombstonedLocked(recordID))})
		return err
	}
	// 第 4 类：对已单独删除的记录重复发起单独删除 —— 拒绝且不改任何状态。
	if s.st.isRecordTombstonedLocked(recordID) {
		err := ErrHistoryAlreadyDeleted
		s.logLocked("DeleteHistory", in, nil, err,
			Conditions{ObjectAlive: boolp(true), RecordLive: boolp(false)})
		return err
	}
	s.st.setRecordTombstoneLocked(recordID, true)
	s.logLocked("DeleteHistory", in, map[string]any{"live": false}, nil,
		Conditions{ObjectAlive: boolp(true), RecordLive: boolp(false)})
	_ = rec
	return nil
}

func (s *Service) RestoreHistory(objectID, recordID string) error {
	s.st.mu.Lock()
	defer s.st.mu.Unlock()
	in := historyInput(objectID, recordID)
	obj, _, err := s.resolveHistoryTarget("RestoreHistory", objectID, recordID)
	switch {
	case errors.Is(err, ErrObjectNotFound):
		s.logLocked("RestoreHistory", in, nil, err, Conditions{})
		return err
	case errors.Is(err, ErrHistoryNotFound):
		s.logLocked("RestoreHistory", in, nil, err,
			Conditions{ObjectAlive: boolp(!obj.tombstoned)})
		return err
	case errors.Is(err, ErrObjectTombstoned):
		s.logLocked("RestoreHistory", in, nil, err,
			Conditions{ObjectAlive: boolp(false)})
		return err
	}
	if !s.st.isRecordTombstonedLocked(recordID) {
		err := ErrHistoryNotDeleted
		s.logLocked("RestoreHistory", in, nil, err,
			Conditions{ObjectAlive: boolp(true), RecordLive: boolp(true)})
		return err
	}
	s.st.setRecordTombstoneLocked(recordID, false)
	s.logLocked("RestoreHistory", in, map[string]any{"live": true}, nil,
		Conditions{ObjectAlive: boolp(true), RecordLive: boolp(true)})
	return nil
}

func (s *Service) IsHistoryLive(recordID string) (bool, error) {
	s.st.mu.Lock()
	defer s.st.mu.Unlock()
	rec := s.st.getRecordLocked(recordID)
	if rec == nil {
		err := ErrHistoryNotFound
		s.logLocked("IsHistoryLive", map[string]any{"record_id": recordID}, nil, err, Conditions{})
		return false, err
	}
	live := !s.st.isRecordTombstonedLocked(recordID)
	s.logLocked("IsHistoryLive", map[string]any{"record_id": recordID},
		map[string]any{"live": live}, nil,
		Conditions{RecordLive: boolp(live)})
	return live, nil
}

// TombstoneSnapshot 供测试/运维导出全部历史单独删除标记，
// 用于复活后逐一验证标记未被遍历改写。
func (s *Service) TombstoneSnapshot() map[string]bool {
	s.st.mu.Lock()
	defer s.st.mu.Unlock()
	return s.st.tombstoneSnapshotLocked()
}

func (s *Service) VisibleValue(objectID, property string, at time.Time) (VisibilityResult, error) {
	s.st.mu.Lock()
	defer s.st.mu.Unlock()
	in := map[string]any{"object_id": objectID, "property": property,
		"at": at.Format(time.RFC3339Nano)}
	obj := s.st.getObjectLocked(objectID)
	if obj == nil {
		err := ErrObjectNotFound
		s.logLocked("VisibleValue", in, nil, err, Conditions{})
		return VisibilityResult{}, err
	}

	// 第 1 层：对象整体删除状态。
	if obj.tombstoned {
		res := VisibilityResult{Visible: false, Reason: ReasonObjectTombstoned}
		s.logLocked("VisibleValue", in, reasonOutput(res), nil,
			Conditions{ObjectAlive: boolp(false)})
		return res, nil
	}

	// 第 3 层（时间维度）：找出该时间点上的最新有效记录。
	latest := s.st.latestRecordAtLocked(objectID, property, at)
	if latest == nil {
		res := VisibilityResult{Visible: false, Reason: ReasonNoValidRecord}
		s.logLocked("VisibleValue", in, reasonOutput(res), nil,
			Conditions{ObjectAlive: boolp(true), IsLatestAt: boolp(false)})
		return res, nil
	}

	// 第 2 层：该条记录自身的单独删除状态。
	if s.st.isRecordTombstonedLocked(latest.ID) {
		res := VisibilityResult{Visible: false, Reason: ReasonRecordTombstoned, RecordID: latest.ID}
		s.logLocked("VisibleValue", in, reasonOutput(res), nil,
			Conditions{ObjectAlive: boolp(true), RecordLive: boolp(false), IsLatestAt: boolp(true)})
		return res, nil
	}

	res := VisibilityResult{Visible: true, RecordID: latest.ID, Value: latest.Value}
	s.logLocked("VisibleValue", in, map[string]any{
		"visible": true, "record_id": latest.ID, "value": latest.Value,
	}, nil, Conditions{ObjectAlive: boolp(true), RecordLive: boolp(true), IsLatestAt: boolp(true)})
	return res, nil
}

func reasonOutput(res VisibilityResult) map[string]any {
	return map[string]any{"visible": false, "reason": string(res.Reason), "record_id": res.RecordID}
}

func (s *Service) Counters() CounterSnapshot {
	s.st.mu.Lock()
	defer s.st.mu.Unlock()
	return s.st.countersLocked()
}

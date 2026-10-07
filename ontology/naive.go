package ontology

import (
	"errors"
	"time"
)

// NaiveModel 是独立维护全部标记、逐条比对的朴素参照实现。
// 它刻意不使用任何索引：每次查询都全量扫描所有记录，
// 标记也各用一张独立的表维护，作为差分测试的“黄金模型”。
type NaiveModel struct {
	objects       map[string]bool // objectID -> 整体删除
	records       []HistoryRecord
	recTombstones map[string]bool // recordID -> 单独删除
}

func NewNaiveModel() *NaiveModel {
	return &NaiveModel{
		objects:       map[string]bool{},
		recTombstones: map[string]bool{},
	}
}

func (m *NaiveModel) CreateObject(id string) error {
	if id == "" {
		return ErrInvalidArgument
	}
	if _, ok := m.objects[id]; !ok {
		m.objects[id] = false
	}
	return nil
}

func (m *NaiveModel) DeleteObject(id string) error {
	tomb, ok := m.objects[id]
	if !ok {
		return ErrObjectNotFound
	}
	if tomb {
		return ErrObjectAlreadyTombstoned
	}
	m.objects[id] = true
	return nil
}

func (m *NaiveModel) ReviveObject(id string) error {
	tomb, ok := m.objects[id]
	if !ok {
		return ErrObjectNotFound
	}
	if !tomb {
		return ErrObjectNotTombstoned
	}
	m.objects[id] = false
	return nil
}

func (m *NaiveModel) AddHistory(rec HistoryRecord) error {
	if rec.ID == "" || rec.ObjectID == "" || rec.Property == "" {
		return ErrInvalidArgument
	}
	tomb, ok := m.objects[rec.ObjectID]
	if !ok {
		return ErrObjectNotFound
	}
	if tomb {
		return ErrObjectTombstoned
	}
	for _, r := range m.records {
		if r.ID == rec.ID {
			return ErrDuplicateHistory
		}
	}
	m.records = append(m.records, rec)
	m.recTombstones[rec.ID] = false
	return nil
}

func (m *NaiveModel) findRecord(objectID, recordID string) *HistoryRecord {
	for i := range m.records {
		if m.records[i].ID == recordID && m.records[i].ObjectID == objectID {
			return &m.records[i]
		}
	}
	return nil
}

func (m *NaiveModel) resolve(objectID, recordID string) error {
	if _, ok := m.objects[objectID]; !ok {
		return ErrObjectNotFound
	}
	if m.findRecord(objectID, recordID) == nil {
		return ErrHistoryNotFound
	}
	if m.objects[objectID] {
		return ErrObjectTombstoned
	}
	return nil
}

func (m *NaiveModel) DeleteHistory(objectID, recordID string) error {
	if err := m.resolve(objectID, recordID); err != nil {
		return err
	}
	if m.recTombstones[recordID] {
		return ErrHistoryAlreadyDeleted
	}
	m.recTombstones[recordID] = true
	return nil
}

func (m *NaiveModel) RestoreHistory(objectID, recordID string) error {
	if err := m.resolve(objectID, recordID); err != nil {
		return err
	}
	if !m.recTombstones[recordID] {
		return ErrHistoryNotDeleted
	}
	m.recTombstones[recordID] = false
	return nil
}

func (m *NaiveModel) IsObjectAlive(id string) (bool, error) {
	tomb, ok := m.objects[id]
	if !ok {
		return false, ErrObjectNotFound
	}
	return !tomb, nil
}

func (m *NaiveModel) IsHistoryLive(recordID string) (bool, error) {
	live, known := m.recTombstones[recordID]
	if !known {
		found := false
		for _, r := range m.records {
			if r.ID == recordID {
				found = true
			}
		}
		if !found {
			return false, ErrHistoryNotFound
		}
		return false, nil
	}
	return !live, nil
}

func (m *NaiveModel) VisibleValue(objectID, property string, at time.Time) (VisibilityResult, error) {
	tomb, ok := m.objects[objectID]
	if !ok {
		return VisibilityResult{}, ErrObjectNotFound
	}
	if tomb {
		return VisibilityResult{Visible: false, Reason: ReasonObjectTombstoned}, nil
	}
	// 全量线性扫描找时间点上的最新记录。
	var latest *HistoryRecord
	for i := range m.records {
		r := &m.records[i]
		if r.ObjectID != objectID || r.Property != property {
			continue
		}
		if r.EffectiveAt.After(at) {
			continue
		}
		if latest == nil || r.EffectiveAt.After(latest.EffectiveAt) {
			latest = r
		}
	}
	if latest == nil {
		return VisibilityResult{Visible: false, Reason: ReasonNoValidRecord}, nil
	}
	if m.recTombstones[latest.ID] {
		return VisibilityResult{Visible: false, Reason: ReasonRecordTombstoned, RecordID: latest.ID}, nil
	}
	return VisibilityResult{Visible: true, RecordID: latest.ID, Value: latest.Value}, nil
}

// EquivError 比较两个错误是否为同一类（含均为 nil 的情形）。
func EquivError(a, b error) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return errors.Is(a, b) || errors.Is(b, a) || a.Error() == b.Error()
}

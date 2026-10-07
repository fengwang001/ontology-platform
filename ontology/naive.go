package ontology

import "fmt"

// NaiveService 是朴素参照实现：整体删除时逐条遍历并下推标记到
// 每一条历史记录上，复活时再逐条清除。它独立维护全部标记，
// 用于在随机操作序列上与 Service 逐条比对结果。
// 不保证并发安全（仅单线程测试使用）。
type NaiveService struct {
	objects map[ObjectID]*naiveObject
}

type naiveObject struct {
	deleted bool
	props   map[string][]*naiveRecord
	byID    map[RecordID]*naiveRecord
}

type naiveRecord struct {
	id          RecordID
	property    string
	value       Value
	validFrom   int64
	selfDeleted bool // 单独删除标记
	hiddenByObj bool // 整体删除时下推到每条记录的标记
}

// NewNaiveService 创建朴素参照实现。
func NewNaiveService() *NaiveService {
	return &NaiveService{objects: make(map[ObjectID]*naiveObject)}
}

// CreateObject 同 Service.CreateObject。
func (n *NaiveService) CreateObject(id ObjectID) error {
	if _, ok := n.objects[id]; ok {
		return ErrObjectExists
	}
	n.objects[id] = &naiveObject{
		props: make(map[string][]*naiveRecord),
		byID:  make(map[RecordID]*naiveRecord),
	}
	return nil
}

// AddHistory 同 Service.AddHistory，使用相同的 RecordID 生成规则。
func (n *NaiveService) AddHistory(id ObjectID, property string, value Value, validFrom int64) (RecordID, error) {
	obj, ok := n.objects[id]
	if !ok {
		return "", ErrObjectNotFound
	}
	hist := obj.props[property]
	if l := len(hist); l > 0 && validFrom <= hist[l-1].validFrom {
		return "", ErrNotMonotonic
	}
	rid := RecordID(fmt.Sprintf("%s/%s/%d", id, property, validFrom))
	rec := &naiveRecord{id: rid, property: property, value: value, validFrom: validFrom, hiddenByObj: obj.deleted}
	obj.props[property] = append(hist, rec)
	obj.byID[rid] = rec
	return rid, nil
}

// DeleteObject 逐条下推整体删除标记（朴素 O(N) 行为，作为对照）。
func (n *NaiveService) DeleteObject(id ObjectID) error {
	obj, ok := n.objects[id]
	if !ok {
		return ErrObjectNotFound
	}
	obj.deleted = true
	for _, rec := range obj.byID {
		rec.hiddenByObj = true
	}
	return nil
}

// RestoreObject 逐条清除整体删除下推标记，保留 selfDeleted 不变。
func (n *NaiveService) RestoreObject(id ObjectID) error {
	obj, ok := n.objects[id]
	if !ok {
		return ErrObjectNotFound
	}
	obj.deleted = false
	for _, rec := range obj.byID {
		rec.hiddenByObj = false
	}
	return nil
}

// DeleteRecord 与 Service.DeleteRecord 相同的固定错误判定次序。
func (n *NaiveService) DeleteRecord(id ObjectID, rid RecordID) error {
	obj, ok := n.objects[id]
	if !ok {
		return ErrObjectNotFound
	}
	rec, ok := obj.byID[rid]
	if !ok {
		return ErrRecordNotFound
	}
	if obj.deleted {
		return ErrObjectDeleted
	}
	if rec.selfDeleted {
		return ErrRecordAlreadyDeleted
	}
	rec.selfDeleted = true
	return nil
}

// UndeleteRecord 与 Service.UndeleteRecord 语义一致（幂等撤销）。
func (n *NaiveService) UndeleteRecord(id ObjectID, rid RecordID) error {
	obj, ok := n.objects[id]
	if !ok {
		return ErrObjectNotFound
	}
	rec, ok := obj.byID[rid]
	if !ok {
		return ErrRecordNotFound
	}
	if obj.deleted {
		return ErrObjectDeleted
	}
	rec.selfDeleted = false
	return nil
}

// QueryVisibleValue 与 Service.QueryVisibleValue 相同的三层归类。
func (n *NaiveService) QueryVisibleValue(id ObjectID, property string, at int64) QueryResult {
	obj, ok := n.objects[id]
	if !ok {
		return QueryResult{Visible: false, Err: ErrObjectNotFound}
	}
	var cand *naiveRecord
	for _, rec := range obj.props[property] {
		if rec.validFrom <= at {
			cand = rec
		} else {
			break
		}
	}
	switch {
	case obj.deleted:
		return QueryResult{Visible: false, Reason: ReasonObjectDeleted, Record: naiveRecordID(cand)}
	case cand == nil:
		return QueryResult{Visible: false, Reason: ReasonNotCurrent}
	case cand.selfDeleted:
		return QueryResult{Visible: false, Reason: ReasonRecordDeleted, Record: cand.id}
	default:
		return QueryResult{Visible: true, Value: cand.value, Record: cand.id}
	}
}

// Snapshot 导出朴素实现的全部标记（selfDeleted 视角），供逐条比对。
func (n *NaiveService) Snapshot() map[ObjectID]ObjectSnapshot {
	out := make(map[ObjectID]ObjectSnapshot, len(n.objects))
	for id, obj := range n.objects {
		snap := ObjectSnapshot{Deleted: obj.deleted, Records: make(map[RecordID]bool, len(obj.byID))}
		for rid, rec := range obj.byID {
			snap.Records[rid] = rec.selfDeleted
		}
		out[id] = snap
	}
	return out
}

func naiveRecordID(rec *naiveRecord) RecordID {
	if rec == nil {
		return ""
	}
	return rec.id
}

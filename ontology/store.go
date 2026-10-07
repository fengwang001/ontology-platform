package ontology

import (
	"sort"
	"sync"
	"time"
)

// store 独立保存三类互不覆写的状态：
// 对象整体删除标记、历史记录本体、历史记录单独删除标记集合。
type store struct {
	mu sync.RWMutex

	// 对象整体删除标记（独立存储）。
	objects map[string]*objectState

	// 历史记录本体（只读：逻辑删除只动 recordTombstones，绝不改写记录）。
	records map[string]*HistoryRecord

	// 按 objectID -> property -> 按时间升序排列的记录 ID 索引。
	// 仅为查询服务；对象级删除/复活不触碰它。
	index map[string]map[string][]string

	// 历史记录单独删除标记集合（独立存储，key=recordID）。
	recordTombstones map[string]struct{}

	// 诊断计数器：触碰历史记录/单独删除标记的次数。
	recordAccesses   int64
	tombstoneTouches int64
}

type objectState struct {
	id         string
	tombstoned bool
}

func newStore() *store {
	return &store{
		objects:          map[string]*objectState{},
		records:          map[string]*HistoryRecord{},
		index:            map[string]map[string][]string{},
		recordTombstones: map[string]struct{}{},
	}
}

// ---- 对象整体删除标记（独立存储；下列两个方法绝不触碰任何属性历史结构）----

func (s *store) createObjectLocked(id string) bool {
	if _, ok := s.objects[id]; ok {
		return false
	}
	s.objects[id] = &objectState{id: id, tombstoned: false}
	s.index[id] = map[string][]string{}
	return true
}

func (s *store) getObjectLocked(id string) *objectState {
	return s.objects[id]
}

func (s *store) setObjectTombstoneLocked(id string, tombstoned bool) bool {
	obj := s.objects[id]
	if obj == nil || obj.tombstoned == tombstoned {
		return false
	}
	obj.tombstoned = tombstoned
	return true
}

// ---- 历史记录本体与时间索引 ----

func (s *store) putRecordLocked(rec HistoryRecord) bool {
	if _, ok := s.records[rec.ID]; ok {
		return false
	}
	cp := rec
	s.records[rec.ID] = &cp
	s.recordAccesses++
	ids := append(s.index[rec.ObjectID][rec.Property], rec.ID)
	sort.Slice(ids, func(i, j int) bool {
		return s.records[ids[i]].EffectiveAt.Before(s.records[ids[j]].EffectiveAt)
	})
	s.index[rec.ObjectID][rec.Property] = ids
	return true
}

func (s *store) getRecordLocked(id string) *HistoryRecord {
	s.recordAccesses++
	if rec := s.records[id]; rec != nil {
		cp := *rec
		return &cp
	}
	return nil
}

// latestRecordAtLocked 返回指定对象/属性在 at 时刻生效（effective_at <= at）
// 的最新一条记录（无视单独删除标记；可见性由三层判定另行处理）。
func (s *store) latestRecordAtLocked(objectID, property string, at time.Time) *HistoryRecord {
	s.recordAccesses++
	var latest *HistoryRecord
	for _, id := range s.index[objectID][property] {
		rec := s.records[id]
		if !rec.EffectiveAt.After(at) {
			cp := *rec
			latest = &cp
		}
	}
	return latest
}

// ---- 历史记录单独删除标记集合（独立存储）----

func (s *store) isRecordTombstonedLocked(id string) bool {
	s.tombstoneTouches++
	_, ok := s.recordTombstones[id]
	return ok
}

func (s *store) setRecordTombstoneLocked(id string, tombstoned bool) bool {
	_, exists := s.recordTombstones[id]
	if exists == tombstoned {
		s.tombstoneTouches++
		return false
	}
	s.tombstoneTouches++
	if tombstoned {
		s.recordTombstones[id] = struct{}{}
	} else {
		delete(s.recordTombstones, id)
	}
	return true
}

// tombstoneSnapshotLocked 复制全部单独删除标记；仅供测试在复活后逐一比对。
func (s *store) tombstoneSnapshotLocked() map[string]bool {
	s.tombstoneTouches++
	out := make(map[string]bool, len(s.recordTombstones))
	for id := range s.recordTombstones {
		out[id] = true
	}
	return out
}

func (s *store) countersLocked() CounterSnapshot {
	return CounterSnapshot{RecordAccesses: s.recordAccesses, TombstoneTouches: s.tombstoneTouches}
}

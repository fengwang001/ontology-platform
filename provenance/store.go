package provenance

import "sync"

// Store 是双时态对象/链接存储。
//
// 关键取舍：
//   - 所有历史写入只追加（append-only），旧记录永不修改或删除；修正即追加一条
//     写入时间严格更晚的新记录。
//   - 对象/链接记录各自按写入时间有序保存在切片中；另维护 source -> link 列表
//     的邻接索引，使一次溯源查询触及的候选数只与「被访问节点的邻接规模与
//     深度上限」有关，不随全图对象/链接总数线性增长。
//   - 单一 sync.RWMutex 覆盖写入与读取：写操作（追加记录 + 更新索引 +
//     提交序号）在临界区内完整完成；查询全程持读锁，看到的是某一提交点的
//     一致状态，不可能观察到「区间已被修正、但写入时间记录尚未完整落定」
//     的中间状态。读写因此可线性化（见 DESIGN.md 的并发论证）。
type Store struct {
	mu sync.RWMutex

	objects map[ObjectID][]ObjectRecord
	links   map[LinkID][]LinkRecord
	// out[id] 为以 id 为源端点的链接 ID（含已被后续修正声明消亡的链接；
	// 可见性必须由查询按 AsOf 时间判定，索引本身不能按当前状态裁剪历史）。
	out map[ObjectID][]LinkID

	seq int64 // 全局单调提交序号
}

// NewStore 创建空存储。
func NewStore() *Store {
	return &Store{
		objects: make(map[ObjectID][]ObjectRecord),
		links:   make(map[LinkID][]LinkRecord),
		out:     make(map[ObjectID][]LinkID),
	}
}

// WriteObject 为对象追加一条写入记录（新建或修正）。
//
// 修正约束：writeAt 必须严格晚于该对象已有最新记录的写入时间。
// exists=false 声明逻辑消亡（区间归一化为空），消亡后仍可被写入时间更晚的
// 记录「复活」（整体替换为新区间）。
func (s *Store) WriteObject(id ObjectID, writeAt Time, valid Interval, exists bool) error {
	if writeAt == IllegalTime {
		return ErrInvalidTime
	}
	if !valid.Valid() {
		return ErrInvalidInterval
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	recs := s.objects[id]
	if len(recs) > 0 && writeAt <= recs[len(recs)-1].WriteAt {
		return ErrOutOfOrderWrite
	}
	s.seq++
	s.objects[id] = append(recs, ObjectRecord{
		ID: id, WriteAt: writeAt, Valid: normalize(valid, exists), Seq: s.seq, Exists: exists,
	})
	return nil
}

// WriteLink 为链接追加一条写入记录（新建或修正）。
//
// 修正约束同 WriteObject。链接两端端点在生命周期内不可变：对已存在链接的
// 修正若携带不同端点将被拒绝（端点变更应建模为删旧链、建新链）。
func (s *Store) WriteLink(id LinkID, writeAt Time, valid Interval, source, target ObjectID, exists bool) error {
	if writeAt == IllegalTime {
		return ErrInvalidTime
	}
	if !valid.Valid() {
		return ErrInvalidInterval
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	recs := s.links[id]
	if len(recs) > 0 {
		if writeAt <= recs[len(recs)-1].WriteAt {
			return ErrOutOfOrderWrite
		}
		if recs[0].Source != source || recs[0].Target != target {
			return ErrInvalidInterval
		}
	}
	s.seq++
	s.links[id] = append(recs, LinkRecord{
		ID: id, WriteAt: writeAt, Valid: normalize(valid, exists), Seq: s.seq,
		Source: source, Target: target, Exists: exists,
	})
	if len(recs) == 0 {
		s.out[source] = append(s.out[source], id)
	}
	return nil
}

// normalize 保证 exists=false 的记录携带空区间 [v.Start, v.Start)，
// 使「不存在」不依赖调用方是否恰好传了空区间。
func normalize(v Interval, exists bool) Interval {
	if !exists {
		return Interval{Start: v.Start, End: v.Start}
	}
	return v
}

// resolveObject 判定对象在 (validAt, asOf) 的状态；调用方须持有 s.mu 读锁。
func (s *Store) resolveObject(id ObjectID, validAt, asOf Time) (*ObjectRecord, InvisibleReason) {
	recs := s.objects[id]
	latest := -1
	for i := range recs {
		if recs[i].WriteAt <= asOf {
			latest = i
		}
	}
	if latest >= 0 && recs[latest].Exists && recs[latest].Valid.Contains(validAt) {
		return &recs[latest], ReasonVisible
	}
	for i := range recs {
		if recs[i].WriteAt > asOf && recs[i].Exists && recs[i].Valid.Contains(validAt) {
			return nil, ReasonNotYetVisible
		}
	}
	return nil, ReasonNotEstablished
}

// resolveLink 判定链接在 (validAt, asOf) 的状态；调用方须持有 s.mu 读锁。
// 判定规则与 resolveObject 完全一致（三方共用同一套双时态语义）：
//
//  1. 写入时间 <= asOf 的最新记录存在且覆盖 validAt => visible；
//  2. 否则存在写入时间 > asOf 且覆盖 validAt 的记录 => not_yet_visible；
//  3. 否则 => not_established。
func (s *Store) resolveLink(id LinkID, validAt, asOf Time) (*LinkRecord, InvisibleReason) {
	recs := s.links[id]
	latest := -1
	for i := range recs {
		if recs[i].WriteAt <= asOf {
			latest = i
		}
	}
	if latest >= 0 && recs[latest].Exists && recs[latest].Valid.Contains(validAt) {
		return &recs[latest], ReasonVisible
	}
	for i := range recs {
		if recs[i].WriteAt > asOf && recs[i].Exists && recs[i].Valid.Contains(validAt) {
			return nil, ReasonNotYetVisible
		}
	}
	return nil, ReasonNotEstablished
}

// earliestObjectWrite 返回对象最早一条记录的写入时间；调用方须持读锁。
func (s *Store) earliestObjectWrite(id ObjectID) (Time, bool) {
	recs := s.objects[id]
	if len(recs) == 0 {
		return 0, false
	}
	return recs[0].WriteAt, true
}

// hasObject 报告对象实体是否在存储中存在（任何写入历史）；调用方须持读锁。
func (s *Store) hasObject(id ObjectID) bool {
	return len(s.objects[id]) > 0
}

// objectRef / linkRef 把命中的记录转为日志可追溯的记录引用。
func objectRef(r *ObjectRecord) RecordRef {
	return RecordRef{Kind: "object", ID: string(r.ID), WriteAt: r.WriteAt, Seq: r.Seq}
}

func linkRef(r *LinkRecord) RecordRef {
	return RecordRef{Kind: "link", ID: string(r.ID), WriteAt: r.WriteAt, Seq: r.Seq}
}

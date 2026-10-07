package ontology

import (
	"strconv"
	"sync/atomic"
)

var rebuildCounter int64

// StartRebuild 在全局锁内记录基准点：当前 seq 即 BaselineSeq。
// 对基准点对象状态做一份深拷贝快照，作为基线范围；
// 之后到达的写入（seq > BaselineSeq）由 maintainIndexLocked 追加为增量。
func (p *Platform) StartRebuild(t TypeID, a AttrName) (*RebuildHandle, *Error) {
	p.mu.Lock()
	k := indexKey{t: t, a: a}
	st, declared := p.indexes[k]
	if !declared {
		p.mu.Unlock()
		err := &Error{Kind: KindIndexNotDeclared, Msg: "未声明索引，拒绝重建"}
		p.log("start_rebuild", k, "拒绝：索引未声明，不改变任何状态", err)
		return nil, err
	}
	if st.pending != nil {
		p.mu.Unlock()
		err := &Error{Kind: KindRejectedRebuild, Msg: "已有重建进行中，拒绝重复发起"}
		p.log("start_rebuild", k, "拒绝：重复重建，不改变任何状态", err)
		return nil, err
	}

	rs := &rebuildState{
		id:          strconv.FormatInt(atomic.AddInt64(&rebuildCounter, 1), 10),
		baselineSeq: p.seq,
		built:       map[Value]map[ObjectID]entryMeta{},
	}
	if objs := p.objects[t]; objs != nil {
		for obj, cells := range objs {
			if cell, ok := cells[a]; ok {
				snap := map[AttrName]AttrCell{a: cell}
				if rs.baselineSnap == nil {
					rs.baselineSnap = map[ObjectID]map[AttrName]AttrCell{}
				}
				rs.baselineSnap[obj] = snap
			}
		}
	}

	oldAvailable := st.status == StatusAvailable
	st.status = StatusBuilding
	st.pending = rs
	p.mu.Unlock()

	p.log("start_rebuild",
		map[string]interface{}{"type": string(t), "attr": string(a), "baseline_seq": rs.baselineSeq},
		"基准点取单调序号：seq<=B 为基线，seq>B 为增量（不重不漏）；旧索引可用性="+strconv.FormatBool(oldAvailable),
		"building:"+rs.id)
	return &RebuildHandle{p: p, key: k}, nil
}

// Complete 完成重建：扫描基线快照 -> 按生效顺序重放全部增量 ->
// 生成完整审计记录（含逐条来源对应）并原子切换为可用。
// 完成时点取切换临界区内的当前 seq（包含全部已登记增量）。
func (h *RebuildHandle) Complete() (*AuditRecord, *Error) {
	p := h.p
	p.mu.Lock()
	st := p.indexes[h.key]
	if st == nil || st.pending == nil {
		p.mu.Unlock()
		return nil, &Error{Kind: KindRejectedRebuild, Msg: "重建句柄已失效（已完成或已失败）"}
	}
	rs := st.pending

	// 1) 基线范围：以基准点快照构建新索引，逐条登记来源。
	for obj, cells := range rs.baselineSnap {
		cell := cells[h.key.a]
		if !cell.Deleted {
			putBuiltEntry(rs.built, obj, cell.Value, cell.Version)
		}
	}

	// 2) 增量范围：严格按对象侧生效顺序（seq 升序）重放，顺序不得颠倒。
	for _, w := range rs.deltas {
		applyWriteToIndex(rs.built, w)
	}

	completeSeq := p.seq
	// 审计条目来自最终新索引：删除不产生条目；来源 seq<=B 归基线，否则归增量。
	entries := make([]AuditEntry, 0)
	for val, bucket := range rs.built {
		for obj, meta := range bucket {
			entries = append(entries, AuditEntry{
				Object: obj, Value: val, SourceSeq: meta.sourceSeq,
				Baseline: meta.sourceSeq <= rs.baselineSeq,
			})
		}
	}
	rec := &AuditRecord{
		RebuildID:   rs.id,
		Type:        h.key.t,
		Attr:        h.key.a,
		BaselineSeq: rs.baselineSeq,
		CompleteSeq: completeSeq,
		Entries:     entries,
	}
	rec.Digest = rec.ComputeDigest()

	// 3) 原子切换：新索引 + 完整审计记录一并可见；此后写入走在线维护。
	st.entries = rs.built
	st.audit = rec
	st.status = StatusAvailable
	st.flaggedDirty = false
	st.pending = nil
	p.mu.Unlock()

	p.log("complete_rebuild",
		map[string]interface{}{"id": rs.id, "baseline_seq": rs.baselineSeq, "complete_seq": completeSeq},
		"基线快照 + 增量按 seq 升序重放，原子切换；审计逐条带来源序号并附指纹", rec)
	return rec, nil
}

// Fail 表示重建中途失败：丢弃部分构建结果，不切换索引，整体标记不可用。
func (h *RebuildHandle) Fail() *Error {
	p := h.p
	p.mu.Lock()
	st := p.indexes[h.key]
	if st == nil || st.pending == nil {
		p.mu.Unlock()
		return &Error{Kind: KindRejectedRebuild, Msg: "重建句柄已失效"}
	}
	// 已写出的部分条目（pending.built）随 pending 一起被隔离，绝不对外提供查询。
	st.pending = nil
	st.status = StatusFailed
	p.mu.Unlock()
	p.log("fail_rebuild", map[string]string{"type": string(h.key.t), "attr": string(h.key.a)},
		"中途失败：部分条目隔离不发布，整体视为未完成，查询=重建中不可用", "failed")
	return nil
}

func putBuiltEntry(built map[Value]map[ObjectID]entryMeta, obj ObjectID, v Value, seq int64) {
	bucket := built[v]
	if bucket == nil {
		bucket = map[ObjectID]entryMeta{}
		built[v] = bucket
	}
	bucket[obj] = entryMeta{sourceSeq: seq}
}

package linkrepair

import "sort"

// 裁决优先级（固定不变，四类原因互斥）：
//
//  1. malformed_structure      记录自身结构损坏，无法解析
//  2. reference_unavailable    记录完整，但引用的对象实例本次恢复中不可用
//  3. cardinality_conflict     单一/带上限基数约束下，固定优先规则竞争失败
//  4. duplicate_record         与保留记录内容完全相同
//
// 顺序理由：
//   - 结构不完整的记录不具备引用与语义，后续检查无从进行，必须最先淘汰；
//   - 引用不可用的链接即使参与基数裁决也没有合法意义，必须在任何
//     冲突裁决之前淘汰，避免“悬空链接”赢得基数竞争；
//   - 基数冲突与重复是语义不同的两类异常：重复键在进入基数分组
//     之前先折叠，保证基数分组内每个内容只出现一次，二者永不混报；
//   - 因此每条记录的舍弃原因唯一且可区分。

// Repair 对一份损坏快照执行修复裁决。
//
// 纯函数语义：
//   - 不修改输入快照的任何字段（内部均使用拷贝与新建 map/slice）；
//   - 对同一份快照并发、反复调用，保留集合与判定依据完全一致；
//   - 结果不依赖 map 迭代顺序：分组内选择只看 (peerID, offset)，
//     输出统一按确定顺序排列。
func Repair(snap Snapshot) Result {
	validator := NewValidator(snap.LinkTypes)
	referencer := NewReferencer(snap.AvailableObjects)
	arbiter := NewArbiter(snap.LinkTypes)

	dropped := []DroppedRecord{}

	// 按原始 Offset 顺序处理，保证去重“首次出现即保留”规则确定。
	records := append([]RawRecord(nil), snap.Records...)
	sort.SliceStable(records, func(i, j int) bool {
		return records[i].Offset < records[j].Offset
	})

	candidates := make([]ScoredLink, 0, len(records))
	for _, rec := range records {
		// 第 1 级：结构有效性。
		link, ok := validator.Check(rec)
		if !ok {
			dropped = append(dropped, DroppedRecord{
				Record: rec,
				Reason: ReasonMalformed,
				Detail: malformedDetail(rec, snap.LinkTypes),
			})
			continue
		}

		// 第 2 级：引用可用性。
		if badID, ok := referencer.Resolve(link); !ok {
			dropped = append(dropped, DroppedRecord{
				Record: rec,
				Reason: ReasonRefUnavailable,
				Detail: "references object unavailable in this recovery run: " + string(badID),
			})
			continue
		}

		candidates = append(candidates, ScoredLink{Link: link, Offset: rec.Offset})
	}

	// 第 3、4 级：基数裁决与去重（内部按上述固定优先级互斥归因）。
	kept, arbDropped, arbStats := arbiter.Adjudicate(candidates)
	dropped = append(dropped, arbDropped...)
	sort.Slice(dropped, func(i, j int) bool {
		if dropped[i].Record.Offset != dropped[j].Record.Offset {
			return dropped[i].Record.Offset < dropped[j].Record.Offset
		}
		return dropped[i].Reason < dropped[j].Reason
	})

	stats := Stats{
		Input:             len(snap.Records),
		Kept:              len(kept),
		ConflictGroups:    arbStats.ConflictGroups,
		ConflictGroupWork: arbStats.ConflictGroupWork,
	}
	for _, d := range dropped {
		switch d.Reason {
		case ReasonMalformed:
			stats.Malformed++
		case ReasonRefUnavailable:
			stats.RefUnavailable++
		case ReasonCardinality:
			stats.Cardinality++
		case ReasonDuplicate:
			stats.Duplicate++
		}
	}

	return Result{Kept: kept, Dropped: dropped, Stats: stats}
}

func malformedDetail(rec RawRecord, types map[LinkTypeID]LinkType) string {
	switch {
	case rec.Type == "":
		return "link type missing or unparseable"
	case !typeKnown(types, rec.Type):
		return "unknown link type: " + string(rec.Type)
	case rec.From == "" && rec.To == "":
		return "both endpoints missing; residual record cannot be completed by guessing"
	case rec.From == "":
		return "from-endpoint missing; residual record discarded as a whole"
	case rec.To == "":
		return "to-endpoint missing; residual record discarded as a whole"
	default:
		if lt, ok := types[rec.Type]; ok && !lt.Cardinality.valid() {
			return "declared cardinality is invalid for link type: " + string(rec.Type)
		}
		return "malformed link record"
	}
}

func typeKnown(types map[LinkTypeID]LinkType, id LinkTypeID) bool {
	_, ok := types[id]
	return ok
}

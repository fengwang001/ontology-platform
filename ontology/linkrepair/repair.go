package linkrepair

import "sort"

// Repair 对损坏快照中的链接记录执行修复裁决。
//
// 该函数是纯函数：不修改输入，不依赖任何可变全局状态，
// 对同一份快照并发或重复调用得到完全一致的结果。
func Repair(snap Snapshot) Report {
	verdicts := make([]Verdict, len(snap.Records))
	for i := range verdicts {
		verdicts[i] = Verdict{Position: i, RecordID: snap.Records[i].ID}
	}

	val := newValidator(snap.LinkTypes)
	refs := newReferenceChecker(snap.AvailableObjects)
	arb := newArbiter(snap.LinkTypes)

	// 阶段 1、2 逐条判定；只有全部通过的记录才进入阶段 3。
	// 阶段顺序即舍弃原因的固定优先级：结构 -> 引用 -> 基数 -> 重复。
	candidates := make([]candidateRecord, 0, len(snap.Records))
	for i, rec := range snap.Records {
		if detail := val.malformedDetail(rec); detail != "" {
			verdicts[i] = Verdict{
				Position: i, RecordID: rec.ID,
				Reason: ReasonMalformed, Detail: detail,
			}
			continue
		}
		if detail := refs.unavailableEnds(rec); detail != "" {
			verdicts[i] = Verdict{
				Position: i, RecordID: rec.ID,
				Reason: ReasonReferencedUnavailable, Detail: detail,
			}
			continue
		}
		candidates = append(candidates, candidateRecord{position: i, record: rec})
	}

	// 阶段 3：去重与基数裁决。
	keptSet, dropped := arb.arbitrate(candidates)

	kept := make([]RawRecord, 0, len(keptSet))
	for _, c := range candidates {
		if _, ok := keptSet[c.position]; ok {
			verdicts[c.position] = Verdict{
				Position: c.position, RecordID: c.record.ID,
				Kept: true, Reason: ReasonNone, Detail: "kept",
			}
			kept = append(kept, c.record)
			continue
		}
		outcome := dropped[c.position]
		verdicts[c.position] = Verdict{
			Position: c.position, RecordID: c.record.ID,
			Reason: outcome.reason, Detail: outcome.detail,
		}
	}

	// 输出按内容键确定性排序，保证重复/并发执行结果字节级一致。
	sort.Slice(kept, func(i, j int) bool {
		a, b := kept[i], kept[j]
		if a.LinkTypeID != b.LinkTypeID {
			return a.LinkTypeID < b.LinkTypeID
		}
		if a.SourceID != b.SourceID {
			return a.SourceID < b.SourceID
		}
		if a.TargetID != b.TargetID {
			return a.TargetID < b.TargetID
		}
		return a.ID < b.ID
	})

	return Report{Kept: kept, Verdicts: verdicts}
}

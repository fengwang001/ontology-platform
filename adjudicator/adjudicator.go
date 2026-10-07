package adjudicator

import (
	"fmt"
	"slices"
)

// Adjudicator 裁决组件入口。它无任何可变状态：
// 每次裁决都是输入快照的纯函数，因此并发调用安全且结果一致。
type Adjudicator struct{}

// New 构造一个裁决器。构造后不持有任何状态。
func New() Adjudicator {
	return Adjudicator{}
}

// Adjudicate 对一批备份快照做完整裁决，输出唯一确定的重建顺序、
// 每条记录的可重建范围判定与判定依据。不会修改快照。
func (Adjudicator) Adjudicate(s *Snapshot) Verdict {
	idx := BuildIndex(s)
	ev := newEvaluator(idx)
	res := ev.evaluate(nil)

	refs := make([]RecordRef, 0, len(idx.records))
	for ref := range idx.records {
		refs = append(refs, ref)
	}
	slices.SortFunc(refs, compareRefs)

	v := Verdict{}
	rebuildableSet := make(map[RecordRef]bool, len(res.rebuildable))
	for _, ref := range refs {
		rv, entry := adjudicateOne(ev, res, ref)
		v.Verdicts = append(v.Verdicts, rv)
		v.Log = append(v.Log, entry)
		rebuildableSet[ref] = rv.Rebuildable
	}

	order, cycle := resolveOrder(idx.records, rebuildableSet)
	v.Order = order
	if len(cycle) > 0 {
		// 兜底：evaluator 未识别出的循环在这里修正判定。
		for i := range v.Verdicts {
			if slices.Contains(cycle, v.Verdicts[i].Ref) {
				v.Verdicts[i].Rebuildable = false
				v.Verdicts[i].Reason = ReasonCycle
			}
		}
	}
	v.Err = classifyError(s, v.Verdicts)
	return v
}

// CheckRecord 只判定单条记录是否可重建。
// 开销只与该记录自身的依赖链长度相关，与备份总规模无关。
func (Adjudicator) CheckRecord(s *Snapshot, ref RecordRef) RecordVerdict {
	return Adjudicator{}.CheckRecordIndexed(BuildIndex(s), ref)
}

// CheckRecordIndexed 在预先构建的索引上判定单条记录是否可重建。
// 索引一次构建可重复使用，单次判定的开销只沿该记录的依赖链传播，
// 与备份总规模无关。
func (Adjudicator) CheckRecordIndexed(idx *Index, ref RecordRef) RecordVerdict {
	if _, ok := idx.records[ref]; !ok {
		// 记录不在备份中：等价于该记录自身不可恢复。
		reason := ReasonSelfDamaged
		if !idx.scopes[int(ref.Category)].available {
			reason = ReasonCategoryUnavailable
		}
		return RecordVerdict{Ref: ref, Rebuildable: false, Reason: reason, BlockedBy: []RecordRef{ref}}
	}
	ev := newEvaluator(idx)
	res := ev.evaluate([]RecordRef{ref})
	rv, _ := adjudicateOne(ev, res, ref)
	return rv
}

// adjudicateOne 把依赖核对结果翻译成单条记录的判定结论与判定依据。
func adjudicateOne(ev *evaluator, res evalResult, ref RecordRef) (RecordVerdict, DecisionEntry) {
	sc := ev.idx.scopes[int(ref.Category)]
	rv := RecordVerdict{Ref: ref}
	entry := DecisionEntry{Ref: ref}
	switch {
	case !sc.available:
		rv.Reason = ReasonCategoryUnavailable
		rv.BlockedBy = []RecordRef{ref}
		entry.Detail = fmt.Sprintf("类别 %s 整体缺失或整体损坏，本类别全部记录不可重建", ref.Category)
	case !sc.recoverable[ref.ID]:
		rv.Reason = ReasonSelfDamaged
		rv.BlockedBy = []RecordRef{ref}
		entry.Detail = "记录自身在备份中损坏，不可恢复"
	case res.cyclic[ref]:
		rv.Reason = ReasonCycle
		rv.BlockedBy = []RecordRef{ref}
		entry.Detail = "记录处于循环依赖中，重建顺序无法确定"
	case res.rebuildable[ref]:
		rv.Rebuildable = true
		rv.Reason = ReasonOK
		entry.Detail = "自身可恢复且全部依赖均可重建"
	default:
		rv.Reason = ReasonDependencyUnavailable
		rv.BlockedBy = res.blockedBy[ref]
		entry.Detail = fmt.Sprintf("依赖不可重建: %v", rv.BlockedBy)
	}
	entry.Verdict = rv.Rebuildable
	entry.Reason = rv.Reason
	return rv, entry
}

// classifyError 按固定优先级归类裁决层面的错误：
// 整体不可用 > 部分不可用级联 > 循环依赖。
// 中途新发现的损坏只发生在执行阶段，由 Session 报告，不在此处出现。
func classifyError(s *Snapshot, verdicts []RecordVerdict) *Error {
	for c := 0; c < categoryCount; c++ {
		if !s.Backups[c].Available() {
			return &Error{
				Kind:     ErrKindCategoryUnavailable,
				Category: Category(c),
				Msg:      "该类别及其全部下游依赖均不可重建",
			}
		}
	}
	for _, rv := range verdicts {
		if rv.Reason == ReasonSelfDamaged || rv.Reason == ReasonDependencyUnavailable {
			return &Error{
				Kind: ErrKindCascade,
				Refs: []RecordRef{rv.Ref},
				Msg:  "部分备份损坏沿依赖链级联，部分记录不可重建",
			}
		}
	}
	var cyclic []RecordRef
	for _, rv := range verdicts {
		if rv.Reason == ReasonCycle {
			cyclic = append(cyclic, rv.Ref)
		}
	}
	if len(cyclic) > 0 {
		return &Error{
			Kind: ErrKindCycle,
			Refs: cyclic,
			Msg:  "可重建子图存在循环依赖，重建顺序无法确定",
		}
	}
	return nil
}

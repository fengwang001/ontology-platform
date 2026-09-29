package twophase

import "fmt"

// RejectKind 描述一次写入或提交被整体拒绝的具体原因。
type RejectKind string

const (
	// RejectInvalidSeq 序号非法（小于等于 0，或小于等于已提交位点）。
	RejectInvalidSeq RejectKind = "invalid_seq"
	// RejectOutOfOrder 越序：写入序号不等于已提交位点 + 1。
	RejectOutOfOrder RejectKind = "out_of_order"
	// RejectGap 位点跳跃：提交序号不等于已提交位点 + 1。
	RejectGap RejectKind = "gap"
	// RejectEffectMissing 缺效果：提交时序号对应的副作用尚未写入。
	RejectEffectMissing RejectKind = "effect_missing"
	// RejectEffectConflict 重复写入时新副作用与已写副作用不一致。
	RejectEffectConflict RejectKind = "effect_conflict"
)

// RejectError 携带可区分的拒绝原因，一次失败不会改变副作用存储与位点。
type RejectError struct {
	Kind     RejectKind
	Seq      int64
	Position int64
	Detail   string
}

func (e *RejectError) Error() string {
	return fmt.Sprintf("twophase: reject seq=%d position=%d kind=%s: %s",
		e.Seq, e.Position, e.Kind, e.Detail)
}

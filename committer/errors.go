package committer

import "fmt"

// ErrKind 区分两阶段提交失败的可判定原因。
type ErrKind int

const (
	// ErrInvalidSeq 非法序号：序号非正，或不大于已提交位点（重复/回退）。
	ErrInvalidSeq ErrKind = iota
	// ErrOutOfOrder 越序写入：副作用写入序号大于已提交位点加一。
	ErrOutOfOrder
	// ErrMissingEffect 缺效果提交：提交序号合法但其副作用尚未写入。
	ErrMissingEffect
	// ErrPositionJump 位点跳跃：提交序号大于已提交位点加一。
	ErrPositionJump
)

func (k ErrKind) String() string {
	switch k {
	case ErrInvalidSeq:
		return "非法序号"
	case ErrOutOfOrder:
		return "越序写入"
	case ErrMissingEffect:
		return "缺效果提交"
	case ErrPositionJump:
		return "位点跳跃"
	default:
		return "未知错误"
	}
}

// Error 携带可区分的失败原因与上下文，且失败时不改变任何状态。
type Error struct {
	Kind     ErrKind
	Seq      int64
	Position int64
	Detail   string
}

func (e *Error) Error() string {
	return fmt.Sprintf("%s: seq=%d position=%d %s", e.Kind, e.Seq, e.Position, e.Detail)
}

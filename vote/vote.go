// Package vote 实现两阶段提交第一阶段的投票收集与裁决。
//
// 本包不依赖工程内其他包。裁决规则：
//   - 所有已注册参与者都投同意票 => Commit
//   - 任一参与者投否决票 => Abort（原因 ReasonRejected）
//   - 截止时刻（左闭右开，now >= deadline 即超时）仍有参与者未回复
//     时按否决处理 => Abort（原因 ReasonTimeout）
//
// 决议一旦写下不可更改，迟到的投票不会改变结果。
package vote

// Decision 是事务的最终决议。
type Decision int

const (
	// Undecided 表示尚未裁决，是 Decision 的零值。
	Undecided Decision = iota
	// Commit 表示全票同意，进入第二阶段提交。
	Commit
	// Abort 表示任一否决或超时，全体中止。
	Abort
)

func (d Decision) String() string {
	switch d {
	case Commit:
		return "Commit"
	case Abort:
		return "Abort"
	default:
		return "Undecided"
	}
}

// Reason 区分决议（尤其是中止）的原因。
type Reason int

const (
	// ReasonNone 表示无特别原因（未裁决或正常全票提交）。
	ReasonNone Reason = iota
	// ReasonRejected 表示有参与者投了否决票。
	ReasonRejected
	// ReasonTimeout 表示有参与者在截止时刻前未回复。
	ReasonTimeout
)

func (r Reason) String() string {
	switch r {
	case ReasonRejected:
		return "Rejected"
	case ReasonTimeout:
		return "Timeout"
	default:
		return "None"
	}
}

// Outcome 是一次裁决结果。零值表示「尚未裁决」，绝不用于猜测。
type Outcome struct {
	Decision Decision
	Reason   Reason
	// Culprit 指出导致中止的参与者 ID（否决者或超时者）；
	// 提交或尚未裁决时为空字符串。
	Culprit string
}

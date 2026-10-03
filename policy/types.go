// Package policy 只包含纯判定逻辑：给定复用策略、冲突策略与现有记录，得出启动结论。
package policy

// State 是实例状态。
type State int

const (
	Running State = iota
	Completed
	Failed
	Cancelled
	Terminated
)

// ReusePolicy 是复用策略。
type ReusePolicy int

const (
	AllowAll ReusePolicy = iota
	AllowFailedOnly
	Reject
)

// ConflictPolicy 是冲突策略。
type ConflictPolicy int

const (
	Fail ConflictPolicy = iota
	UseExisting
	Terminate
)

// Existing 描述一条现有存活记录。
type Existing struct {
	Run   uint64
	Owner []byte
	State State
}

// Verdict 是纯判定结论。
type Verdict int

const (
	VerdictNew Verdict = iota
	VerdictRejectRunning
	VerdictUseExisting
	VerdictTerminateOK
	VerdictRejectReuse
	VerdictReplace
)

// Decide 依据 reuse、conflict 与现有记录得出纯判定结论。
//
// 无存活记录时返回 VerdictNew（容量检查由调用方负责）。
// Running + Terminate 返回 VerdictTerminateOK；该路径绕过 reuse，
// 权限（ErrDenied）由调用方查 acl，policy 不持有权限表，保持纯判定。
func Decide(exists bool, e Existing, reuse ReusePolicy, conflict ConflictPolicy) Verdict {
	if !exists {
		return VerdictNew
	}
	if e.State == Running {
		switch conflict {
		case Fail:
			return VerdictRejectRunning
		case UseExisting:
			return VerdictUseExisting
		case Terminate:
			return VerdictTerminateOK
		}
	}
	switch reuse {
	case AllowAll:
		return VerdictReplace
	case AllowFailedOnly:
		if e.State == Failed || e.State == Cancelled || e.State == Terminated {
			return VerdictReplace
		}
		return VerdictRejectReuse
	case Reject:
		return VerdictRejectReuse
	}
	return VerdictRejectReuse
}

// ValidReuse 报告 reuse 是否为合法枚举值。
func ValidReuse(r ReusePolicy) bool { return r >= AllowAll && r <= Reject }

// ValidConflict 报告 conflict 是否为合法枚举值。
func ValidConflict(c ConflictPolicy) bool { return c >= Fail && c <= Terminate }

// ValidFinishState 报告 state 是否可用于 Finish（仅 Completed/Failed/Cancelled）。
func ValidFinishState(s State) bool { return s >= Completed && s <= Cancelled }

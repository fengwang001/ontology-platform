package approval

import "time"

// Vote 表示一次表态的意见。
type Vote int

const (
	VoteApprove Vote = iota + 1 // 同意
	VoteReject                  // 否决
)

func (v Vote) String() string {
	switch v {
	case VoteApprove:
		return "同意"
	case VoteReject:
		return "否决"
	default:
		return "未知"
	}
}

// Terminal 表示流程终局类型。
type Terminal int

const (
	TerminalApproved  Terminal = iota + 1 // 通过
	TerminalRejected                      // 驳回
	TerminalTimedOut                      // 超时
	TerminalWithdrawn                     // 撤回
)

func (t Terminal) String() string {
	switch t {
	case TerminalApproved:
		return "通过"
	case TerminalRejected:
		return "驳回"
	case TerminalTimedOut:
		return "超时"
	case TerminalWithdrawn:
		return "撤回"
	default:
		return "未终局"
	}
}

// Stage 描述一个会签阶段：审批人集合、所需同意数 k、截止时刻。
type Stage struct {
	Name      string
	Approvers []string
	Required  int
	Deadline  time.Time
}

// RejectCode 是被拒绝操作的原因码，按错误优先级定义。
type RejectCode int

const (
	RejectNone RejectCode = iota
	RejectFlowNotFound
	RejectAlreadyTerminal
	RejectNotParticipant
	RejectDelegatorVoting
	RejectDuplicateVote
	RejectDelegateToSelf
	RejectDelegateeIsMember
	RejectDelegateeRedelegating
	RejectVoteAfterDelegation
	RejectAlreadyDelegated
)

func (c RejectCode) String() string {
	switch c {
	case RejectFlowNotFound:
		return "流程不存在"
	case RejectAlreadyTerminal:
		return "流程已终局"
	case RejectNotParticipant:
		return "既非当前阶段审批人也非其受托人"
	case RejectDelegatorVoting:
		return "已委托者本人不得再表态"
	case RejectDuplicateVote:
		return "重复表态"
	case RejectDelegateToSelf:
		return "不能委托给自己"
	case RejectDelegateeIsMember:
		return "受托人不得是本阶段成员或已受托者"
	case RejectDelegateeRedelegating:
		return "受托人不得再转委托"
	case RejectVoteAfterDelegation:
		return "已表态后不得再委托"
	case RejectAlreadyDelegated:
		return "该审批人已委托，不能重复委托"
	default:
		return ""
	}
}

// Clock 为引擎注入时间来源。
type Clock interface {
	Now() time.Time
}

// Event 是追加式事件日志中的一条记录。
type Event struct {
	Seq    int
	Time   time.Time
	FlowID string
	Kind   string
	Detail string
}

// Result 是一次操作的返回结果。
type Result struct {
	OK         bool
	Terminal   Terminal
	RejectCode RejectCode
	Reason     string
}

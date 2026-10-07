package estimation

import (
	"fmt"
	"strconv"
)

// 参数合法域。
const (
	MinDeckSize = 2
	MaxDeckSize = 20
	MinRounds   = 1
	MaxRounds   = 5
	MinRoundTTL = 1
	MaxRoundTTL = 86400
	// MaxNow 为 now 的上界（0 到 10^12 的整数秒）。
	MaxNow = int64(1_000_000_000_000)
)

// Card 表示一张牌。数值牌为正整数；两张特殊牌使用负值哨兵，
// 与一切合法数值牌天然不相交。
type Card int32

const (
	CardUnsure Card = -1 // 不确定
	CardBreak  Card = -2 // 需要休息
)

// Special 报告该牌是否为特殊牌（不参与统计）。
func (c Card) Special() bool { return c == CardUnsure || c == CardBreak }

func (c Card) String() string {
	switch c {
	case CardUnsure:
		return "unsure"
	case CardBreak:
		return "break"
	}
	return strconv.Itoa(int(c))
}

// Role 为成员角色。
type Role int

const (
	RoleVoter Role = iota
	RoleObserver
)

// Valid 报告角色是否合法。
func (r Role) Valid() bool { return r == RoleVoter || r == RoleObserver }

func (r Role) String() string {
	if r == RoleVoter {
		return "voter"
	}
	if r == RoleObserver {
		return "observer"
	}
	return fmt.Sprintf("role(%d)", int(r))
}

// Phase 为议题状态机的阶段。
type Phase int

const (
	PhaseIdle     Phase = iota // 无进行中议题（初始或议题已结束）
	PhaseVoting                // 某一轮投票阶段
	PhaseRevealed              // 已揭示但未终局（分歧/无有效票且轮次未尽）
)

func (p Phase) String() string {
	switch p {
	case PhaseIdle:
		return "idle"
	case PhaseVoting:
		return "voting"
	case PhaseRevealed:
		return "revealed"
	}
	return fmt.Sprintf("phase(%d)", int(p))
}

// ResultKind 为一次揭示的结果类别。
type ResultKind int

const (
	ResultConsensus     ResultKind = iota + 1 // 共识：全体数值牌相同
	ResultConverged                           // 收敛：最大最小在牌组中相邻，取较大者
	ResultDiverged                            // 分歧：其余情况且轮次未尽
	ResultNoValidVotes                        // 无有效票：无数值牌且轮次未尽
	ResultForced                              // 强制取值：分歧且轮次用尽，取下中位
	ResultFinalNoResult                       // 终局无结果：无有效票且轮次用尽
)

func (k ResultKind) String() string {
	switch k {
	case ResultConsensus:
		return "consensus"
	case ResultConverged:
		return "converged"
	case ResultDiverged:
		return "diverged"
	case ResultNoValidVotes:
		return "no_valid_votes"
	case ResultForced:
		return "forced"
	case ResultFinalNoResult:
		return "final_no_result"
	}
	return fmt.Sprintf("result(%d)", int(k))
}

// EndsIssue 报告该结果是否使议题结束。
func (k ResultKind) EndsIssue() bool {
	switch k {
	case ResultConsensus, ResultConverged, ResultForced, ResultFinalNoResult:
		return true
	}
	return false
}

// RevealTrigger 记录一次揭示的触发方式。
type RevealTrigger int

const (
	TriggerManual  RevealTrigger = iota + 1 // 主持人 Reveal
	TriggerAuto                             // 自动揭示条件成立
	TriggerExpired                          // 时限到期（惰性处理）
)

func (t RevealTrigger) String() string {
	switch t {
	case TriggerManual:
		return "manual"
	case TriggerAuto:
		return "auto"
	case TriggerExpired:
		return "expired"
	}
	return fmt.Sprintf("trigger(%d)", int(t))
}

// RevealOutcome 为一次揭示的完整结果。任何触发揭示的操作
// （Reveal、自动揭示、到期惰性揭示）都必须返回它。
type RevealOutcome struct {
	Round        int           // 第几轮（从 1 开始）
	RevealedAt   int64         // 揭示时刻（到期揭示为到期时刻本身）
	Trigger      RevealTrigger // 触发方式
	Distribution map[Card]int  // 牌的分布（含特殊牌，仅含非零项）
	NumericVotes int           // 参与统计的票数（数值牌张数）
	Kind         ResultKind    // 结果类别
	Value        int           // 取值（共识/收敛/强制取值时有效）
	HasValue     bool          // Value 是否有效
	IssueEnded   bool          // 议题是否因此结束
}

// PeekView 为 Peek 的返回视图：只含本人投票与全体已投人数，
// 不泄露任何他人的牌。
type PeekView struct {
	HasVote    bool // 本人当前是否有已投的票
	Self       Card // 本人已投的牌（HasVote 为真时有效）
	VotedCount int  // 全体已投人数
}

// Status 为会话的可观测状态快照，不含任何他人的牌。
type Status struct {
	Phase      Phase
	Round      int   // 当前轮次（从 1 开始；无议题时为 0）
	RoundStart int64 // 当前轮开始时刻
	Deadline   int64 // 当前轮到期时刻（仅 Voting 阶段有意义）
	Voters     int   // 在室投票者人数
	Voted      int   // 已投人数
	LastNow    int64 // 上一次被接受操作的 now
}

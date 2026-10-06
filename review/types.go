package review

import (
	"fmt"
	"time"
)

func sprintf(format string, args ...any) string { return fmt.Sprintf(format, args...) }

// Choice 是一轮投票中某位评委的唯一选择。
type Choice int

const (
	Approve Choice = 1 // 赞成
	Oppose  Choice = 2 // 反对
	Abstain Choice = 3 // 弃权（计入总人数，不计赞成）
)

// Reviewer 评委库中的评委。
type Reviewer struct {
	ID    int    // 评委编号（正整数，库内唯一）
	Unit  string // 所属单位
	Group string // 专业组
}

// Applicant 申报人。
type Applicant struct {
	ID   int    // 申报人编号（正整数，唯一）
	Unit string // 所属单位
}

// Outcome 一轮结算后的结论。
type Outcome int

const (
	OutcomeNone       Outcome = 0 // 未结算
	OutcomePass       Outcome = 1 // 通过
	OutcomeFail       Outcome = 2 // 不通过
	OutcomeReconsider Outcome = 3 // 进入复议
)

// ReviewStatus 评审生命周期状态。
type ReviewStatus int

const (
	StatusVoting    ReviewStatus = 1 // 投票中（含复议投票）
	StatusPublicity ReviewStatus = 2 // 公示中
	StatusFinalPass ReviewStatus = 3 // 终局：通过
	StatusFinalFail ReviewStatus = 4 // 终局：不通过
	StatusVoid      ReviewStatus = 5 // 评审整体作废（公示异议成立）
	StatusAborted   ReviewStatus = 6 // 评审中止（无法替补）
)

// RoundResult 是一次轮次结算的不可变留痕记录。
type RoundResult struct {
	Round      int       // 轮次（1=第一轮，2=复议）
	Version    int       // 结算所依据的评委构成版本
	Total      int       // 该版本评委总人数
	Approve    int       // 有效赞成票数
	Oppose     int       // 有效反对票数
	Abstain    int       // 有效弃权票数
	Outcome    Outcome   // 结算结论
	DecidedAt  time.Time // 结算时刻
	Superseded bool      // 是否因后续评委退出、票作废而被推翻
	Reason     string    // 判定依据的可复算说明
}

// VoteRecord 是每位评委每一票的可查询留痕。
type VoteRecord struct {
	ReviewID     int
	ReviewerID   int
	Round        int
	Choice       Choice
	VotedAt      time.Time
	PanelVersion int  // 投票时的评委构成版本
	Voided       bool // 该票是否因评委回避退出而作废
}

// PanelVersion 描述一次评委构成版本（按替补次序不可变保存）。
type PanelVersion struct {
	Version   int   // 版本号，自 1 起
	Reviewers []int // 该版本全体评委编号，按编号升序
	CreatedAt time.Time
	Reason    string // "抽取" 或 "回避替补"
	Left      []int  // 因回避退出的评委（首个版本为空）
}

// ReviewView 是评审状态的只读快照。
type ReviewView struct {
	ID            int
	ApplicantID   int
	Status        ReviewStatus
	N             int
	MinGroups     map[string]int
	CurrentRound  int           // 当前接收投票的轮次（1 或 2）
	Version       int           // 当前评委构成版本号
	Panel         []int         // 当前评委编号（升序）
	History       []RoundResult // 全部结算留痕（含被推翻的）
	AnnouncedAt   time.Time     // 宣布时刻（公示起点）
	PublicityEnd  time.Time     // 公示结束时刻（宣布后整七个自然日）
	Objection     bool          // 是否已受理异议
	ObjectionTime time.Time
}

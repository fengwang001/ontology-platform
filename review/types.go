package review

import "time"

// PublicityDuration 公示期：自结果宣布时刻起整整七个自然日。
const PublicityDuration = 7 * 24 * time.Hour

// Choice 投票选项。弃权计入总人数但不计赞成。
type Choice int

const (
	ChoiceApprove Choice = iota + 1
	ChoiceReject
	ChoiceAbstain
)

func (c Choice) valid() bool { return c >= ChoiceApprove && c <= ChoiceAbstain }

// Outcome 一轮表决或一次评审的结论。
type Outcome int

const (
	OutcomePass Outcome = iota + 1
	OutcomeFail
	OutcomeRevote
)

func (o Outcome) String() string {
	switch o {
	case OutcomePass:
		return "pass"
	case OutcomeFail:
		return "fail"
	case OutcomeRevote:
		return "revote"
	}
	return "unknown"
}

// Status 评审生命周期状态。
type Status int

const (
	// StatusActive 评审进行中（尚无生效的最终结论）。
	StatusActive Status = iota + 1
	// StatusAnnounced 结果已宣布，公示期内（非终局）。
	StatusAnnounced
	// StatusEffective 公示结束且异议不成立或无异议，结果终局生效。
	StatusEffective
	// StatusVoid 异议成立，评审整体作废。
	StatusVoid
	// StatusAborted 无法替补，评审中止。
	StatusAborted
)

func (s Status) String() string {
	switch s {
	case StatusActive:
		return "active"
	case StatusAnnounced:
		return "announced"
	case StatusEffective:
		return "effective"
	case StatusVoid:
		return "void"
	case StatusAborted:
		return "aborted"
	}
	return "unknown"
}

// Expert 评委库成员。
type Expert struct {
	ID    int64
	Unit  string
	Group string
}

// Applicant 申报人及其回避相关索引（全部为 O(1) 查找的集合，
// 使抽取/替补开销不随历史评审总数增长）。
type Applicant struct {
	ID   int64
	Unit string
	// Relations 已登记的亲属/师生关系（对称、仅直接关系），值为评委编号。
	Relations map[int64]bool
	// RecusalPending 已提出但未受理的回避申请。
	RecusalPending map[int64]bool
	// RecusalAccepted 已被受理的回避申请。
	RecusalAccepted map[int64]bool
	// VoidedExperts 在该申报人以往已作废评审中任过评委的评委编号。
	VoidedExperts map[int64]bool
}

// PanelVersion 评委构成版本。每次替补形成一个新版本，历史版本只增不改。
type PanelVersion struct {
	Index   int
	Members []int64
	Reason  string
	At      time.Time
}

// VoteRecord 一票的完整记录，含投票时的评委构成版本。
// VoidedAt 为 -1 表示有效；否则为该票作废时形成的版本号。
type VoteRecord struct {
	ExpertID int64
	Round    int
	Choice   Choice
	Version  int
	At       time.Time
	VoidedAt int
}

// TrailEntry 结算留痕。被取代的条目标记 Superseded，但永不删除。
type TrailEntry struct {
	At         time.Time
	Round      int
	Outcome    Outcome
	Final      bool
	Version    int
	Superseded bool
}

// Objection 公示期异议。受理、裁定至多各发生一次。
type Objection struct {
	Reason  string
	FiledAt time.Time
	Ruled   bool
	Upheld  bool
	RuledAt time.Time
}

// Review 一次职称评审。
type Review struct {
	ID          int64
	ApplicantID int64
	N           int
	GroupMins   map[string]int
	Versions    []*PanelVersion
	Votes       []*VoteRecord
	Trail       []*TrailEntry
	Objection   *Objection
	Aborted     bool
	Voided      bool
	rounds      int
}

func (r *Review) members() []int64 { return r.Versions[len(r.Versions)-1].Members }

// finalEntry 返回当前生效（未被取代）的最终结论条目，无则返回 nil。
func (r *Review) finalEntry() *TrailEntry {
	for i := len(r.Trail) - 1; i >= 0; i-- {
		if r.Trail[i].Final && !r.Trail[i].Superseded {
			return r.Trail[i]
		}
	}
	return nil
}

// Stats 用于验证抽取/替补开销不随历史评审总数增长。
type Stats struct {
	RecusalChecks int64
}

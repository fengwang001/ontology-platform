package ontology

// MemberInput 登记一个成员。金额单位为非负整数分。
type MemberInput struct {
	ID            string
	AnnualLimit   int64 // 个人年度限额
	LifetimeLimit int64 // 个人终身限额
}

// ItemInput 登记一个赔付项目。金额单位为非负整数分。
type ItemInput struct {
	ID          string
	AnnualLimit int64 // 个人年度项目限额
}

// PolicyInput 登记一张保单。
// 保单年度为左闭右开区间 [StartDay+k*YearLength, StartDay+(k+1)*YearLength)。
type PolicyInput struct {
	ID                string
	StartDay          int64 // 承保起始日，非负整数天
	YearLength        int64 // 年度长度，正整数天
	FamilyAnnualLimit int64 // 家庭共享年度限额
	Members           []MemberInput
	Items             []ItemInput
}

// LineInput 是一条费用明细，金额为正整数分。
type LineInput struct {
	ItemID string
	Day    int64 // 发生日
	Amount int64
}

// ClaimInput 是一笔理赔。
type ClaimInput struct {
	ID       string
	MemberID string
	Lines    []LineInput
}

// EndorseKind 标识批改作用的对象层。
type EndorseKind int

const (
	EndorseMemberAnnual EndorseKind = iota + 1 // 某成员的个人年度限额
	EndorseItemAnnual                          // 某项目的年度项目限额
	EndorseFamilyAnnual                        // 家庭共享年度限额
)

// Snapshot 是某成员在某保单年度各层剩余额的只读快照。
type Snapshot struct {
	MemberAnnualRemaining int64
	ItemAnnualRemaining   int64 // 指定项目的年度项目剩余额
	FamilyAnnualRemaining int64
	LifetimeRemaining     int64
	Capped                bool // 是否已封顶
}

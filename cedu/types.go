package cedu

// Category 为学分类别。
type Category int

const (
	Required Category = iota // 必修类
	Elective                 // 选修类
	Online                   // 线上类
)

func (c Category) valid() bool { return c >= Required && c <= Online }

// Config 为服务级全局参数，所有持证人共享。
type Config struct {
	CycleLength   int // 周期长度（日），正整数
	TotalRequired int // 每周期总学分要求，正整数
	RequiredMin   int // 每周期必修最低计入量，正整数
	RequiredCap   int // 必修类计入上限，正整数
	ElectiveCap   int // 选修类计入上限，正整数
	GraceDays     int // 宽限天数，非负整数
	CorrectDays   int // 更正/撤销期限（自登记日起，含两端），非负整数
	CarryoverCap  int // 结转上限，非负整数
}

// RegisterInput 为注册持证人输入。
type RegisterInput struct {
	HolderID  string
	IssueDate int // 发证日（整数日序号）
	Now       int
}

// CreditInput 为登记学分记录输入。
type CreditInput struct {
	HolderID string
	Category Category
	Credits  int    // 正整数
	EarnedOn int    // 取得日，不得晚于 Now
	Org      string // 出具机构
	Now      int
}

// Record 为一条学分记录。
type Record struct {
	ID       string
	HolderID string
	Category Category
	Credits  int
	EarnedOn int
	Org      string
	RegAt    int // 登记时刻（操作的 now）
	Revoked  bool

	cycle int  // 归属周期序号
	zone  bool // true=取得日位于归属周期宽限带（候选），false=周期本体
}

// CycleStatus 为周期核算快照。
type CycleStatus struct {
	Index      int
	Start      int
	End        int // 左闭右开，实际末日为 End-1
	GraceEnd   int // 宽限满日（该日起宽限期外）
	RequiredIn int // 必修计入量
	ElectiveIn int // 选修计入量（含结转学分）
	OnlineIn   int // 线上计入量
	TotalIn    int // 总计入量
	CarryIn    int // 自上周期结转的学分量
	CarryOut   int // 结转到下一周期的学分数
	Met        bool
	InGrace    bool
}

// HolderStatus 为持证人整体状态快照。
type HolderStatus struct {
	HolderID     string
	IssueDate    int
	Active       bool
	Expired      bool
	CurrentCycle CycleStatus
	AsOf         int // 快照对应的时钟时刻
}

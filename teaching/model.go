package teaching

// ScaleTier 规模档位：班级人数处于 (UpperOfPrev, MaxSize] 时命中本档。
// 边界取等归高档：人数恰为某档 MaxSize 时命中该档（较高档）。
// Coeff 为整数百分数，例如 110 表示 1.10。
type ScaleTier struct {
	MaxSize int
	Coeff   int
}

// Rank 职级：决定单学期折算工作量下限与上限。
type Rank struct {
	Name    string
	MinLoad int
	MaxLoad int
}

// Config 为服务初始化配置。所有系数均为整数百分数。
type Config struct {
	Tiers        []ScaleTier // 按 MaxSize 升序，且无重叠
	NewCourseAdd int         // 新开课加成（加到规模系数上），百分数
	LabCoeff     int         // 实验课系数，百分数
	Ranks        map[string]Rank
	ConfirmTicks int64 // 待确认指派的确认期限（时钟单位）；恰等于期限仍有效
}

// TaskSpec 课程任务定义。每周固定占用 Periods 个节次。
type TaskSpec struct {
	ID        string
	Semester  string
	Hours     int   // 总学时
	ClassSize int   // 班级规模
	NewCourse bool  // 是否新开课
	IsLab     bool  // 是否实验课
	WeekStart int   // 起始周（含，闭区间）
	WeekEnd   int   // 结束周（含，闭区间）
	Periods   []int // 每周固定节次，互不相同
}

// AssignmentReq 单条指派请求（合上时同一任务给出多条，教师不同）。
type AssignmentReq struct {
	TeacherID string
	TaskID    string
	Hours     int // 该教师承担的整数学时
}

// AssignmentView 指派状态快照（供测试与朴素模型对照）。
type AssignmentView struct {
	TeacherID string
	TaskID    string
	Hours     int
	Status    string // pending | confirmed | rejected | released
	Deadline  int64  // pending 的超时时刻；其余为 0
	WeekStart int    // 当前承担段起始周（换人后为换入周）
	WeekEnd   int
}

// SettlementResult 学期核算结果（幂等：重复核算返回相同结果）。
type SettlementResult struct {
	TeacherID      string
	Semester       string
	Load           int // 已生效折算工作量（核算时待确认项被惰性释放）
	MinLoad        int
	Shortfall      int // 核算冲抵前欠额
	Excess         int // 超额
	CreditEarned   int // 本学期挣得、可结转下一学期的抵扣额度（已施加 1/4 下限封顶）
	CreditUsed     int // 用上学期结转额度冲抵掉的欠额
	Unmet          int // 冲抵后仍为正的欠额（未达标）
	CreditIncoming int // 上学期实际结转进入本学期的额度
}

// 指派状态常量。
const (
	StatusPending   = "pending"
	StatusConfirmed = "confirmed"
	StatusRejected  = "rejected"
	StatusReleased  = "released"
)

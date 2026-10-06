package allocation

// ScaleTier 规模档位：班级规模 >= MinSize 时命中本档（取等归高档）。
// Percent 为整数百分数，如 120 表示 120%。
type ScaleTier struct {
	MinSize int
	Percent int
}

// RankLimit 职级对应的学期工作量下限与上限。
type RankLimit struct {
	Rank string
	Min  int
	Max  int
}

// Config 全局核算参数：所有系数均为整数百分数。
type Config struct {
	Tiers                 []ScaleTier // 按 MinSize 升序
	NewCourseBonusPercent int         // 新开课加成，如 20 表示 +20%
	LabPercent            int         // 实验课系数，如 150 表示 150%
	Ranks                 []RankLimit // 各职级上下限
	ConfirmDeadline       int64       // 待确认指派的确认期限（逻辑时间单位，恰等仍有效）
}

// TeacherSpec 教师初始化参数。
type TeacherSpec struct {
	ID   string
	Rank string
}

// TaskSpec 课程任务参数。周次为闭区间，Periods 为每周固定占用的节次编号。
type TaskSpec struct {
	ID        string
	Semester  string
	Hours     int
	ClassSize int
	IsNew     bool
	IsLab     bool
	StartWeek int
	EndWeek   int
	Periods   []int
}

// AssignItem 单项指派：某教师承担某任务的整数学时。
type AssignItem struct {
	TaskID    string
	TeacherID string
	Hours     int
}

// TeacherReport 单个教师的学期核算结果。
type TeacherReport struct {
	TeacherID     string
	Rank          string
	Total         int // 累计折算工作量
	Min           int
	Max           int
	Deficit       int // 低于下限的差额（冲抵前）
	Excess        int // 高于下限的部分
	CreditBrought int // 自上学期带入的抵扣额度
	CreditUsed    int // 实际用于冲抵欠额的额度
	Unmet         int // 冲抵后仍为正的欠额（未达标）
	CreditNext    int // 结转下学期的可抵扣额度
}

// SemesterReport 学期核算结果，按教师 ID 排序。
type SemesterReport struct {
	Semester  string
	SettledAt int64
	Teachers  []TeacherReport
}

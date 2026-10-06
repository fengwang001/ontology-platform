package scholarship

import "time"

// LevelConfig 描述一个奖项等级。顺序由 NewEngine 中传入的切片决定。
type LevelConfig struct {
	ID         string
	MinAverage float64 // 平均成绩下限（含）
	MinCredits float64 // 本周期修读学分下限（含）
	PoolQuota  int     // 该等级全局机动名额
}

// Student 是评定时刻的学生快照数据。
type Student struct {
	ID        string
	Name      string
	Dept      string
	Average   float64
	Credits   float64
	Honor     int
	HasFail   bool
	Sanctions []Sanction
}

// Sanction 为一条处分记录；ReleasedAt 为零值表示尚未解除。
type Sanction struct {
	ID         string
	ReleasedAt time.Time
}

// DisqualReason 为资格不满足原因，按固定优先级取值。
type DisqualReason int

const (
	ReasonEligible DisqualReason = iota
	ReasonSanction
	ReasonFail
	ReasonCredits
	ReasonAverage
)

// Eligibility 为单个学生在单个等级上的资格判定结果。
type Eligibility struct {
	StudentID string
	Level     string
	Eligible  bool
	Reason    DisqualReason
}

// RankedStudent 为院系内排序后的一条记录。
type RankedStudent struct {
	StudentID string
	Dept      string
	Rank      int // 并列同名次，其后跳号
	Average   float64
	Credits   float64
	Honor     int
}

// AwardSource 标识名额来源。
type AwardSource int

const (
	SourceDept AwardSource = iota + 1
	SourcePool
)

// Award 为一项已授予的奖励。
type Award struct {
	StudentID string
	Level     string
	Dept      string
	Source    AwardSource
	Rank      int
	Confirmed bool
}

// Result 为一次评定的不可变快照。
type Result struct {
	EvaluatedAt time.Time
	Levels      []string
	Awards      []Award
	Ranking     map[string][]RankedStudent // level -> 全体合格者的跨院系排序
}

// Tracer 记录评定过程中的输入、输出与判定依据。
type Tracer interface {
	Log(step, detail string)
}

type nopTracer struct{}

func (nopTracer) Log(string, string) {}

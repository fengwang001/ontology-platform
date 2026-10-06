// Package dr 实现需求响应邀约与履约考核系统：运营方发布削减事件并邀约参与者，
// 参与者承诺削减量并在事件窗口内履约，系统按历史用电推定基线、考核履约率并
// 结算报酬与违约金。所有操作携带调用方给出的当前时刻，拒绝次序固定，结果可复现。
package dr

import "fmt"

// State 为事件状态机：已发布 → 进行中 → 已结束 → 已考核；考核前可取消。
type State int

const (
	StatePublished State = iota // 已发布
	StateRunning                // 进行中
	StateEnded                  // 已结束
	StateCancelled              // 已取消
	StateSettled                // 已考核
)

func (s State) String() string {
	switch s {
	case StatePublished:
		return "已发布"
	case StateRunning:
		return "进行中"
	case StateEnded:
		return "已结束"
	case StateCancelled:
		return "已取消"
	case StateSettled:
		return "已考核"
	}
	return "未知状态"
}

// ErrKind 为错误类别；声明顺序即固定拒绝次序（数值小者优先）。
type ErrKind int

const (
	ErrKindParam         ErrKind = iota // 参数非法
	ErrKindClock                        // 时钟回退
	ErrKindEventState                   // 事件不存在或状态不允许
	ErrKindNotInvited                   // 参与者未被邀约
	ErrKindDeadline                     // 已过截止
	ErrKindEventConflict                // 事件冲突
	ErrKindDataConflict                 // 数据冲突
	ErrKindSettled                      // 已考核
	ErrKindIncomplete                   // 考核数据不齐（仅 Assess 返回，不参与上述次序）
)

func (k ErrKind) String() string {
	switch k {
	case ErrKindParam:
		return "参数非法"
	case ErrKindClock:
		return "时钟回退"
	case ErrKindEventState:
		return "事件不存在或状态不允许"
	case ErrKindNotInvited:
		return "参与者未被邀约"
	case ErrKindDeadline:
		return "已过截止"
	case ErrKindEventConflict:
		return "事件冲突"
	case ErrKindDataConflict:
		return "数据冲突"
	case ErrKindSettled:
		return "已考核"
	case ErrKindIncomplete:
		return "考核数据不齐"
	}
	return "未知错误"
}

// Error 为系统拒绝操作时返回的错误，Kind 可区分错误类别；
// 考核被拒绝时 Participant/Reason 给出第一个不满足的参与者与原因。
type Error struct {
	Kind        ErrKind
	Msg         string
	Participant string
	Reason      string
}

func (e *Error) Error() string {
	if e.Participant != "" {
		return fmt.Sprintf("%s: %s（首个不满足参与者=%s，原因=%s）", e.Kind, e.Msg, e.Participant, e.Reason)
	}
	return fmt.Sprintf("%s: %s", e.Kind, e.Msg)
}

func newErr(kind ErrKind, format string, args ...any) *Error {
	return &Error{Kind: kind, Msg: fmt.Sprintf(format, args...)}
}

// Config 为系统配置，创建后不可变。
type Config struct {
	IntervalTicks       int64   // 计量间隔长度（tick）
	TicksPerDay         int64   // 每天的 tick 数，须为 IntervalTicks 的整数倍
	AdjustmentIntervals int     // 窗口之前同日校正调整期的间隔数
	QualifyingDays      int     // 基线资格日目标数量
	MinQualifyingDays   int     // 基线资格日最少数量，不足则不可考核
	MinCommitment       float64 // 最小承诺量
	AdjRatioLower       float64 // 同日校正比例下界（取等不裁剪）
	AdjRatioUpper       float64 // 同日校正比例上界（取等不裁剪）
	MaxLookbackDays     int     // 资格日回看天数上限
	IsWorkday           func(day int64) bool
}

func (c *Config) validate() error {
	if c.IntervalTicks <= 0 || c.TicksPerDay <= 0 || c.TicksPerDay%c.IntervalTicks != 0 {
		return fmt.Errorf("IntervalTicks/TicksPerDay 非法")
	}
	if c.AdjustmentIntervals <= 0 {
		return fmt.Errorf("AdjustmentIntervals 须为正")
	}
	if c.QualifyingDays <= 0 || c.MinQualifyingDays <= 0 || c.MinQualifyingDays > c.QualifyingDays {
		return fmt.Errorf("资格日数量配置非法")
	}
	if c.MinCommitment <= 0 {
		return fmt.Errorf("MinCommitment 须为正")
	}
	if c.AdjRatioLower <= 0 || c.AdjRatioLower > c.AdjRatioUpper {
		return fmt.Errorf("校正比例上下界非法")
	}
	if c.MaxLookbackDays < c.QualifyingDays {
		return fmt.Errorf("MaxLookbackDays 须不小于 QualifyingDays")
	}
	return nil
}

func (c *Config) workday(day int64) bool {
	if c.IsWorkday != nil {
		return c.IsWorkday(day)
	}
	return day%7 < 5
}

// EventParams 为创建事件的参数。窗口左闭右开，起点须对齐计量间隔。
type EventParams struct {
	Day              int64   // 事件日
	WindowStart      int64   // 窗口起点 tick，须对齐间隔边界
	WindowIntervals  int     // 窗口长度（间隔数）
	ResponseDeadline int64   // 应答截止时刻（该时刻本身视为已过）
	ExitDeadline     int64   // 免责退出截止时刻
	PayUnitPrice     float64 // 单位削减量报酬单价
	PenaltyUnitPrice float64 // 违约金单价
	QualifiedRatio   float64 // 履约合格比例，(0,1]
}

// Invite 为一次邀约记录。
type Invite struct {
	Participant string
	Requested   float64
	Responded   bool
	Accepted    bool
}

// Commitment 为接受邀约后形成的承诺；LateWithdrawn 表示免责截止后退出，
// 承诺仍在但实际削减按零考核。
type Commitment struct {
	EventID       string
	Participant   string
	Requested     float64
	Committed     float64
	LateWithdrawn bool
}

// Event 为事件实体。
type Event struct {
	ID     string
	Params EventParams
	cfg    *Config

	Cancelled        bool
	CancelAfterStart bool // 窗口开始后取消：窗口截断到取消时刻所在间隔的起点
	CancelTick       int64
	Settled          bool

	invites     map[string]*Invite
	commitments map[string]*Commitment // 仅含仍具约束力的承诺
	assessment  *Assessment
}

// ParticipantResult 为一名参与者的考核结果。
type ParticipantResult struct {
	Participant    string
	Assessable     bool // false 表示资格日不足，不可考核，不付不罚
	LateWithdrawn  bool
	QualifyingDays int
	AdjRatio       float64 // 同日校正比例（裁剪后）
	Committed      float64 // 折算后的承诺量
	Reduction      float64 // 事件削减量
	Ratio          float64 // 履约率
	Payment        float64
	Penalty        float64
}

// Assessment 为一次考核的完整结果，考核成功后不可变。
type Assessment struct {
	EventID string
	Results []ParticipantResult // 按参与者 ID 排序
}

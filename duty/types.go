// Package duty 实现机组值勤时限与排班合规系统。
//
// 所有时刻均为非负整数分钟，自某个固定历元起算。区间约定见各函数文档。
package duty

// Config 为系统级配置。DayLen 为一天的分钟数（通常 1440）。
// 一天按报到时刻划分为三个左闭右开的时段：
//
//	[0, DayBoundary1)、[DayBoundary1, DayBoundary2)、[DayBoundary2, DayLen)
//
// 报到时刻按 t mod DayLen 归入对应时段。
type Config struct {
	DayLen       int
	DayBoundary1 int // 早段 / 日段边界
	DayBoundary2 int // 日段 / 夜段边界

	BaseLimit [3]int // 三时段基础单次值勤上限（分钟）
	PerLegCut int    // 每多一个航段，上限递减的分钟数
	MinLimit  int    // 递减后的下限

	MinRest int // 最短休息分钟数

	Window7  int // 7 日时间窗长度（分钟）
	Window28 int // 28 日时间窗长度（分钟）
	Limit7   int // 任意 Window7 窗内值勤总时长上限
	Limit28  int // 任意 Window28 窗内值勤总时长上限

	MaxExtension int // 单次值勤最大延长量
}

// Duty 为一个（已接受的）值勤期。区间语义为 [Start, End)。
type Duty struct {
	ID       int
	PersonID int
	Start    int  // 报到时刻
	End      int  // 解除时刻（延长后更新）
	Legs     int  // 航段数，0 表示待命/地面值勤
	Aircraft int  // 所需机型资质
	Extended bool // 是否已延长（至多一次）
	OrigEnd  int  // 延长前的解除时刻
}

// Length 为值勤时长。
func (d *Duty) Length() int { return d.End - d.Start }

// Reason 是统一拒绝类别。值为空字符串 "" 表示接受。
type Reason string

const (
	RejectInvalidParams Reason = "invalid_params"
	RejectClockRollback Reason = "clock_rollback"
	RejectNoPerson      Reason = "person_not_found"
	RejectNoDuty        Reason = "duty_not_found"
	RejectImmutable     Reason = "started_or_released"
	RejectQualification Reason = "qualification_invalid"
	RejectOverlap       Reason = "overlap"
	RejectRest          Reason = "rest_insufficient"
	RejectSingleLimit   Reason = "single_duty_exceeded"
	RejectExtension     Reason = "extension_rule_violated"
	RejectRolling7      Reason = "rolling_7d_exceeded"
	RejectRolling28     Reason = "rolling_28d_exceeded"
)

// Accepted 为变更操作被接受后的结果。
type Accepted struct {
	DutyID int // 登记时为新值勤期 ID；其它操作为被操作值勤期 ID
}

// Result 为一次变更操作的结论。接受时 Reject == ""。
// 仅在 Reject 为 RejectRolling7 / RejectRolling28 时 WindowStart 有效，
// 给出使之超限的那个时间窗的起点（多个时取最小起点，保证可复现）。
type Result struct {
	Reject      Reason
	WindowStart int
}

// OK 报告操作是否被接受。
func (r Result) OK() bool { return r.Reject == "" }

func (r Result) String() string {
	if r.Reject == "" {
		return "ACCEPT"
	}
	if r.Reject == RejectRolling7 || r.Reject == RejectRolling28 {
		return "REJECT " + string(r.Reject) + "@window=" + itoa(r.WindowStart)
	}
	return "REJECT " + string(r.Reject)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [24]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}

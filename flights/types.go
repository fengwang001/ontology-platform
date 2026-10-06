package flights

import "errors"

// Reason 取消原因。
type Reason int

const (
	ReasonNone       Reason = iota // 未取消
	ReasonInjected                 // 注入取消
	ReasonCurfew                   // 到达落入宵禁
	ReasonNoAircraft               // 无飞机可用
	ReasonNoCrew                   // 无机组可用
	ReasonDuty                     // 值勤超限
)

func (r Reason) String() string {
	switch r {
	case ReasonInjected:
		return "注入"
	case ReasonCurfew:
		return "宵禁"
	case ReasonNoAircraft:
		return "无飞机"
	case ReasonNoCrew:
		return "无机组"
	case ReasonDuty:
		return "值勤超限"
	}
	return "无"
}

// reasonRank 多个原因根航班相同时的取舍优先级（数值小者优先）。
// 注入取消的根必为本段、无飞机/无机组的根必为更早航段，二者不会并列，
// 故注入的位次无关紧要；其余按 无飞机 < 宵禁 < 值勤超限 < 无机组 取舍。
func reasonRank(r Reason) int {
	switch r {
	case ReasonNoAircraft:
		return 0
	case ReasonCurfew:
		return 1
	case ReasonDuty:
		return 2
	case ReasonNoCrew:
		return 3
	case ReasonInjected:
		return 4
	}
	return 5
}

// Status 航班结论状态。
type Status int

const (
	StatusOnTime    Status = iota // 按计划
	StatusDelayed                 // 推迟
	StatusCancelled               // 取消
)

func (s Status) String() string {
	switch s {
	case StatusOnTime:
		return "按计划"
	case StatusDelayed:
		return "推迟"
	case StatusCancelled:
		return "取消"
	}
	return "未知"
}

// Curfew 机场宵禁区间，左闭右开 [Start, End)；Start >= End 表示无宵禁。
type Curfew struct {
	Start int
	End   int
}

func (c Curfew) Active() bool { return c.Start < c.End }

func (c Curfew) Contains(t int) bool { return c.Active() && t >= c.Start && t < c.End }

// Flight 计划航班表中的一段航班。
type Flight struct {
	ID       string
	SchedDep int // 计划起飞时刻（分钟，非负）
	Duration int // 计划飞行时长（分钟，非负）
	Origin   string
	Dest     string
	Aircraft string
	Crew     string
}

// Config 系统配置。
type Config struct {
	MinTurnaround int               // 飞机最短过站时间
	MinConnection int               // 机组最短衔接时间
	MaxDuty       int               // 机组单日值勤上限
	Curfews       map[string]Curfew // 每个机场的宵禁区间
}

// Result 查询返回的航班结论。取消时 Dep/Arr 为 -1。
type Result struct {
	Status Status
	Dep    int
	Arr    int
	Reason Reason
}

var (
	ErrInvalidParam      = errors.New("参数非法")
	ErrClockRollback     = errors.New("时钟回退")
	ErrFlightNotFound    = errors.New("航班不存在")
	ErrAlreadyDeparted   = errors.New("已起飞不可改")
	ErrInjectionNotFound = errors.New("注入记录不存在")
)

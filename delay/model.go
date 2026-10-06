// Package delay 实现航班延误连锁调整引擎：给定一天的计划航班表与
// 延误/取消注入，沿飞机链与机组链推算各航班的实际起降时刻与结论，
// 并以增量方式保证查询与重算的复杂度不随无关航班增长。
package delay

import "sync"

// Interval 为左闭右开时间区间 [Start, End)。Start==End 表示空区间。
type Interval struct {
	Start int
	End   int
}

// Contains 按左闭右开语义判断 t 是否落入区间。
func (i Interval) Contains(t int) bool {
	return i.Start <= t && t < i.End
}

// Empty 判断区间是否为空。
func (i Interval) Empty() bool { return i.Start >= i.End }

// Flight 描述一个计划航班。
type Flight struct {
	ID         string // 唯一标识
	Scheduled  int    // 计划起飞时刻（非负分钟）
	Duration   int    // 计划飞行时长（正分钟）
	Origin     string // 起飞机场
	Dest       string // 到达机场
	AircraftID string // 所属飞机
	CrewID     string // 所属机组
}

// Config 为引擎系统配置。
type Config struct {
	MinTurnaround int                 // 飞机最短过站时间
	MinConnection int                 // 机组最短衔接时间
	DutyLimit     int                 // 机组单日值勤上限
	Curfews       map[string]Interval // 每个机场的宵禁区间（可为空）
}

// Status 为航班结论状态。
type Status int

const (
	StatusScheduled Status = iota // 按计划
	StatusDelayed                 // 推迟
	StatusCanceled                // 取消
)

// Reason 为取消原因（仅取消航班有意义）。
type Reason int

const (
	ReasonNone         Reason = iota
	ReasonInjected            // 注入取消
	ReasonCurfew              // 宵禁（到达落入宵禁区间）
	ReasonNoAircraft          // 无飞机可用
	ReasonNoCrew              // 无机组可用
	ReasonDutyExceeded        // 值勤超限
)

func (s Status) String() string {
	switch s {
	case StatusScheduled:
		return "scheduled"
	case StatusDelayed:
		return "delayed"
	default:
		return "canceled"
	}
}

func (r Reason) String() string {
	switch r {
	case ReasonInjected:
		return "injected"
	case ReasonCurfew:
		return "curfew"
	case ReasonNoAircraft:
		return "no_aircraft"
	case ReasonNoCrew:
		return "no_crew"
	case ReasonDutyExceeded:
		return "duty_exceeded"
	default:
		return "none"
	}
}

// Result 为单个航班的查询结论。
type Result struct {
	Status    Status
	ActualDep int // 取消时无意义
	ActualArr int // 取消时无意义
	Reason    Reason
}

// SentinelError 为按类别区分的可判定错误。
type SentinelError string

func (e SentinelError) Error() string { return string(e) }

const (
	ErrInvalid     SentinelError = "invalid flight table or parameters"
	ErrClockRewind SentinelError = "clock rewind"
	ErrNoFlight    SentinelError = "flight not found"
	ErrDeparted    SentinelError = "flight already departed"
	ErrNoInjection SentinelError = "injection reference not found"
)

// Injection 是一次注入的内容。Cancel=true 为取消注入，否则 Delay 为
// 非负的延误分钟数。
type Injection struct {
	Delay  int
	Cancel bool
}

// Engine 为延误连锁调整引擎。所有方法可并发调用。
type Engine struct {
	mu sync.Mutex

	cfg       Config
	flights   []*flight
	byID      map[string]*flight
	chainsA   map[string][]*flight
	chainsC   map[string][]*flight
	conc      map[string]*conclusion
	inj       map[string]map[string]Injection
	clock     int
	lastSwept int // 最近一次重算处理的航班数（性能可验证计数）
}

// New 校验航班表与配置并构建引擎；初始时钟为 -1（尚未接受任何操作）。
func New(flights []Flight, cfg Config) (*Engine, error) {
	return newEngine(flights, cfg)
}

// Clock 返回当前引擎时钟。
func (e *Engine) Clock() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.clock
}

// flight 为引擎内部使用的航班记录。
type flight struct {
	Flight
	ord int // 原始输入序号，用于确定性平手排序
	piA int // 在飞机链中的位置
	piC int // 在机组链中的位置
}

// conclusion 为一个航班被推算出并持久化的结论。
type conclusion struct {
	status    Status
	dep       int
	arr       int
	reason    Reason
	root      *croot // 导致本航班取消的根事件（取消时）
	rootA     *croot // 本航班之后飞机所处滞留状态的根（nil 表示无滞留）
	rootC     *croot // 本航班之后机组所处滞留状态的根（nil 表示无滞留）
	airportA  string // 本航班之后飞机所在机场（取消段也准确保留）
	airportC  string // 本航班之后机组所在机场
	readyASet bool   // 飞机最近一段实际执行后的最早可用下界是否存在
	readyA    int
	readyCSet bool // 机组最近一段实际执行后的最早可用下界是否存在
	readyC    int
	deadC     *croot // 机组值勤失效根（一旦存在不再恢复）
	firstDepC int    // 机组第一个实际起飞段的实际起飞时刻
	firstSetC bool
}

// croot 是取消传导链的根事件。多个根同时成立时按
// (time, reasonRank, birth) 取最早者，保证与注入次序无关。
type croot struct {
	time   int
	rank   int
	reason Reason
	birth  int // 根航班的确定性序号
}

func (r *croot) earlier(o *croot) bool {
	if r.time != o.time {
		return r.time < o.time
	}
	if r.rank != o.rank {
		return r.rank < o.rank
	}
	return r.birth < o.birth
}

// reasonRank 为同一时刻多原因并存时的固定先后（注入最靠前）。
var reasonRank = map[Reason]int{
	ReasonInjected:     0,
	ReasonCurfew:       1,
	ReasonNoAircraft:   2,
	ReasonNoCrew:       3,
	ReasonDutyExceeded: 4,
}

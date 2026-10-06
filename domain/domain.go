// Package domain 定义特种设备检验周期与超期管控系统的核心领域类型：
// 对象类别、类别配置、对象状态、检验结果、错误种类与结构化错误。
package domain

// Category 是对象类别。设备类别为锅炉、压力容器；附件类别为安全阀、压力表。
type Category string

const (
	CatBoiler         Category = "boiler"          // 锅炉（设备）
	CatPressureVessel Category = "pressure_vessel" // 压力容器（设备）
	CatSafetyValve    Category = "safety_valve"    // 安全阀（附件）
	CatPressureGauge  Category = "pressure_gauge"  // 压力表（附件）
)

// IsDevice 报告该类别是否为设备（可挂接附件、可做使用登记）。
func (c Category) IsDevice() bool {
	return c == CatBoiler || c == CatPressureVessel
}

// IsAccessory 报告该类别是否为附件。
func (c Category) IsAccessory() bool { return c == CatSafetyValve || c == CatPressureGauge }

// Valid 报告类别是否为已知的四类之一。
func (c Category) Valid() bool { return c.IsDevice() || c.IsAccessory() }

// Config 是一个类别的管控配置。
type Config struct {
	PeriodMonths    int // 检验周期（日历月数），>0
	EarlyWindowDays int // 提前检验窗口（天数），>=0
	MinUnsealDays   int // 启封最小保障天数，>=0
	WarnAheadDays   int // 预警提前天数，>=0
}

// Valid 报告配置各字段是否合法。
func (c Config) Valid() bool {
	return c.PeriodMonths > 0 && c.EarlyWindowDays >= 0 && c.MinUnsealDays >= 0 && c.WarnAheadDays >= 0
}

// Status 是对象状态。
type Status int

const (
	StatusInService Status = iota // 在用
	StatusSealed                  // 封存（暂停计时）
	StatusSuspended               // 停用（检验不合格）
	StatusScrapped                // 报废（终态）
)

func (s Status) String() string {
	switch s {
	case StatusInService:
		return "in_service"
	case StatusSealed:
		return "sealed"
	case StatusSuspended:
		return "suspended"
	case StatusScrapped:
		return "scrapped"
	}
	return "unknown"
}

// InspectResult 是检验结论。
type InspectResult int

const (
	ResultPass        InspectResult = iota // 合格
	ResultConditional                      // 有条件合格（附整改限期）
	ResultFail                             // 不合格
)

func (r InspectResult) String() string {
	switch r {
	case ResultPass:
		return "pass"
	case ResultConditional:
		return "conditional"
	case ResultFail:
		return "fail"
	}
	return "unknown"
}

// ErrKind 区分错误类别；声明顺序即优先级（靠前者优先报告）。
type ErrKind int

const (
	ErrInvalidParam    ErrKind = iota // 参数非法
	ErrDateRegression                 // 日期回退
	ErrNotFound                       // 对象不存在
	ErrScrapped                       // 对象已报废
	ErrStateNotAllowed                // 状态不允许
	ErrConditionUnmet                 // 条件不满足
)

func (k ErrKind) String() string {
	switch k {
	case ErrInvalidParam:
		return "invalid_param"
	case ErrDateRegression:
		return "date_regression"
	case ErrNotFound:
		return "not_found"
	case ErrScrapped:
		return "scrapped"
	case ErrStateNotAllowed:
		return "state_not_allowed"
	case ErrConditionUnmet:
		return "condition_unmet"
	}
	return "unknown"
}

// Error 是系统的结构化错误，携带类别与可读信息。
type Error struct {
	Kind ErrKind
	Msg  string
}

func (e *Error) Error() string { return string(e.Kind.String()) + ": " + e.Msg }

// NewError 构造一个结构化错误。
func NewError(kind ErrKind, msg string) *Error { return &Error{Kind: kind, Msg: msg} }

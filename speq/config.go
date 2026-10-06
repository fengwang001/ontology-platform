package speq

// ObjectKind 对象种类。
type ObjectKind int

const (
	KindDevice ObjectKind = iota
	KindSafetyValve
	KindPressureGauge
)

// CategoryConfig 设备/附件类别配置。
//
//	PeriodMonths       检验周期（月数）
//	EarlyWindowDays    提前检验窗口（天数，含边界）
//	MinUnsealDays      启封最小保障天数
//	WarningLeadDays    预警提前天数
type CategoryConfig struct {
	Code            string
	Kind            ObjectKind
	PeriodMonths    int
	EarlyWindowDays int
	MinUnsealDays   int
	WarningLeadDays int
}

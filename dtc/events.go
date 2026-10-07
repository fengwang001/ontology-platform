package dtc

// EventKind 事件类型。
type EventKind int

const (
	EventIgnitionOn EventKind = iota
	EventIgnitionOff
	EventMonitorResult
	EventEnvironmentSample
	EventScanToolClear
)

func (k EventKind) String() string {
	switch k {
	case EventIgnitionOn:
		return "IgnitionOn"
	case EventIgnitionOff:
		return "IgnitionOff"
	case EventMonitorResult:
		return "MonitorResult"
	case EventEnvironmentSample:
		return "EnvironmentSample"
	case EventScanToolClear:
		return "ScanToolClear"
	}
	return "Unknown"
}

// Event 是一个输入事件。时刻与里程不得小于上一个被接受事件。
type Event struct {
	Kind     EventKind
	Time     int64 // 事件时刻（单调不减）
	Odometer int64 // 事件里程（单调不减）

	// 监测结果上报（Kind == EventMonitorResult）
	DTCCode int
	Passed  bool // true=通过，false=失败

	// 环境采样（Kind == EventEnvironmentSample）
	Speed       int64
	CoolantTemp int64
}

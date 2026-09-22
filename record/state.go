package record

// State 是单个幂等键记录所处的状态。
type State int

const (
	// Running 表示执行函数尚未返回。
	Running State = iota + 1
	// Succeeded 表示执行已成功，结果已固定，只可回放。
	Succeeded
	// Failed 表示执行返回了错误；同键同体允许再次重试。
	Failed
	// Expired 表示记录已超过存活时长，对外等同于不存在。
	Expired
)

// String 返回状态的可读名称。
func (s State) String() string {
	switch s {
	case Running:
		return "running"
	case Succeeded:
		return "succeeded"
	case Failed:
		return "failed"
	case Expired:
		return "expired"
	default:
		return "unknown"
	}
}

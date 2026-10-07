package cascade

// Strategy 是删除策略。
type Strategy int

const (
	// Background 后台删除：对象可立即移除，零属主依赖者连带后台删除。
	Background Strategy = iota + 1
	// Foreground 前台删除：等待阻塞依赖者处理完毕才移除属主。
	Foreground
	// Orphan 孤立：移除对象时不连带删除任何依赖者。
	Orphan
)

func (s Strategy) String() string {
	switch s {
	case Background:
		return "Background"
	case Foreground:
		return "Foreground"
	case Orphan:
		return "Orphan"
	default:
		return "Unknown"
	}
}

// OwnerRef 是一条属主引用。
type OwnerRef struct {
	OwnerID string
	// Blocking 为 true 时，前台删除中的属主必须等待该依赖者处理完。
	Blocking bool
}

// Object 是对控制器内部对象的只读快照。
type Object struct {
	ID         string
	Owners     []OwnerRef
	Finalizers []string
	Deleting   bool
	Strategy   Strategy
	DeleteAt   int64
}

// Snapshot 是某一时刻控制器的完整状态。
type Snapshot struct {
	Objects map[string]Object
	Clock   int64
}

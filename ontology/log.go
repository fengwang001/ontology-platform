package ontology

// DecisionKind 区分日志中记录的判定类别。
type DecisionKind string

const (
	// DecisionBegin 记录一次遍历的开始，包含调用方权限与输入。
	DecisionBegin DecisionKind = "begin"
	// DecisionExtended 记录一条可见链接的正常扩展。
	DecisionExtended DecisionKind = "extended"
	// DecisionCycle 记录一次真实环路终止判定。
	DecisionCycle DecisionKind = "cycle"
	// DecisionHidden 记录一次「部分不可见」标记判定。
	DecisionHidden DecisionKind = "hidden"
	// DecisionEnd 记录一次遍历的结束。
	DecisionEnd DecisionKind = "end"
)

// LogEntry 是遍历过程中产生的一条结构化判定日志。
type LogEntry struct {
	Kind         DecisionKind
	CallerLabels []Label  // 调用方权限标签集合（已排序副本）
	Start        ObjectID // 遍历输入：起始对象
	MaxDepth     int      // 遍历输入：深度上限
	Version      uint64   // 遍历所基于的快照版本
	Path         []string // 到达当前判定点的可见链接 ID 序列
	At           ObjectID // 判定发生处的对象
	LinkID       string   // 被考察的链接（不可见链接为空）
	Reason       string   // 判定依据的可读描述
}

// Logger 接收遍历过程中的判定日志。实现必须是非阻塞且并发安全的，
// 遍历对每个判定同步调用一次 Log。
type Logger interface {
	Log(entry LogEntry)
}

// LoggerFunc 把函数适配为 Logger。
type LoggerFunc func(entry LogEntry)

// Log 实现 Logger 接口。
func (f LoggerFunc) Log(entry LogEntry) { f(entry) }

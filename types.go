package ontology

// Object 是集合中的元素：字符串主键 + 一个 int 字段。
type Object struct {
	Key   string
	Value int
}

// ChangeKind 描述遍历期间集合发生过的变更类别。
type ChangeKind int

const (
	ChangeNone   ChangeKind = 0
	ChangeInsert ChangeKind = 1 << iota
	ChangeDelete
	ChangeBoth = ChangeInsert | ChangeDelete
)

// String 返回变更类别的可读名称。
func (c ChangeKind) String() string {
	switch c {
	case ChangeNone:
		return "none"
	case ChangeInsert:
		return "insert"
	case ChangeDelete:
		return "delete"
	case ChangeBoth:
		return "insert+delete"
	default:
		return "unknown"
	}
}

// Page 是一次 Scan 的结果。
type Page struct {
	Objects []Object // 本页返回的对象（已删除的元素不会出现）
	Cursor  string   // 下一页游标；无后续时为空字符串
	HasMore bool     // 是否还有确切续点（截断）

	// Truncated 表示本页因 limit 被截断（与 HasMore 等价，单独暴露以便断言）。
	Truncated bool
	// Discarded 表示本页期间观察到的丢弃：快照中存在、但已被删除的元素。
	Discarded int
	// Changes 表示截止到本次 Scan，遍历期间发生过的集合变更类别。
	Changes ChangeKind

	sessionID string
}

// SessionStats 是一次遍历会话的累计统计。
type SessionStats struct {
	Changes       ChangeKind // 遍历期间发生过的变更类别
	Inserted      int        // 遍历期间发生的插入次数
	Deleted       int        // 遍历期间发生的删除次数
	Discarded     int        // 累计丢弃（快照元素被删除）个数
	SnapshotTotal int        // 首次 Scan 时快照中的元素总数
	Returned      int        // 已返回的快照元素个数
	Valid         bool       // 会话是否仍然有效
}

// SkippedReason 用于按原因分类报告累计跳过情况。
type SkippedReason struct {
	DiscardedByDelete int // 因遍历期间被删除而丢弃
}

// SkippedReport 汇总遍历累计跳过的元素数及原因分类。
type SkippedReport struct {
	Total    int
	Reasons  SkippedReason
	Valid    bool
	Returned int
}

package lifecycle

// LinkBehavior 声明链接在端点对象删除时的两类行为。
type LinkBehavior int

const (
	// LinkInvalidatesWithEndpoint：随对象删除一并失效（复活时可按条件恢复）。
	LinkInvalidatesWithEndpoint LinkBehavior = iota
	// LinkKeepsIndependent：独立于对象存活状态继续存在，永不随删除/复活改变。
	LinkKeepsIndependent
)

// LinkStatus 链接记录状态。
type LinkStatus int

const (
	LinkAvailable LinkStatus = iota
	LinkInvalidated
)

// LinkType 链接类型声明。
type LinkType struct {
	ID       string
	Behavior LinkBehavior
}

// LinkRecord 对象间链接记录。
type LinkRecord struct {
	ID            string
	TypeID        string
	SourceID      string
	TargetID      string
	Status        LinkStatus
	InvalidatedAt int64 // 仅 Status == LinkInvalidated 时有意义
}

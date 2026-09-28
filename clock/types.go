package clock

// EventType 是事件种类：本地 / 发送 / 接收。
type EventType string

const (
	// EventLocal 本地事件，时钟 +1。
	EventLocal EventType = "local"
	// EventSend 发送事件，时钟 +1，同时登记一条携带新时间戳的消息。
	EventSend EventType = "send"
	// EventReceive 接收事件，时钟 = max(本地时钟, 消息时间戳) + 1。
	EventReceive EventType = "receive"
)

// Event 描述一次被接受的本地 / 发送 / 接收操作。
//
// 字段全部只读，供并发查询使用。同一节点上 Seq 从 0 起连续递增，
// 全局 ID 从 0 起按“被接受的顺序”连续分配。
type Event struct {
	ID        int64     // 全局事件编号（接受顺序，仅用于稳定的次级排序）
	Node      int       // 所属节点编号
	Seq       int64     // 节点内事件序号（从 0 起连续）
	Clock     int64     // 该事件的逻辑时钟值（Lamport 时间戳）
	Type      EventType // 事件种类
	MessageID int64     // send 事件登记的消息编号；receive 事件所接收的消息；否则为 -1
}

// Order 是两个事件之间的关系判定结果。
type Order string

const (
	// OrderBefore a 因果先于 b（happened-before）。
	OrderBefore Order = "before"
	// OrderAfter a 因果晚于 b。
	OrderAfter Order = "after"
	// OrderConcurrent 二者并发（无因果关系）。
	OrderConcurrent Order = "concurrent"
)

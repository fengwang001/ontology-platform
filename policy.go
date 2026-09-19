package ontology

// OverflowPolicy 决定订阅者队列写满时的处置方式。
type OverflowPolicy int

const (
	// DropOldest 丢弃队列中最旧的一条，腾出位置给新消息。
	DropOldest OverflowPolicy = iota
	// DropNewest 丢弃当前这条新消息，队列保持不变。
	DropNewest
	// Disconnect 标记该订阅者为落后并断开：丢弃新消息并关闭其队列。
	Disconnect
)

func (p OverflowPolicy) String() string {
	switch p {
	case DropOldest:
		return "DropOldest"
	case DropNewest:
		return "DropNewest"
	case Disconnect:
		return "Disconnect"
	default:
		return "Unknown"
	}
}

// DrainPolicy 决定取消订阅或关闭分发器时，
// 已入队但尚未被取出的消息如何处理。
type DrainPolicy int

const (
	// DropPending 丢弃队列中尚未被取出的消息。
	DropPending DrainPolicy = iota
	// DrainPending 允许订阅者把队列中剩余的消息读完。
	DrainPending
)

func (p DrainPolicy) String() string {
	switch p {
	case DropPending:
		return "DropPending"
	case DrainPending:
		return "DrainPending"
	default:
		return "Unknown"
	}
}

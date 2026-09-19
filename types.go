// Package ontology 目前只包含属性变更的订阅与扇出分发器。
package ontology

// OverflowPolicy 描述订阅者自身缓冲队列写满时的处置方式。
// 该策略只影响声明它的订阅者，不会影响其他订阅者。
type OverflowPolicy int

const (
	// DropOldest 队列满时丢弃最旧的一条，为新消息腾出位置。
	DropOldest OverflowPolicy = iota
	// DropNewest 队列满时丢弃本次到达的新消息，保留队列内已有消息。
	DropNewest
	// DisconnectLagging 队列满时将订阅者标记为落后并断开，不再投递。
	DisconnectLagging
)

// PendingPolicy 描述取消订阅或关闭分发器时，队列中尚未取出消息的处理方式。
type PendingPolicy int

const (
	// DropPending 丢弃队列中尚未取出的消息，通道随即关闭。
	DropPending PendingPolicy = iota
	// DrainPending 保留队列内容允许读完，读完后通道关闭；不主动关闭得更早。
	DrainPending
)

// Message 是投递给订阅者的一条属性变更，Seq 为全局单调递增序号。
type Message struct {
	Seq      uint64
	Entity   string
	Property string
	Value    any
}

// SubscriptionConfig 是订阅参数。
type SubscriptionConfig struct {
	// ID 为订阅者标识，必须在同一分发器内非空且唯一。
	ID string
	// Prefix 为实体前缀；匹配要求按路径段精确对齐（"user" 不匹配 "superuser"）。
	Prefix string
	// Properties 为关心的属性名集合；为空表示匹配该前缀下所有属性。
	Properties []string
	// Buffer 为有界缓冲容量，必须 >= 1。
	Buffer int
	// OnOverflow 为队列写满时的策略。
	OnOverflow OverflowPolicy
	// OnPending 为取消/关闭时残留消息的处理策略。
	OnPending PendingPolicy
}

// Stats 为单个订阅者的可归因计量数据。
type Stats struct {
	// Dropped 为累计丢弃条数（满队列丢弃与收尾丢弃都计入）。
	Dropped uint64
	// LastDropSeq 为最后一次丢弃发生时，新到消息（或残留区间）对应的序号；无丢弃时为 0。
	LastDropSeq uint64
	// Lagged 表示是否因 DisconnectLagging 策略被标记为落后并断开。
	Lagged bool
	// Closed 表示订阅是否已终止（取消、关闭或落后断开）。
	Closed bool
}

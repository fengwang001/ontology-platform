package causalbuf

// Outcome 表示一次 Receive 调用对触发消息本身的处置结果。
type Outcome int

const (
	// OutcomeDelivered 触发消息已交付（可能还级联交付了缓冲消息）。
	OutcomeDelivered Outcome = iota
	// OutcomeBuffered 触发消息暂不满足因果条件，已进入缓冲。
	OutcomeBuffered
	// OutcomeDuplicate 触发消息是重复消息，已丢弃（不是错误）。
	OutcomeDuplicate
)

// Result 是 Receive 的成功返回值。
type Result struct {
	// Outcome 触发消息的处置结果。
	Outcome Outcome

	// Delivered 本次调用实际交付的全部消息，按交付顺序排列；
	// 级联交付时包含触发消息及其后被连带交付的消息。
	Delivered []Message

	// DupCount 接收端当前累计的重复消息计数。
	DupCount int64
}

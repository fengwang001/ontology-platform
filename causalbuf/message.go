package causalbuf

// Message 是一条带向量时钟的广播消息。
//
// 消息身份由 (Sender, Vector[Sender]) 唯一确定：
// Sender 为发送方编号，Vector[Sender] 为该发送方的消息序号，
// Vector 的其余分量携带发送方已知的因果信息。
type Message struct {
	// Sender 发送方编号，取值范围 [0, peers)。
	Sender int

	// Vector 发送时刻的向量时钟，长度必须等于 peers。
	Vector []int

	// Payload 上层负载，组件不解释其内容。
	Payload any
}

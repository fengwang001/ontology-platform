package outbox

// Downstream 是中继投递的下游。Apply 必须按消息标识幂等。
type Downstream interface {
	Apply(msg Message) error
}

// IdempotentDownstream 是带崩溃点注入能力的幂等下游。骨架为桩。
type IdempotentDownstream struct{}

// NewIdempotentDownstream 构造一个新的幂等下游。
func NewIdempotentDownstream() *IdempotentDownstream { return nil }

// Apply 幂等地应用一条消息。
func (d *IdempotentDownstream) Apply(msg Message) error { return nil }

// Applied 返回下游最终应用序列的快照。
func (d *IdempotentDownstream) Applied() []Message { return nil }

// AppliedIDs 返回已应用消息标识的有序快照。
func (d *IdempotentDownstream) AppliedIDs() []string { return nil }

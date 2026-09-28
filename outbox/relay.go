package outbox

// Downstream 是消息下游。实现需保证 Apply 具备持久幂等语义：
// 同一 msgID 只会被真正应用一次，重复投递返回首次结果。
type Downstream interface {
	Apply(msg Message) error
}

// Relay 在发件箱与下游之间中继：每次取出全部已提交未标记消息，
// 按提交序与写入序投递，先投递后标记。
type Relay struct {
	// zero placeholders
}

// NewRelay 创建中继。crashAfterDelivery>0 时模拟在投递该序号消息后崩溃
// （最后一条已投递但未标记）。
func NewRelay(ob *Outbox, crashAfterDelivery int) *Relay {
	_ = ob
	_ = crashAfterDelivery
	return &Relay{}
}

// Run 执行一轮投递。返回本轮投递的消息标识序列。
func (r *Relay) Run() ([]string, error) { return nil, nil }

// CrashCount 返回累计发生的崩溃次数。
func (r *Relay) CrashCount() int { return 0 }

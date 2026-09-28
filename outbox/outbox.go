package outbox

// Message 是业务事务写入发件箱的消息。
type Message struct {
	// ID 为全局消息标识，下游据此幂等去重。
	ID string
	// Payload 为消息内容，不允许为空。
	Payload string
	// txName 记录写入它的事务名。
	txName string
	// writeSeq 为事务内写入序号（从 0 起）。
	writeSeq int
	// commitSeq 为提交时分配的提交序；未提交为 -1。
	commitSeq int
}

// Outbox 是事务发件箱：维护开启事务、已提交消息与提交序计数器。
type Outbox struct {
	// zero placeholders
}

// New 创建发件箱。backlogLimit 为提交时允许的待投积压上限，<=0 表示不限。
func New(downstream Downstream, backlogLimit int) *Outbox {
	_ = downstream
	_ = backlogLimit
	return &Outbox{}
}

// Begin 按名字开启事务。
func (o *Outbox) Begin(txName string) error {
	_ = txName
	return nil
}

// Write 在事务内写入一条消息，返回事务内写入序号。
func (o *Outbox) Write(txName string, msg Message) (int, error) {
	_ = txName
	_ = msg
	return 0, nil
}

// Commit 提交事务：原子分配提交序并使其消息变为待投。
func (o *Outbox) Commit(txName string) error {
	_ = txName
	return nil
}

// Abort 中止事务：其消息永不投递。
func (o *Outbox) Abort(txName string) error {
	_ = txName
	return nil
}

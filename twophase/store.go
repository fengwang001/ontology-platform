package twophase

// Effect 是一次外部副作用的持久化记录。
type Effect struct {
	Seq    int64
	Action string
}

// Store 是副作用与位点的持久化抽象。
// 实现必须保证 SaveEffect / SavePosition 在返回后崩溃不丢数据。
type Store interface {
	// LoadPosition 返回已提交位点；无记录时返回 0。
	LoadPosition() (int64, error)
	// LoadEffect 读取指定序号的副作用；不存在时返回 (nil, nil)。
	LoadEffect(seq int64) (*Effect, error)
	// SaveEffect 持久化副作用（崩溃后仍可读取）。
	SaveEffect(effect Effect) error
	// SavePosition 持久化位点，位点只能单调推进。
	SavePosition(position int64) error
	// ListPending 返回所有 seq > position 的已持久化副作用序号（升序）。
	// 用于崩溃重启后发现“效果已写、位点未进”的在途事件。
	ListPending(position int64) ([]int64, error)
}

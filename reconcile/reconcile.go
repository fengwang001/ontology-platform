package reconcile

// Comparison 记录一次区间哈希比较。
type Comparison struct {
	Level int
	Base  uint64
	End   uint64
	HashA uint64
	HashB uint64
	Equal bool
}

// Report 是一次对账的结果快照。
type Report struct {
	DifferingKeys []uint64
	SequencedKey  []uint64
	Sequence      []Comparison
}

// Reconcile 对两个形状相同的副本进行对账。
func Reconcile(a, b *Replica) (*Report, error) { return nil, nil }

package tso

// Timestamp 由物理毫秒与逻辑计数组成，按 (Physical, Logical) 字典序比较。
type Timestamp struct {
	Physical int64
	Logical  int64
}

// Cmp 返回 -1/0/1，按字典序比较。
func (t Timestamp) Cmp(o Timestamp) int {
	switch {
	case t.Physical < o.Physical:
		return -1
	case t.Physical > o.Physical:
		return 1
	case t.Logical < o.Logical:
		return -1
	case t.Logical > o.Logical:
		return 1
	default:
		return 0
	}
}

func (t Timestamp) String() string {
	return formatTimestamp(t)
}

// Bound 是共享存储中持久化的（任期，上界）。
// 上界为物理毫秒的排他上界：任何已发出时间戳的物理部分必须严格小于它。
type Bound struct {
	Term int64
	High int64
}

func (b Bound) String() string {
	return formatBound(b)
}

// Store 模拟共享持久化存储。写入仅在任期不小于存量任期时被接受。
type Store interface {
	// Get 返回当前持久化的（任期，上界）。
	Get() Bound
	// CompareBound 原子地"读后写"：读取当前值交给 decide，
	// 仅当 decide 返回的任期 >= 存量任期时整体提交，返回 committed=true
	// 与提交后的最新值；否则拒绝（committed=false, 最新值）。
	CompareBound(decide func(current Bound) Bound) (committed bool, latest Bound)
}

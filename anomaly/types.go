package anomaly

// 事务状态。
type Status string

const (
	Committed Status = "committed"
	Aborted   Status = "aborted"
)

// Op 是一个事务内的单个操作。
//   - Type 为 "write" 时写键 Key；
//   - Type 为 "read" 时读键 Key，并返回 ReadVersion 指向的具体版本；
//   - 版本为二元组（写事务编号, 该事务对该键的第几次写，从 1 起）；
//   - (0,0) 保留给初始版本。
type Op struct {
	Type        string  `json:"type"`
	Key         string  `json:"key"`
	ReadVersion *[2]int `json:"readVersion,omitempty"`
}

// Txn 是一个事务及其按时间顺序排列的操作序列。
type Txn struct {
	ID     int    `json:"id"`
	Status Status `json:"status"`
	Ops    []Op   `json:"ops"`
}

// History 是一次判定的完整输入。
// Order 以键为单位给出版本次序：每个值的序列以 (0,0) 开头，
// 其后按版本次序排列各已提交事务对该键的最后一次写版本。
type History struct {
	Txns  []Txn               `json:"txns"`
	Order map[string][][2]int `json:"order"`
}

// Cycle 是一个见证环。Txns 为环上事务编号序列，
// 以环内最小编号起头，沿边方向排列，不含重复结尾。
// Edges[i] 为 Txns[i] 到 Txns[(i+1)%n] 的边类型集合。
type Cycle struct {
	Txns  []int      `json:"txns"`
	Edges []EdgeMask `json:"edges"`
}

// Result 是一次判定的完整结果。
type Result struct {
	Accepted bool   `json:"accepted"`
	Category string `json:"category,omitempty"`
	Level    string `json:"level,omitempty"`
	Witness  *Cycle `json:"witness,omitempty"`
	Reason   string `json:"reason,omitempty"`
	Reject   string `json:"reject,omitempty"`
}

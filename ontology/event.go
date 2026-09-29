package ontology

// Side 标识事件来自哪一条输入流。零值非法，必须显式指定。
type Side int8

const (
	SideLeft  Side = 1 // 左侧流
	SideRight Side = 2 // 右侧流
)

// String 返回侧别的可读名称。
func (s Side) String() string {
	switch s {
	case SideLeft:
		return "left"
	case SideRight:
		return "right"
	default:
		return "invalid"
	}
}

// Event 是连接器接受一条输入后为其分配的带编号事件记录。
type Event struct {
	Seq  int64  // 连接器分配的全局单调编号，从 1 开始
	Side Side   // 来自左流还是右流
	Key  string // 连接键
	Time int64  // 事件时间（毫秒）
}

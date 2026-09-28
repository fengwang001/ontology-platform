package ontology

// Logger 是机制内部的结构化日志钩子。测试可注入自己的实现，
// 逐行记录输入、栅栏、缓存内容与判定依据。
type Logger interface {
	Logf(format string, args ...any)
}

// Event 是一条上游变更数据捕获事件。
type Event struct {
	Key     string
	Version int64
	// Value 为空字符串且 Tomb 为 true 表示删除墓碑。
	Value string
	Tomb  bool
}

// Token 是一次源头读取的状态令牌。
type Token struct {
	id      int64
	key     string
	version int64
	value   string
	tomb    bool
}

// ID 返回令牌编号，仅供观测使用。
func (t Token) ID() int64 { return t.id }

// Key 返回令牌对应的键。
func (t Token) Key() string { return t.key }

// Version 返回令牌读取到的源头版本；键不存在时为 0。
func (t Token) Version() int64 { return t.version }

// IsTomb 报告令牌是否读到墓碑（负缓存来源）。
func (t Token) IsTomb() bool { return t.tomb }

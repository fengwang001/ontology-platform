package fsm

// State 标识状态机中的一个状态。
type State string

// Event 标识可以投递给状态机的一个事件。
type Event string

// Transition 表示转移表中的一条规则：在 From 状态收到 Event 时迁移到 To。
type Transition struct {
	From  State
	Event Event
	To    State
}

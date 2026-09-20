package fsm

// State 标识状态机所处的一个状态。
type State string

// Event 标识可以投递给状态机的一个事件。
type Event string

// Transition 描述一条转移规则：在 From 状态收到 Event 时迁移到 To。
// From == To 时是合法的自转移。
type Transition struct {
	From  State
	Event Event
	To    State
}

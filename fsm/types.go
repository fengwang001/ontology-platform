package fsm

// State 标识会话协议机的一个状态。
type State string

// Event 标识可投递给状态机的一个事件。
type Event string

// Transition 描述一条转移：在 From 状态收到 Event 后进入 To。
type Transition struct {
	From  State
	Event Event
	To    State
}

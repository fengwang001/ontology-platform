package fsm

// State 标识状态机中的一个状态。
type State string

// Event 标识可以投递给状态机的一个事件。
type Event string

// Transition 描述一条转移：在 From 状态收到 Event 时迁移到 To。
type Transition struct {
	From  State
	Event Event
	To    State
}

// 三类拒绝原因，均可用 errors.Is 判定。
var (
	// ErrNoTransition 表示当前状态下该事件没有对应转移。
	ErrNoTransition = newStateError("fsm: no transition for event in current state")
	// ErrTerminal 表示机器已处于终态，终态吸收一切事件。
	ErrTerminal = newStateError("fsm: machine is in a terminal state")
	// ErrEntryFailed 表示目标状态的 entry 动作失败；
	// 用 errors.Unwrap 可取到 entry 动作返回的原始错误。
	ErrEntryFailed = newStateError("fsm: entry action failed")
)

// stateError 是一个稳定的哨兵错误类型，支持 errors.Is 与 %w 包裹。
type stateError string

func newStateError(msg string) error { return stateError(msg) }

func (e stateError) Error() string { return string(e) }

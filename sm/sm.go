// Package sm 是确定性状态机：命令类型与纯转移函数。
// 本包不依赖任何其他包。
package sm

// Op 是命令种类；零值 Op(0) 不是合法命令。
type Op int

const (
	Add Op = iota + 1
	Mul
)

// Command 是一条确定性命令：Add 使状态 += K，Mul 使状态 *= K。
type Command struct {
	Op Op
	K  int
}

// Valid 报告命令是否可识别；零值/未识别命令视为空命令。
func (c Command) Valid() bool {
	return c.Op == Add || c.Op == Mul
}

// Apply 是纯转移函数：返回 state 在 cmd 作用下的下一状态。
// 对非法命令保持状态不变（正常流程中非法命令在入日志前即被拒绝）。
func Apply(cmd Command, state int) int {
	switch cmd.Op {
	case Add:
		return state + cmd.K
	case Mul:
		return state * cmd.K
	default:
		return state
	}
}

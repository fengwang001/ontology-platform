// Package policy 只含纯判定：给定现有记录、复用策略与冲突策略得出结论。
package policy

// Status 实例状态。
type Status int

// 实例状态枚举。
const (
	Running Status = iota
	Completed
	Failed
	Cancelled
	Terminated
)

// Reuse 复用策略。
type Reuse int

// 复用策略枚举。
const (
	AllowAll Reuse = iota
	AllowFailedOnly
	Reject
)

// Conflict 冲突策略。
type Conflict int

// 冲突策略枚举。
const (
	Fail Conflict = iota
	UseExisting
	Terminate
)

// Action policy.Decide 的结论。
type Action int

// 判定动作枚举。
const (
	// ActionCreate 无存活记录，允许新建。
	ActionCreate Action = iota
	// ActionReuse 现有记录已结束且允许复用，替换后新建。
	ActionReuse
	// ActionReturnExisting Running + UseExisting，返回现有 run 号。
	ActionReturnExisting
	// ActionTerminateAndCreate Running + Terminate 且权限已由调用方另行校验。
	ActionTerminateAndCreate
	// ActionRejectRunning Running + Fail。
	ActionRejectRunning
	// ActionDenyReuse 已结束记录按复用策略不可复用。
	ActionDenyReuse
)

// Record 纯判定使用的记录视图。
type Record struct {
	Run   int64
	Owner []byte
	State Status
	Ended int64
}

// Decide 给定现有记录 rec（nil 表示无存活记录）、复用策略与冲突策略，返回结论。
//
// Running：Fail -> ActionRejectRunning；UseExisting -> ActionReturnExisting；
// Terminate -> ActionTerminateAndCreate（权限由调用方在 registry 中校验）。
// 已结束：AllowAll 通过；AllowFailedOnly 仅 Failed/Cancelled/Terminated 通过；Reject 拒绝。
// 无记录：ActionCreate。
func Decide(rec *Record, reuse Reuse, conflict Conflict) Action {
	if rec == nil {
		return ActionCreate
	}
	if rec.State == Running {
		switch conflict {
		case Fail:
			return ActionRejectRunning
		case UseExisting:
			return ActionReturnExisting
		case Terminate:
			return ActionTerminateAndCreate
		}
		return ActionRejectRunning
	}
	switch reuse {
	case AllowAll:
		return ActionReuse
	case AllowFailedOnly:
		switch rec.State {
		case Failed, Cancelled, Terminated:
			return ActionReuse
		default:
			return ActionDenyReuse
		}
	case Reject:
		return ActionDenyReuse
	}
	return ActionDenyReuse
}

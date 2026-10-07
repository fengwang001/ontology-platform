// Package precheck provides read-only hypothetical re-precheck of Actions.
package precheck

// ErrorKind 是预检“解析阶段”四类互不相同的错误之一。
type ErrorKind string

const (
	// ErrActionNotYetDefined 指定历史时刻早于动作类型被定义的时刻。
	ErrActionNotYetDefined ErrorKind = "action_not_yet_defined"
	// ErrSnapshotMissing 钩子版本或权限继承快照因历史数据缺失无法重建。
	ErrSnapshotMissing ErrorKind = "snapshot_missing"
	// ErrCallerNotExist 调用者身份在指定历史时刻尚不存在。
	ErrCallerNotExist ErrorKind = "caller_not_exist"
	// ErrInvalidParams 动作参数违反当时生效的结构约束。
	ErrInvalidParams ErrorKind = "invalid_params"
)

// ResolutionError 描述预检在进入校验阶段之前发生的、按固定优先级唯一上报的错误。
type ResolutionError struct {
	Kind   ErrorKind
	Detail string
}

func (e *ResolutionError) Error() string {
	return string(e.Kind) + ": " + e.Detail
}

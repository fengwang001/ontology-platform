// Package recovery 实现快照损坏修复与动作重放的原子性协调器。
package recovery

import "fmt"

// ErrorKind 是可区分的错误类别。
type ErrorKind string

const (
	// KindSnapshotRecordCorrupt 快照记录损坏（对象级，按对象报告）。
	KindSnapshotRecordCorrupt ErrorKind = "snapshot_record_corrupt"
	// KindActionRecordCorrupt 动作日志记录损坏（整条记录不可用）。
	KindActionRecordCorrupt ErrorKind = "action_record_corrupt"
	// KindVersionMismatch 快照与日志版本不衔接。
	KindVersionMismatch ErrorKind = "version_mismatch"
	// KindOutOfCoverage 请求对象不在快照与日志的覆盖范围内。
	KindOutOfCoverage ErrorKind = "object_out_of_coverage"
)

// Error 携带一个可机读的错误类别与上下文。
type Error struct {
	Kind    ErrorKind
	Message string
	// Object 在快照记录损坏时为对应对象 ID。
	Object ObjectID
	// RecordIndex 在动作记录损坏时为记录的全局序号（从 0 开始）。
	RecordIndex int
}

func (e *Error) Error() string {
	switch e.Kind {
	case KindSnapshotRecordCorrupt:
		return fmt.Sprintf("snapshot record corrupt for object %q: %s", e.Object, e.Message)
	case KindActionRecordCorrupt:
		return fmt.Sprintf("action record corrupt at index %d: %s", e.RecordIndex, e.Message)
	case KindVersionMismatch:
		return fmt.Sprintf("snapshot/action-log version mismatch: %s", e.Message)
	default:
		return fmt.Sprintf("%s: %s", e.Kind, e.Message)
	}
}

// AsError 将任意 error 断言为 *Error；失败返回 nil。
func AsError(err error) *Error {
	if err == nil {
		return nil
	}
	if e, ok := err.(*Error); ok {
		return e
	}
	return nil
}

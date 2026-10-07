package compensate

import (
	"fmt"
)

// ErrKind 是子系统错误分类。优先级数字越小，报告优先级越高（固定优先级，见 kindPriority）。
type ErrKind int

const (
	// KindDependencyCycle 分支依赖环被拒绝（声明阶段，最高优先级）。
	KindDependencyCycle ErrKind = iota
	// KindUpstreamFailed 分支因上游分支最终失败而被动失败。
	KindUpstreamFailed
	// KindSubOperationFailed 分支子操作自身生效失败。
	KindSubOperationFailed
	// KindCompensationDenied 违反补偿顺序的直接补偿请求被拒绝。
	KindCompensationDenied
	// KindInverseFailed 逆操作（补偿）执行失败（最低优先级）。
	KindInverseFailed
)

// ErrorRecord 是一条可独立区分来源的错误记录。
type ErrorRecord struct {
	Kind     ErrKind
	ActionID string
	Branch   string
	OpIndex  int // -1 表示与具体子操作无关
	OpID     string
	Message  string
}

func (r ErrorRecord) Error() string {
	where := r.ActionID + "/" + r.Branch
	if r.OpIndex >= 0 {
		where = fmt.Sprintf("%s#%d(%s)", where, r.OpIndex, r.OpID)
	}
	return fmt.Sprintf("%s: %s: %s", kindName(r.Kind), where, r.Message)
}

func kindName(k ErrKind) string {
	switch k {
	case KindDependencyCycle:
		return "DEPENDENCY_CYCLE"
	case KindUpstreamFailed:
		return "UPSTREAM_FAILED"
	case KindSubOperationFailed:
		return "SUB_OP_FAILED"
	case KindCompensationDenied:
		return "COMPENSATION_ORDER_DENIED"
	case KindInverseFailed:
		return "INVERSE_FAILED"
	default:
		return "UNKNOWN"
	}
}

// kindPriority 返回固定报告优先级：数字越小越优先。
func kindPriority(k ErrKind) int { return int(k) }

package compensation

// ErrorKind 是补偿子系统对外暴露的固定错误分类。
//
// 数值越小，汇总报告时优先级越高。当多个错误条件同时成立时，
// Report 只返回优先级最高的那一类；详细的分支/子操作定位信息
// 仍然完整保留在对应字段中。
type ErrorKind int

const (
	// KindDependencyCycle 声明的分支依赖关系中存在环，动作声明被拒绝。
	KindDependencyCycle ErrorKind = iota + 1
	// KindUpstreamFailure 分支因它所依赖的上游分支最终失败而被动失败。
	KindUpstreamFailure
	// KindOperationFailure 分支内部某个子操作自身生效失败。
	KindOperationFailure
	// KindCompensationOrderViolation 在全部下游分支完成补偿之前，
	// 外部直接请求补偿某分支，被拒绝。
	KindCompensationOrderViolation
	// KindUndoFailure 已生效子操作的逆操作执行失败。
	KindUndoFailure
)

// String 返回错误分类的固定名称。
func (k ErrorKind) String() string {
	switch k {
	case KindDependencyCycle:
		return "dependency-cycle"
	case KindUpstreamFailure:
		return "upstream-failure"
	case KindOperationFailure:
		return "operation-failure"
	case KindCompensationOrderViolation:
		return "compensation-order-violation"
	case KindUndoFailure:
		return "undo-failure"
	default:
		return "unknown"
	}
}

// Priority 数值越小优先级越高；同时满足多个条件时取最高优先级上报。
func (k ErrorKind) Priority() int { return int(k) }

// BranchError 记录单条分支上的一个错误事件。同一个分支在一次
// 补偿过程中可能累积多条 BranchError（例如多个逆操作分别失败），
// 它们彼此独立保留，互不遮蔽。
type BranchError struct {
	Kind       ErrorKind
	BranchName string
	// StepIndex 是分支内子操作的下标（声明顺序，0 起）；
	// 环检测与顺序违例这类不针对单个子操作的错误取 -1。
	StepIndex int
	// Cause 是底层原因（子操作返回的错误等），可为 nil。
	Cause error
	// Detail 保存用于定位的补充信息，例如环路径。
	Detail string
}

func (e *BranchError) Error() string {
	s := e.Kind.String() + " branch=" + e.BranchName
	if e.StepIndex >= 0 {
		s += " step=" + itoa(e.StepIndex)
	}
	if e.Detail != "" {
		s += " " + e.Detail
	}
	if e.Cause != nil {
		s += ": " + e.Cause.Error()
	}
	return s
}

func (e *BranchError) Unwrap() error { return e.Cause }

// Report 是一次动作执行/补偿的完整错误汇总。
//
// 即使存在 KindUndoFailure 与 KindOperationFailure 等多类错误，
// 每一条分支、每一个子操作的失败都分别保留在 Errors 中；Kind
// 只负责按固定优先级给出"首要错误类别"。
type Report struct {
	Errors []*BranchError
}

func (r *Report) Error() string {
	if r == nil || len(r.Errors) == 0 {
		return "no errors"
	}
	return r.Kind().String() + ": " + r.Errors[0].Error() +
		" (+" + itoa(len(r.Errors)-1) + " more)"
}

// Kind 按固定优先级返回汇总报告的首要错误类别；无错误时返回 0。
func (r *Report) Kind() ErrorKind {
	if r == nil || len(r.Errors) == 0 {
		return 0
	}
	top := r.Errors[0].Kind
	for _, e := range r.Errors[1:] {
		if e.Kind.Priority() < top.Priority() {
			top = e.Kind
		}
	}
	return top
}

// ByKind 返回指定类别的全部错误记录，保留各自的分支与子操作定位。
func (r *Report) ByKind(k ErrorKind) []*BranchError {
	var out []*BranchError
	if r == nil {
		return out
	}
	for _, e := range r.Errors {
		if e.Kind == k {
			out = append(out, e)
		}
	}
	return out
}

// cycleError 是声明阶段环检测失败返回给调用方的错误。
type cycleError struct {
	cycle []string
}

func (e *cycleError) Error() string {
	msg := "dependency cycle detected among branches:"
	for _, name := range e.cycle {
		msg += " " + name
	}
	return msg
}

// orderViolationError 是直接补偿请求违反逆向依赖次序时返回的错误。
type orderViolationError struct {
	branch   string
	blockers []string
}

func (e *orderViolationError) Error() string {
	msg := "compensation of branch " + e.branch +
		" rejected: downstream branches not yet compensated:"
	for _, name := range e.blockers {
		msg += " " + name
	}
	return msg
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}

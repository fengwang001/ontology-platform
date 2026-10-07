package snapshot

import "fmt"

// ErrorKind 区分四类导出错误。判定与报告遵循固定优先级，
// 数值越小优先级越高，不允许混用或互相掩盖。
type ErrorKind int

const (
	// ErrBoundaryConflict 边界位置本身的定义冲突：请求的边界位置
	// 不存在（超出已接受写入范围），或恰好落在一笔事务中间，
	// 导致边界无法被明确定义。优先级最高：边界未定义时，任何
	// 归属判定与完整性核对都失去意义。
	ErrBoundaryConflict ErrorKind = iota
	// ErrAtomicityConflict 写入顺序导致的原子性冲突：同一笔事务
	// 的写入在日志中被其他事务的写入隔开，无法聚合成一条增量
	// 记录，也就无法保证该事务整体可见或整体不可见。优先级次之：
	// 可见性单元（事务）未确定前，引用完整性无从谈起。
	ErrAtomicityConflict
	// ErrReferentialConflict 写入顺序导致的引用完整性冲突：一条
	// 链接在某个可见状态中已出现，而其端点对象尚未出现。
	ErrReferentialConflict
	// ErrResourceExhausted 因资源不足被迫中止。优先级最低：它是
	// 环境性、瞬态的失败，不能掩盖确定性的逻辑错误。
	ErrResourceExhausted
)

// Rank 返回错误的固定优先级，数值越小越优先。
func (k ErrorKind) Rank() int { return int(k) }

func (k ErrorKind) String() string {
	switch k {
	case ErrBoundaryConflict:
		return "boundary-conflict"
	case ErrAtomicityConflict:
		return "atomicity-conflict"
	case ErrReferentialConflict:
		return "referential-conflict"
	case ErrResourceExhausted:
		return "resource-exhausted"
	default:
		return "unknown"
	}
}

// ExportError 是导出过程中产生的错误，携带类别、规则标识与现场信息。
type ExportError struct {
	Kind   ErrorKind
	Rule   string // 触发判定所依据的规则标识，便于复核
	Detail string
	LSN    LSN // 相关写入的位置；0 表示不适用
}

func (e *ExportError) Error() string {
	if e.LSN != 0 {
		return fmt.Sprintf("%s [%s] at LSN %d: %s", e.Kind, e.Rule, e.LSN, e.Detail)
	}
	return fmt.Sprintf("%s [%s]: %s", e.Kind, e.Rule, e.Detail)
}

// 规则标识常量：每次判定都记录所依据的规则，供测试与复核引用。
const (
	RuleBoundaryInclusive  = "R1-boundary-inclusive"   // 边界含端点：LSN<=N 归快照
	RuleBoundaryExists     = "R2-boundary-must-exist"  // 边界位置必须已存在
	RuleBoundaryTxnSafe    = "R3-boundary-txn-safe"    // 边界不得切分事务
	RuleTxnContiguous      = "R4-txn-contiguous"       // 同事务写入必须连续
	RuleLinkEndpointsFirst = "R5-link-endpoints-first" // 链接可见前端点须可见
	RuleResourceLimit      = "R6-resource-limit"       // 资源配额
)

// Precedes 报告 err 是否应按固定优先级先于 other 被报告。
func Precedes(err, other *ExportError) bool {
	if other == nil {
		return true
	}
	if err == nil {
		return false
	}
	return err.Kind.Rank() < other.Kind.Rank()
}

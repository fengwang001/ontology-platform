// Package compensate 实现本体平台的「变更流消费 + 恰好一次补偿」子系统。
//
// 子系统的职责：消费表示某个本体动作（ActionExecution）已经成功执行的变更事件，
// 并对每条这样的事件恰好触发一次关联的补偿型动作（compensation action）。
// 补偿动作由若干项幂等副作用（effect）组成，以 saga 风格逐项提交；
// 即使消费者在中途崩溃重启，续作也只会补齐尚未生效的副作用，
// 而不会把已生效的副作用再施加一遍。
package compensate

import "errors"

// SemanticVersion 语义版本。
const SemanticVersion = "1.0.0"

// Event 是变更流中的一条事件，表示一次动作执行（ActionExecution）已经成功提交。
//
// 去重身份的约定（区分「重复投递」与「独立等价调用」的唯一依据）：
//   - EventID 由生产者（动作执行端）在动作提交的事务内一次性分配并随变更流持久化。
//     同一次动作执行无论因网络重试被投递多少次，携带的 EventID 永远相同。
//   - 调用者在另一个时刻再次发起的、参数与效果恰好等价的调用是一次全新的动作执行，
//     生产者必须为其分配一个全新的 EventID（哪怕 ActionType/参数/结果完全一致）。
//
// 因此：EventID 相同 ⇒ 同一次执行的重复投递（跳过补偿）；
// EventID 不同但内容等价 ⇒ 两次独立调用（各自补偿一次）。
// 系统不依据动作参数、时间窗口或内容指纹做任何去重推断。
type Event struct {
	// EventID 是动作执行标识（ActionExecutionID），全局唯一、不可变、永不复用。
	EventID string
	// ActionType 是原始动作类型，用于查找其关联的补偿动作规格。
	ActionType string
	// Payload 是补偿动作所需的上下文（对象键、参数等）。
	Payload map[string]string
}

// TerminalError 是消费过程中被分类报告的四类互斥业务错误。
// 每条事件最多只报告其中一类；同时具备多类条件时按优先级只报告最高优先级者。
type TerminalError struct {
	Class ErrorClass
	Event Event
	msg   string
}

func (e *TerminalError) Error() string { return string(e.Class) + ": " + e.msg }

// ErrorClass 是四类互不相同的终端错误。
type ErrorClass string

const (
	// ErrAmbiguousIdentity（E3，最高优先级）：
	// 事件携带的标识信息无法判断它与此前已处理事件的关系。
	// 典型情形：EventID 缺失，或同一 EventID 此前已处理但负载与首次投递不一致
	//（可能是 ID 冲突/流损坏，也可能是不同事件被错误复用了同一 ID）。
	ErrAmbiguousIdentity ErrorClass = "E3_AMBIGUOUS_IDENTITY"

	// ErrHistoryMissing（E2）：
	// 发现补偿此前已部分生效（崩溃分裂场景），但续作所需的历史记录缺失，
	// 无法确定边界。系统冻结在已确定的边界上，绝不猜测、绝不重放副作用。
	ErrHistoryMissing ErrorClass = "E2_HISTORY_MISSING"

	// ErrAtomicityViolation（E4）：
	// 补偿动作自身的事务原子性校验失败——存储中的实际状态与补偿记录不一致
	//（例如记录声称未开始，但对象上已能探测到该补偿的效果痕迹）。
	ErrAtomicityViolation ErrorClass = "E4_ATOMICITY_VIOLATION"

	// ErrTargetGone（E1，最低优先级）：
	// 补偿目标对象在消费该事件时已不存在或已被撤销。
	ErrTargetGone ErrorClass = "E1_TARGET_GONE"
)

// ClassPriority 返回错误分类的报告优先级（数值越大优先级越高）。
func ClassPriority(c ErrorClass) int {
	switch c {
	case ErrAmbiguousIdentity:
		return 40
	case ErrHistoryMissing:
		return 30
	case ErrAtomicityViolation:
		return 20
	case ErrTargetGone:
		return 10
	default:
		return -1
	}
}

// 存储层与处理器交互时使用的哨兵错误。
var (
	// ErrNotFound 表示键不存在。
	ErrNotFound = errors.New("compensate: key not found")
	// ErrConflict 表示乐观并发冲突，调用方应当在可串行化事务内重试。
	ErrConflict = errors.New("compensate: serializable conflict")
	// ErrCanceled 表示补偿记录尚未领取前，撤销请求已经赢了竞态。
	ErrCanceled = errors.New("compensate: superseded by undo before claim")
)

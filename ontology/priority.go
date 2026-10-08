// Package ontology 提供本体对象存储及其并发写入的公平冲突仲裁能力。
//
// 本文件定义冲突仲裁的核心概念：票据（Ticket）、优先级分数与全序比较。
// 所有判定仅依赖实例内部的逻辑时钟，不依赖任何墙钟时间，
// 因此不会受到系统负载或时钟漂移的影响。
package ontology

// FailureWeight 是失败次数在优先级分数中的权重。
// 只要权重 >= 1，即可保证"任何已登记等待的请求严格优先于新到达请求"
// 这一关键不变式（详见设计文档）。
const FailureWeight uint64 = 1

// RequestID 唯一标识一次逻辑写入请求。同一请求的重试必须复用同一 ID。
type RequestID string

// Ticket 是请求在多次重试间携带的优先级依据。
// 它由仲裁器在请求首次到达时签发，并在每次失败后由仲裁器更新；
// 调用方必须原样携带，仲裁器侧同时保留权威副本以防止被重置。
type Ticket struct {
	// ArrivalSeq 是请求首次到达时分配的实例逻辑序号，签发后不再变化。
	ArrivalSeq uint64
	// Failures 是该请求迄今为止经历的失败次数（写冲突 + 被挤出）。
	Failures uint64
}

// Score 计算请求在实例逻辑时刻 now 的优先级分数。
// 分数对 Failures 与等待长度（now - ArrivalSeq）均单调不减：
// 等待越久、失败越多，分数只会升高不会降低。
func (t Ticket) Score(now uint64) uint64 {
	return t.Failures*FailureWeight + (now - t.ArrivalSeq)
}

// priorityKey 是参与裁定的完整优先级依据，构成确定性的全序。
type priorityKey struct {
	score   uint64
	arrival uint64
	id      RequestID
}

// keyOf 由票据与当前逻辑时刻构造优先级键。
func keyOf(id RequestID, t Ticket, now uint64) priorityKey {
	return priorityKey{score: t.Score(now), arrival: t.ArrivalSeq, id: id}
}

// less 报告 a 的优先级是否严格低于 b。
// 判定顺序：分数低者劣后；分数相同则晚到达者劣后；
// 仍相同（仅可能出现在防御性场景）则请求 ID 字典序大者劣后。
// 由于同一实例内 ArrivalSeq 与 RequestID 均唯一，该序为全序，
// 任何两个不同请求的比较结果都是确定且可复现的。
func (a priorityKey) less(b priorityKey) bool {
	if a.score != b.score {
		return a.score < b.score
	}
	if a.arrival != b.arrival {
		return a.arrival > b.arrival
	}
	return a.id > b.id
}

// Outcome 标识一次写入尝试的结果。各种结果彼此互斥，
// 调用方可凭返回值单独识别每一种情形。
type Outcome int

const (
	// OutcomeCommitted 表示写入已提交，版本号已推进。
	OutcomeCommitted Outcome = iota
	// OutcomeConflict 表示乐观版本检查失败（写冲突），可立即重试。
	OutcomeConflict
	// OutcomePreempted 表示在本轮裁定中因优先级落后被挤出，可稍后重试。
	OutcomePreempted
	// OutcomeExhausted 表示重试次数已达上限，为最终失败，不再可重试。
	OutcomeExhausted
)

// String 返回结果的可读名称，用于日志与测试断言。
func (o Outcome) String() string {
	switch o {
	case OutcomeCommitted:
		return "committed"
	case OutcomeConflict:
		return "conflict"
	case OutcomePreempted:
		return "preempted"
	case OutcomeExhausted:
		return "exhausted"
	default:
		return "unknown"
	}
}

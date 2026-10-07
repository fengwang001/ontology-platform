// Package incremental 实现本体平台增量导出的位点推进与去重合并组件。
//
// 组件划分为四个职责清晰的部分：
//   - CursorStore / Ledger：位点的声明与确认（cursor.go、ledger.go）
//   - CycleDeduper：历史记录的去重判定（dedup.go）
//   - DeriveSafeCursor：位点记录不可读时的安全推导（ledger.go）
//   - Exporter / Cycle：周期编排、错误判定与多链路并发（exporter.go）
package incremental

import "fmt"

// ErrorKind 区分四类互不相同、判定顺序固定的错误。
//
// 判定优先级（数字越小优先级越高）固定为：
//  1. ErrKindStartMismatch     起始位点与上一次确认的结束位点不匹配
//  2. ErrKindCursorUnreadable  位点记录本身不可读，需要重新推导
//  3. ErrKindHistoryGap        推导过程中发现历史增量记录存在缺口
//  4. ErrKindResourceExhausted 输出过程中资源不足被迫中止
//
// 理由：该顺序与导出流水线“声明位点 -> 读取位点 -> 推导位点 -> 执行输出”
// 的阶段顺序一致。起始位点不匹配是调用方违反协议的逻辑错误，继续执行可能
// 污染整条链路的输出，必须最先暴露；位点不可读触发恢复流程，居于其次；
// 历史缺口只有在恢复流程（推导）中才可能被发现，故排在第三；资源不足只
// 可能发生在输出阶段，天然位于最后。当多种情况同时可判定时，按此固定
// 顺序报告，保证行为可复现、可测试。
//
// 一条补充规则：当推导因历史缺口失败时，已确认位点的权威值不可得，
// 起始位点一致性校验无从适用，此时直接报告历史缺口（缺口本身已携带
// 不晚于真正已确认位点的安全位点，调用方可据此重新发起）。
type ErrorKind int

const (
	ErrKindStartMismatch ErrorKind = iota
	ErrKindCursorUnreadable
	ErrKindHistoryGap
	ErrKindResourceExhausted
)

// Priority 返回判定优先级，数值越小越优先。
func (k ErrorKind) Priority() int { return int(k) }

func (k ErrorKind) String() string {
	switch k {
	case ErrKindStartMismatch:
		return "start-mismatch"
	case ErrKindCursorUnreadable:
		return "cursor-unreadable"
	case ErrKindHistoryGap:
		return "history-gap"
	case ErrKindResourceExhausted:
		return "resource-exhausted"
	default:
		return "unknown"
	}
}

// Error 是组件返回的带类别错误。
type Error struct {
	Kind   ErrorKind
	Chain  string
	Detail string
	// Findings 记录本次判定过程中发生的非致命恢复事件
	// （例如位点不可读后已成功推导出安全位点，但随后的起始位点
	// 一致性校验失败）。
	Findings []Finding
	// SafeCursor 仅在 Kind == ErrKindHistoryGap 时有意义：
	// 推导在历史缺口处停止时得到的、不晚于真正已确认位点的安全位点。
	SafeCursor Cursor
	// HasSafeCursor 指示 SafeCursor 字段是否有效。
	HasSafeCursor bool
}

func (e *Error) Error() string {
	return fmt.Sprintf("incremental: chain %q: %s: %s", e.Chain, e.Kind, e.Detail)
}

// newError 构造一个组件错误。
func newError(kind ErrorKind, chain, detail string) *Error {
	return &Error{Kind: kind, Chain: chain, Detail: detail}
}

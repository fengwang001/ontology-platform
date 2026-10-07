// Package importer 实现本体平台的批量导入子系统。
//
// 子系统接收可能乱序、重复到达的数据块，尽早落地依赖已满足的条目，
// 记录跨块悬挂引用并在被引用块到达后自动重试，对重复到达、迟到块、
// 达到块数上限、提前结束等情形给出确定且可区分的处理结果。
package importer

import (
	"crypto/sha256"
	"encoding/binary"
	"hash"
	"sort"
)

// Entry 是待导入的本体对象实例描述。
type Entry struct {
	// ID 在任务内唯一标识该条目。
	ID string
	// References 列出该条目引用的其他条目 ID，被引用条目可能位于尚未到达的块中。
	References []string
	// Payload 为条目携带的不透明数据，具体解释由调用方（校验器）决定。
	Payload string
}

// Chunk 是一次导入任务下的一个数据分块。
type Chunk struct {
	// JobID 标识块所属的导入任务。
	JobID string
	// Seq 为块在任务内的序号，同一序号的块可能因客户端重试而重复到达。
	Seq int
	// Entries 为该块携带的条目。
	Entries []Entry
}

// Hash 返回块内容的确定性摘要。
//
// 重复到达判定只依赖序号与该摘要，不依赖到达时间：序号相同且摘要相同
// 视为同一块的重复投递，序号相同而摘要不同视为内容不一致的冲突。
func (c Chunk) Hash() [32]byte {
	h := sha256.New()
	var buf [8]byte
	binary.LittleEndian.PutUint64(buf[:], uint64(int64(c.Seq)))
	h.Write(buf[:])
	for _, e := range c.Entries {
		writeString(h, e.ID)
		writeString(h, e.Payload)
		refs := append([]string(nil), e.References...)
		sort.Strings(refs)
		binary.LittleEndian.PutUint64(buf[:], uint64(len(refs)))
		h.Write(buf[:])
		for _, r := range refs {
			writeString(h, r)
		}
	}
	var sum [32]byte
	h.Sum(sum[:0])
	return sum
}

func writeString(h hash.Hash, s string) {
	var buf [8]byte
	binary.LittleEndian.PutUint64(buf[:], uint64(len(s)))
	h.Write(buf[:])
	h.Write([]byte(s))
}

// EntryStatus 描述条目当前所处的状态，各取值两两可区分。
type EntryStatus int

const (
	// StatusUnknown 表示该条目从未在任何被受理的块中出现过。
	StatusUnknown EntryStatus = iota
	// StatusLanded 表示条目已成功落地。
	StatusLanded
	// StatusFailed 表示条目已确定失败（传播失败或自身校验失败）。
	StatusFailed
	// StatusPending 表示条目因悬挂引用仍在等待。
	StatusPending
	// StatusTimeout 表示条目因达到块数上限仍存在悬挂引用而被判定超时失败。
	StatusTimeout
	// StatusConflict 表示条目首次出现于一个内容不一致的重复块中，未被处理。
	StatusConflict
)

// String 返回状态的可读名称，用于日志与调试。
func (s EntryStatus) String() string {
	switch s {
	case StatusUnknown:
		return "unknown"
	case StatusLanded:
		return "landed"
	case StatusFailed:
		return "failed"
	case StatusPending:
		return "pending"
	case StatusTimeout:
		return "timeout"
	case StatusConflict:
		return "conflict"
	default:
		return "invalid"
	}
}

// Category 是错误类别。多个条件同时满足时按优先级从高到低报告，
// 数值越小优先级越高（CategoryNone 除外，表示无错误）。
type Category int

const (
	// CategoryNone 表示无错误。
	CategoryNone Category = iota
	// CategoryChunkConflict 表示块序号重复且内容不一致（优先级最高）。
	CategoryChunkConflict
	// CategoryReferenceFailed 表示引用的条目已确定失败导致的传播失败。
	CategoryReferenceFailed
	// CategoryDanglingTimeout 表示达到块数上限后悬挂引用超时失败。
	CategoryDanglingTimeout
	// CategoryEntryInvalid 表示条目自身校验失败（含循环引用）。
	CategoryEntryInvalid
	// CategoryJobTerminated 表示任务提前结束后到达的块被拒绝（优先级最低）。
	CategoryJobTerminated
)

// String 返回错误类别的可读名称。
func (c Category) String() string {
	switch c {
	case CategoryNone:
		return "none"
	case CategoryChunkConflict:
		return "chunk-conflict"
	case CategoryReferenceFailed:
		return "reference-failed"
	case CategoryDanglingTimeout:
		return "dangling-timeout"
	case CategoryEntryInvalid:
		return "entry-invalid"
	case CategoryJobTerminated:
		return "job-terminated"
	default:
		return "invalid"
	}
}

// EntryInfo 是查询条目状态时返回的只读快照。
type EntryInfo struct {
	Status   EntryStatus
	Category Category
	Detail   string
}

// SubmitOutcome 描述一次块提交的受理结果。
type SubmitOutcome int

const (
	// OutcomeAccepted 表示块被受理并完成处理。
	OutcomeAccepted SubmitOutcome = iota
	// OutcomeDuplicate 表示块是内容一致的重复到达，已被幂等忽略。
	OutcomeDuplicate
	// OutcomeRejected 表示块被拒绝，具体原因见 SubmitResult.Category。
	OutcomeRejected
)

// SubmitResult 是 SubmitChunk 的返回结果。
type SubmitResult struct {
	Outcome  SubmitOutcome
	Category Category
	Err      error
}

// Stats 是任务内部状态的只读快照，主要用于验证悬挂引用记录的开销
// 只随当前悬挂数量增长（EvalOps/ScanOps 与已处理条目总数无关）。
type Stats struct {
	ArrivedChunks int
	Landed        int
	Failed        int
	Timeout       int
	Pending       int
	Conflict      int
	// DanglingRefs 为当前仍处于悬挂状态的引用边数量。
	DanglingRefs int
	// EvalOps 为累计条目求值次数，只与悬挂解析事件相关。
	EvalOps int64
	// ScanOps 为累计在悬挂集合上扫描的次数，不触碰已落地条目。
	ScanOps int64
}

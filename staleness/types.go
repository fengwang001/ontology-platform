// Package staleness 提供分区级数据资产的过期判定、最小回填规划与重写影响面分析。
//
// 资产按整数日序号分区，声明范围 [first,last]；依赖边（上游 U -> 下游 D，偏移 lo<=hi）
// 表示 D 的分区 d 读取 U 的分区 [d+lo, d+hi] 闭区间，落在 U 声明范围外的忽略。
package staleness

// PartitionRef 定位一个资产的一个分区。
type PartitionRef struct {
	Asset     string
	Partition int
}

// Kind 区分可被调用方程序化处理的不同拒绝原因。
type Kind int

const (
	KindUnknownAsset           Kind = iota // 引用了不存在的资产
	KindDuplicateAsset                     // 重复声明同名资产
	KindDuplicateEdge                      // 重复声明同一对上下游依赖边
	KindCycle                              // 依赖成环（含自依赖）
	KindInvalidOffset                      // 偏移 lo > hi
	KindInvalidRange                       // 声明范围 first > last
	KindPartitionOutOfRange                // 操作的分区不在声明范围内
	KindAlreadyRunning                     // 同一分区已有运行中的物化
	KindInputsNotReady                     // 存在缺失或过期的输入分区
	KindRunNotFound                        // 运行号不存在或已结束
	KindNonSourceExternalWrite             // 对非源资产做外部写入
)

// Error 是所有被拒绝操作返回的错误类型，Kind 字段可区分原因。
// 被拒绝的操作不改变任何状态。
type Error struct {
	Kind    Kind
	Message string
	// Missing 与 Stale 仅在 Kind == KindInputsNotReady 时填充，
	// 按（资产名，分区号）升序列出全部未就绪输入。
	Missing []PartitionRef
	Stale   []PartitionRef
}

func (e *Error) Error() string { return e.Message }

func newError(kind Kind, msg string) *Error {
	return &Error{Kind: kind, Message: msg}
}

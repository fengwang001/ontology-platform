// Package bitemporal 提供双时态链接关系的历史一致性审计子系统。
//
// 两条时间轴：
//   - 有效时间 valid time：链接事实在业务世界中成立/失效的时间；
//   - 记录时间 record time：事实被系统知晓（记入日志）的时间，只增不改。
//
// 所有事实只追加（append-only），审计永远不修改任何历史事实。
package bitemporal

import "errors"

// ID 是对象、对象类型、链接类型的统一标识符类型。
type ID string

// Card 表示链接某一端的基数约束：每个该端对象可关联的对端对象数量区间 [Min, Max]。
// Max 为 0 表示无上界。
type Card struct {
	Min int
	Max int
}

// Cardinality 同时规定正向（source -> target）与反向（target -> source）基数。
type Cardinality struct {
	Forward Card
	Reverse Card
}

// LinkType 描述一种链接类型。
//
// Symmetric 为 true 时两端对象类型必须相同，链接关系天然对称：
// (a, b) 与 (b, a) 是同一条事实，任一端记录一次即应双向可见。
type LinkType struct {
	ID         ID
	SourceType ID
	TargetType ID
	Symmetric  bool
}

// LinkTypeRule 是基数约束在某个记录时间点生效的版本。
type LinkTypeRule struct {
	LinkType LinkType
	Card     Cardinality
	// FromRecord 是该版本开始生效的记录时间（含）。
	FromRecord int64
}

// ObjectTypeRecord 记录对象类型的诞生时刻（记录时间）。
type ObjectTypeRecord struct {
	ID         ID
	BornRecord int64
}

// 事实种类。
const (
	kindCreate = 1
	kindRevoke = 2
)

// linkFact 是一条不可变的双时态链接事实（内部表示）。
type linkFact struct {
	linkType ID
	// 规范化后的有序端点。
	src ID
	dst ID
	// 非对称链接：src/dst 按 LinkType.SourceType->TargetType 方向规范化。
	// 对称链接：src < dst 规范化为无序对。
	symmetric bool

	kind       int // kindCreate / kindRevoke
	validTime  int64
	recordTime int64

	// origin 仅用于内部对账（检测对称链接单侧缺失），
	// 永不出现在任何对外的回放/审计结果中。
	origin string
	// token 用于区分“同一无序对的两个方向各记录一次”这一物理事实，
	// 仅内部使用。取值 "ab" / "ba"。
	token string
}

// Edge 是回放结果中的一条有向链接实例。
type Edge struct {
	LinkType ID
	From     ID
	To       ID
}

// Segment 是审计结果在记录时间轴上的一个分段。
type Segment struct {
	// RecordStart/RecordEnd 为该分段的记录时间区间 [Start, End)。
	RecordStart int64
	RecordEnd   int64
	// ValidTime 为该分段审计时采用的有效时间。
	ValidTime int64
	// Rule 为本分段实际生效的基数约束版本。
	Rule LinkTypeRule
	// Violations 为该分段中违反基数的对象列表。
	Violations []Violation
}

// Violation 描述单个对象在某个方向上的基数违反。
type Violation struct {
	// Direction 为 "forward"（源->目标出度）或 "reverse"（目标->源出度）。
	Direction string
	Object    ID
	Outgoing  int
	Card      Card
}

// 审计错误（按优先级从高到低，审计只报告其中一类）。
var (
	// ErrIntervalContradiction：请求的记录时间区间自相矛盾（E3，优先级最高）。
	ErrIntervalContradiction = errors.New("bitemporal: contradictory record-time interval")
	// ErrObjectTypeMissing：链接类型端点对象类型在请求记录时刻尚不存在（E2）。
	ErrObjectTypeMissing = errors.New("bitemporal: endpoint object type does not exist at record time")
	// ErrRuleVersionSuperseded：审计所依据的基数约束版本在处理期间被作废（E1）。
	ErrRuleVersionSuperseded = errors.New("bitemporal: cardinality rule basis superseded during audit")
	// ErrMirrorStructural：镜像一致性结构性缺失（E4，如对称链接仅单侧有事实）。
	ErrMirrorStructural = errors.New("bitemporal: structural mirror inconsistency")
)

// AuditError 携带审计错误的结构化细节。
type AuditError struct {
	Kind error
	// Detail 为可核查的技术细节，不包含任何记录来源信息。
	Detail string
}

func (e *AuditError) Error() string {
	if e == nil || e.Kind == nil {
		return "bitemporal: audit error"
	}
	return e.Kind.Error() + ": " + e.Detail
}

func (e *AuditError) Unwrap() error { return e.Kind }

// MirrorDefect 描述一次镜像对账发现的结构性不一致。
type MirrorDefect struct {
	LinkType ID
	A        ID
	B        ID
	// MissingSide 取值 "ab"/"ba"，表示缺失的是哪个方向的事实；
	// 该字段只描述结构方向，不描述是“谁记录的”。
	MissingSide string
	ValidTime   int64
	RecordTime  int64
}

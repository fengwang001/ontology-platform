package ontology

// Side 标识实例在某一链接类型上所处的一侧。
type Side uint8

const (
	SideInvalid Side = 0
	SideA       Side = 1
	SideB       Side = 2
)

// Opposite 返回另一侧。
func (s Side) Opposite() Side {
	switch s {
	case SideA:
		return SideB
	case SideB:
		return SideA
	default:
		return SideInvalid
	}
}

func (s Side) String() string {
	switch s {
	case SideA:
		return "A"
	case SideB:
		return "B"
	default:
		return "invalid"
	}
}

// Cardinality 是单侧基数上限：处在该侧的单个实例最多关联 Max 个另一侧实例。
// Max<=0 视为不设置约束（nil 同理）。
type Cardinality struct {
	Max int
}

// LinkType 声明一个链接类型及其 A/B 两侧各自的基数约束（可为 nil）。
type LinkType struct {
	ID           string
	CardinalityA *Cardinality // 每个 A 实例最多关联的 B 数量
	CardinalityB *Cardinality // 每个 B 实例最多关联的 A 数量
}

// Constraint 返回指定侧的基数约束，无约束时返回 nil。
func (lt *LinkType) Constraint(side Side) *Cardinality {
	switch side {
	case SideA:
		return lt.CardinalityA
	case SideB:
		return lt.CardinalityB
	default:
		return nil
	}
}

// ConstraintKey 唯一标识“某个链接类型某一侧”的基数约束。
type ConstraintKey struct {
	TypeID string
	Side   Side
}

// Link 是一条 A↔B 关联。去重键为 (TypeID, A, B)。
type Link struct {
	TypeID string
	A      string
	B      string
}

// Key 返回链接的规范化去重键。
func (l Link) Key() string { return l.TypeID + "\x00" + l.A + "\x00" + l.B }

// holder 返回指定侧上的实例 ID。
func (l Link) holder(side Side) string {
	if side == SideA {
		return l.A
	}
	return l.B
}

// Op 是以目标实例为持有方的单次链接变更。
// Side 表示目标实例在该链接类型中处于哪一侧；Other 为另一侧实例。
type Op struct {
	TypeID string
	Side   Side
	Other  string
	Add    bool // true=建立关联，false=移除关联
}

// Request 是一次针对单个目标实例的逻辑写入请求。
type Request struct {
	Instance string
	Baseline uint64 // 调用方读取到的基线版本
	Ops      []Op
}

// AttemptOutcome 标识单次内部尝试的互斥结局。
type AttemptOutcome uint8

const (
	OutcomeConflict    AttemptOutcome = iota // 基线落后：版本冲突
	OutcomeCardinality                       // 最新读取下基数仍不满足
	OutcomeCommitted                         // 成功提交
)

// ConstraintVerdict 记录单次尝试中对单个约束的重新校验依据。
type ConstraintVerdict struct {
	Constraint ConstraintKey
	Current    int // 本次重新读取到的当前关联数（决策依据，O(1) 计数器）
	Delta      int // 本次变更对该约束计数的净增量
	Max        int // 上限；0 表示无约束
	Satisfied  bool
}

// AttemptRecord 完整记录一次内部尝试读取到的状态、判定依据与结局，供重放核验。
type AttemptRecord struct {
	Index       int
	VersionRead uint64 // 本次重新读取到的当前基线版本
	Verdicts    []ConstraintVerdict
	Snapshot    []Link // 本次重新读取到的目标实例全部链接（审计/重放用）
	Outcome     AttemptOutcome
}

// Rejection 是终态拒绝的结构化原因。
type Rejection struct {
	Code       Code
	Message    string
	Constraint *ConstraintKey
}

func (r *Rejection) Error() string {
	if r == nil {
		return ""
	}
	return r.Message
}

// Result 是一次逻辑写入请求的最终结果。
type Result struct {
	Committed bool
	Version   uint64
	Attempts  []AttemptRecord
	Reject    *Rejection
}

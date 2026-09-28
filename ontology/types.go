// Package ontology 实现带前像校验的变更复制组件。
//
// 副本表按批应用携带前后像的变更事件：仅当事件前像与副本中当前行
// 完全一致（按列名与值的集合比较，缺列与空串不相等）时才应用变更，
// 否则把该事件分类为冲突并跳过，冲突不改变副本状态。
package ontology

import "fmt"

// Row 是一行的列镜像：列名到列值的映射。
// 比较时按“列名与值的集合”进行：键集合不同即不同行，
// 因此“缺少某列”与“该列存在且值为空串”不相等。
type Row map[string]string

// Op 是变更事件的操作类型。
type Op string

const (
	// OpInsert 插入一行：要求前像为 nil（行不存在），后像为非 nil 的新行。
	OpInsert Op = "insert"
	// OpUpdate 更新一行：要求前像、后像均非 nil，且前像与当前行完全一致。
	OpUpdate Op = "update"
	// OpDelete 删除一行：要求前像非 nil、后像为 nil，且前像与当前行完全一致。
	OpDelete Op = "delete"
)

// Event 是一条携带前后像的变更事件。
type Event struct {
	// Seq 是事件在全局变更序列中的序号，批内必须与已处理序号严格连续。
	Seq int64
	// Key 是目标行的主键（副本表中的行标识）。
	Key string
	// Op 是操作类型。
	Op Op
	// Before 是前像：insert 必须为 nil；update/delete 必须非 nil。
	Before Row
	// After 是后像：delete 必须为 nil；insert/update 必须非 nil。
	After Row
}

// ConflictKind 是冲突的分类。
type ConflictKind string

const (
	// ConflictRowMissing 表示更新/删除的目标行在副本中不存在。
	ConflictRowMissing ConflictKind = "row_missing"
	// ConflictBeforeMismatch 表示行存在但前像与当前行不完全一致。
	ConflictBeforeMismatch ConflictKind = "before_mismatch"
	// ConflictRowExists 表示插入的目标行在副本中已存在。
	ConflictRowExists ConflictKind = "row_exists"
)

// Conflict 记录一条被分类为冲突的事件。冲突不是错误：
// 事件被跳过、不改变副本，但会进入冲突日志与批结果。
type Conflict struct {
	Seq    int64
	Key    string
	Op     Op
	Kind   ConflictKind
	Reason string
}

// RejectReason 是整批被拒绝的可区分原因。
type RejectReason string

const (
	// RejectInvalidEvent 表示批中存在非法事件（未知操作或前后像组合非法）。
	RejectInvalidEvent RejectReason = "invalid_event"
	// RejectSeqGap 表示事件序号与已处理序号不连续。
	RejectSeqGap RejectReason = "seq_gap"
	// RejectTooManyRows 表示应用本批会使副本行数超过上限。
	RejectTooManyRows RejectReason = "too_many_rows"
)

// RejectError 描述一次被拒绝的批，携带可区分的原因与定位信息。
type RejectError struct {
	Reason RejectReason
	// Index 是触发拒绝的事件在批内的下标（从 0 起）。
	Index int
	// Detail 是人类可读的补充说明。
	Detail string
}

func (e *RejectError) Error() string {
	return fmt.Sprintf("批被拒绝: reason=%s index=%d detail=%s", e.Reason, e.Index, e.Detail)
}

// BatchResult 是一批事件的应用结果。
type BatchResult struct {
	// Applied 是实际应用的事件数。
	Applied int
	// Conflicts 是本批分类出的冲突（按事件顺序）。
	Conflicts []Conflict
	// LastSeq 是应用成功后已处理的最后一个事件序号。
	LastSeq int64
}

// Snapshot 是某一批完整边界上的副本只读视图。
type Snapshot struct {
	rows map[string]Row
}

// Get 返回主键对应行的副本与是否存在。返回的 Row 是拷贝，
// 调用方修改它不会影响快照或副本表。
func (s *Snapshot) Get(key string) (Row, bool) {
	r, ok := s.rows[key]
	if !ok {
		return nil, false
	}
	cp := make(Row, len(r))
	for k, v := range r {
		cp[k] = v
	}
	return cp, true
}

// Len 返回快照中的行数。
func (s *Snapshot) Len() int { return len(s.rows) }

// Keys 返回快照中全部主键（顺序不保证）。
func (s *Snapshot) Keys() []string {
	keys := make([]string, 0, len(s.rows))
	for k := range s.rows {
		keys = append(keys, k)
	}
	return keys
}

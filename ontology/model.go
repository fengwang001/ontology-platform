package ontology

// Mode 是可延迟唯一约束在事务内的检查模式。
type Mode int

const (
	// IMMEDIATE 表示语句级即时检查。
	IMMEDIATE Mode = iota
	// DEFERRED 表示延迟到提交（或切回即时）时检查。
	DEFERRED
)

// Key 是行的键：IsNull 为真表示 NULL（空值），否则 Value 为字节字符串。
// NULL 与 NULL 互不相等，空串与 NULL 不同。
type Key struct {
	IsNull bool
	Value  string
}

// NullKey 返回空值（NULL）键。
func NullKey() Key { return Key{IsNull: true} }

// StringKey 返回非空字符串键（允许空串）。
func StringKey(v string) Key { return Key{Value: v} }

// Op 是一条语句内的单个操作。
type Op interface {
	opMarker()
}

// InsertOp 插入一行，行号必须不存在。
type InsertOp struct {
	Row string
	Key Key
}

// UpdateOp 更新一行的键，行号必须存在。
type UpdateOp struct {
	Row string
	Key Key
}

// DeleteOp 删除一行，行号必须存在。
type DeleteOp struct {
	Row string
}

func (InsertOp) opMarker() {}
func (UpdateOp) opMarker() {}
func (DeleteOp) opMarker() {}

// Insert 构造插入操作。
func Insert(row string, key Key) Op { return InsertOp{Row: row, Key: key} }

// Update 构造更新操作。
func Update(row string, key Key) Op { return UpdateOp{Row: row, Key: key} }

// Delete 构造删除操作。
func Delete(row string) Op { return DeleteOp{Row: row} }

// Reason 标识一次被拒绝操作的可区分原因。
type Reason int

const (
	ReasonOK Reason = iota
	// ReasonTransactionActive：Begin 时已有事务在进行。
	ReasonTransactionActive
	// ReasonNoTransaction：事务外调用 Apply/SetMode/Commit/Rollback。
	ReasonNoTransaction
	// ReasonInvalidMode：SetMode 的模式不是 IMMEDIATE/DEFERRED。
	ReasonInvalidMode
	// ReasonNotDeferrable：不可延迟约束上调用 SetMode。
	ReasonNotDeferrable
	// ReasonInitiallyDeferredRequiresDeferrable：构造时 initiallyDeferred 为真但 deferrable 为假。
	ReasonInitiallyDeferredRequiresDeferrable
	// ReasonEmptyRow：操作的行号为空字符串。
	ReasonEmptyRow
	// ReasonRowExists：Insert 的行号已存在。
	ReasonRowExists
	// ReasonRowNotFound：Update/Delete/Get 的行号不存在。
	ReasonRowNotFound
	// ReasonUniqueViolation：唯一约束违例。
	ReasonUniqueViolation
)

// Error 携带被拒绝操作的原因与定位信息。
type Error struct {
	Reason Reason
	// OpIndex 是 Apply 中导致失败的 op 下标（即时检查类失败）；其余场景为 -1。
	OpIndex int
	// ViolatedKey 是唯一约束违例时字节序最小的违例键。
	ViolatedKey string
}

func (e *Error) Error() string {
	return reasonString(e.Reason)
}

func reasonString(r Reason) string {
	switch r {
	case ReasonTransactionActive:
		return "transaction already active"
	case ReasonNoTransaction:
		return "no active transaction"
	case ReasonInvalidMode:
		return "invalid constraint mode"
	case ReasonNotDeferrable:
		return "constraint is not deferrable"
	case ReasonInitiallyDeferredRequiresDeferrable:
		return "initiallyDeferred requires deferrable"
	case ReasonEmptyRow:
		return "row identifier is empty"
	case ReasonRowExists:
		return "row already exists"
	case ReasonRowNotFound:
		return "row not found"
	case ReasonUniqueViolation:
		return "unique constraint violation"
	default:
		return "ok"
	}
}

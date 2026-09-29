package replication

import "strconv"

// Record 是逻辑日志中的一条记录。记录按递增的 LSN 排列，
// 属于不同事务（XID）的记录可以交错出现。
type Record struct {
	// LSN 是日志记录的递增序号（Log Sequence Number），必须严格递增。
	LSN uint64
	// XID 是记录所属事务的标识。
	XID uint64
	// Kind 是记录类型：Begin / Data / Commit / Abort。
	Kind Kind
	// Payload 是 Data 记录携带的业务数据；其它类型应为 nil。
	Payload []byte
}

// Kind 表示日志记录类型。
type Kind uint8

const (
	KindBegin Kind = iota + 1
	KindData
	KindCommit
	KindAbort
)

func (k Kind) String() string {
	switch k {
	case KindBegin:
		return "BEGIN"
	case KindData:
		return "DATA"
	case KindCommit:
		return "COMMIT"
	case KindAbort:
		return "ABORT"
	default:
		return "UNKNOWN"
	}
}

// MarshalJSON 让日志中的记录类型可读（"BEGIN"/"DATA"/"COMMIT"/"ABORT"）。
func (k Kind) MarshalJSON() ([]byte, error) {
	if k >= KindBegin && k <= KindAbort {
		return []byte(`"` + k.String() + `"`), nil
	}
	return []byte(strconv.Itoa(int(k))), nil
}

// Transaction 是解码器在读到提交时整体发出的一个事务。
type Transaction struct {
	XID       uint64
	CommitLSN uint64
	Records   []Record
}

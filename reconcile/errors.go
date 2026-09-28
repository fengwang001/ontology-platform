package reconcile

import "fmt"

// ErrorCode 以稳定的字符串标识每一类被拒绝的操作，调用方可用 errors.Is
// 或类型断言区分拒绝原因。
type ErrorCode string

const (
	// ErrInvalidParam 扇出/深度/键空间形状等参数非法。
	ErrInvalidParam ErrorCode = "invalid parameter"
	// ErrKeyOutOfRange 键不在 [0, keyspace) 内。
	ErrKeyOutOfRange ErrorCode = "key out of range"
	// ErrTooManyKeys 副本内非空键数量超过创建时声明的上限。
	ErrTooManyKeys ErrorCode = "too many keys"
	// ErrShapeMismatch 两个副本的键空间形状（扇出/深度/键空间）不同。
	ErrShapeMismatch ErrorCode = "shape mismatch"
)

// OpError 携带可区分的错误码与具体说明。被拒绝的操作不会改变任何键值
// 或区间哈希（所有校验均在写入生效之前完成）。
type OpError struct {
	Code ErrorCode
	Op   string
	Msg  string
}

func (e *OpError) Error() string {
	if e.Op == "" {
		return string(e.Code) + ": " + e.Msg
	}
	return string(e.Code) + " [" + e.Op + "]: " + e.Msg
}

func newError(code ErrorCode, op, format string, args ...any) *OpError {
	return &OpError{Code: code, Op: op, Msg: fmt.Sprintf(format, args...)}

}

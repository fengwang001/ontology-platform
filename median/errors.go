package median

import (
	"errors"
	"fmt"
)

// OpError 标注整批提交中触发失败的操作下标，便于区分具体是哪一条。
type OpError struct {
	Index int
	Op    Op
	Err   error
}

func (e *OpError) Error() string {
	return fmt.Sprintf("median: op[%d] kind=%d value=%d: %s", e.Index, e.Op.Kind, e.Op.Value, e.Err.Error())
}

func (e *OpError) Unwrap() error { return e.Err }

func errWithIndex(err error, i int, ops ...Op) error {
	var op Op
	if len(ops) > 0 {
		op = ops[0]
	}
	return &OpError{Index: i, Op: op, Err: err}
}

// asOpError 从错误中提取 *OpError（若有）。
func asOpError(err error) (*OpError, bool) {
	var oe *OpError
	return oe, errors.As(err, &oe)
}

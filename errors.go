package ontology

import "errors"

// ID 是库位、SKU、订单号的统一类型：1 到 32 字节的非空字节串。
type ID string

// 五类拒绝原因，按检查次序：参数 > 不存在 > 状态 > 数量 > 冲突。
var (
	ErrArgument = errors.New("invalid argument")
	ErrNotFound = errors.New("not found")
	ErrState    = errors.New("illegal state")
	ErrQuantity = errors.New("quantity mismatch")
	ErrConflict = errors.New("conflict")
)

// ValidID 校验非空且长度不超过 32 字节。
func ValidID(id ID) bool {
	return len(id) >= 1 && len(id) <= 32
}

func inRange64(q, lo, hi int64) bool { return q >= lo && q <= hi }

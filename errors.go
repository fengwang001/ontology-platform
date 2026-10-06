package ontology

// 业务错误：所有错误彼此可区分（哨兵错误，支持 errors.Is）。
var (
	ErrCodeDuplicate   = sentinel("编码重复")
	ErrParentNotFound  = sentinel("上级不存在")
	ErrInvalidParam    = sentinel("参数非法")
	ErrOverlap         = sentinel("区间重叠")
	ErrClaimExists     = sentinel("理赔已存在")
	ErrNotInsured      = sentinel("出险日未承保")
	ErrInsuredNotFound = sentinel("被保人不存在")
	ErrCodeNotFound    = sentinel("编码不存在")
)

// CodeError 携带可读错误信息。
type CodeError struct {
	msg string
}

func (e *CodeError) Error() string { return e.msg }

func sentinel(msg string) *CodeError {
	return &CodeError{msg: msg}
}

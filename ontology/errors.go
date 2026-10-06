package ontology

// ErrCode 标识可区分的错误类别。数值越小，拒绝次序越靠前，
// 与规格中「参数非法 > 保单不存在 > ... > 发生日未承保」一致。
type ErrCode int

const (
	ErrInvalidParam    ErrCode = iota + 1 // 参数非法
	ErrPolicyNotFound                     // 保单不存在
	ErrMemberNotFound                     // 成员不存在
	ErrItemNotFound                       // 项目不存在
	ErrMemberDuplicate                    // 成员重复
	ErrItemDuplicate                      // 项目重复
	ErrClaimExists                        // 理赔已存在
	ErrClaimNotFound                      // 理赔不存在
	ErrNotLastClaim                       // 非末笔
	ErrCapped                             // 已封顶
	ErrRetroactive                        // 追溯批改
	ErrDayNotCovered                      // 发生日未承保
)

func (c ErrCode) String() string {
	switch c {
	case ErrInvalidParam:
		return "参数非法"
	case ErrPolicyNotFound:
		return "保单不存在"
	case ErrMemberNotFound:
		return "成员不存在"
	case ErrItemNotFound:
		return "项目不存在"
	case ErrMemberDuplicate:
		return "成员重复"
	case ErrItemDuplicate:
		return "项目重复"
	case ErrClaimExists:
		return "理赔已存在"
	case ErrClaimNotFound:
		return "理赔不存在"
	case ErrNotLastClaim:
		return "非末笔"
	case ErrCapped:
		return "已封顶"
	case ErrRetroactive:
		return "追溯批改"
	case ErrDayNotCovered:
		return "发生日未承保"
	}
	return "未知错误"
}

// Error 是引擎返回的唯一错误类型，Code 可用于区分错误类别。
type Error struct {
	Code     ErrCode
	PolicyID string
	Detail   string
}

func (e *Error) Error() string {
	if e.Detail == "" {
		return e.Code.String()
	}
	return e.Code.String() + ": " + e.Detail
}

func errOf(code ErrCode, policyID, detail string) *Error {
	return &Error{Code: code, PolicyID: policyID, Detail: detail}
}

package policy

// ErrCode 区分各类拒绝原因，数值大小即拒绝次序（小者优先报告）。
type ErrCode int

const (
	ErrInvalidParam          ErrCode = iota + 1 // 参数非法
	ErrPolicyNotFound                           // 保单不存在
	ErrClockRegression                          // 时钟回退
	ErrTerminated                               // 已终态
	ErrEndorsementNotFound                      // 批改不存在
	ErrEndorsementDuplicate                     // 批改重复
	ErrAlreadyEffective                         // 已生效
	ErrRetroactive                              // 追溯批改
	ErrPendingTopUp                             // 待补缴
	ErrHasPendingEndorsement                    // 存在未生效批改
	ErrInHesitation                             // 犹豫期内（部分退保专用，次序置于“存在未生效批改”之后）
	ErrAlreadyPaid                              // 已缴
	ErrBelowMinSumInsured                       // 低于最低保额
)

// Error 是引擎返回的唯一错误类型，调用方按 Code 区分原因。
type Error struct {
	Code    ErrCode
	Message string
}

func (e *Error) Error() string { return e.Message }

func newErr(code ErrCode, msg string) *Error {
	return &Error{Code: code, Message: msg}
}

// errOf 把 error 断言为 *Error，供调用方与测试取 Code。
func errOf(err error) (*Error, bool) {
	e, ok := err.(*Error)
	return e, ok
}

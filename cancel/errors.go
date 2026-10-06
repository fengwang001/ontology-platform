package cancel

// ErrKind 以可程序化区分的枚举标识每一类被拒绝的原因。
type ErrKind uint8

const (
	KindInvalidParam   ErrKind = iota + 1 // 参数非法
	KindClockBack                         // 时钟回退
	KindNoOrder                           // 订单不存在
	KindStageOrder                        // 阶段次序错误
	KindTerminal                          // 已送达或已取消的终态
	KindCancelPending                     // 取消待决（争议窗口中再次取消/取货等）
	KindNotCancellable                    // 不可取消（取货后未迟到）
	KindPickedUp                          // 骑手在已取货后请求取消
	KindUnauthorized                      // 无权（发起方与阶段/身份不匹配）
	KindWindowTimeout                     // 声明超时（窗口右端点或之后）
	KindNoDispute                         // 无争议可声明
)

// CancelError 是系统返回的唯一错误类型，按命中顺序的第一个错误产生。
type CancelError struct {
	Kind ErrKind
	msg  string
}

func (e *CancelError) Error() string { return e.msg }

func newErr(kind ErrKind, msg string) *CancelError {
	return &CancelError{Kind: kind, msg: msg}
}

var (
	errInvalidParam   = newErr(KindInvalidParam, "invalid parameter")
	errClockBack      = newErr(KindClockBack, "clock moved backwards")
	errNoOrder        = newErr(KindNoOrder, "order not found")
	errStageOrder     = newErr(KindStageOrder, "stage advance out of order or duplicated")
	errTerminal       = newErr(KindTerminal, "order already in terminal state")
	errCancelPending  = newErr(KindCancelPending, "cancellation pending in dispute window")
	errNotCancellable = newErr(KindNotCancellable, "order cannot be cancelled before rider is late")
	errPickedUp       = newErr(KindPickedUp, "rider cannot cancel after pickup")
	errUnauthorized   = newErr(KindUnauthorized, "actor not permitted for this stage")
	errWindowTimeout  = newErr(KindWindowTimeout, "preparation claim past dispute window end")
	errNoDispute      = newErr(KindNoDispute, "no pending dispute to claim")
)

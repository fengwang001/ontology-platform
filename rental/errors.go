package rental

// Code 标识一次被拒绝操作的首要原因。
// 检查按固定次序进行，只报告第一个错误：
// 参数非法 -> 时钟回退 -> 不存在 -> 状态不允许 -> 日期不可订
// （不可订内部再按 封锁 -> 冲突 -> 间隙 -> 最短入住 -> 孤夜）。
type Code int

const (
	CodeInvalidParams Code = iota // 参数非法
	CodeClockRollback             // 时钟回退
	CodeNotFound                  // 房源或预订不存在
	CodeInvalidState              // 状态不允许该操作
	CodeBlocked                   // 与封锁区间相交
	CodeConflict                  // 与有效保留/已确认预订相交
	CodeGap                       // 换客间隙不足
	CodeMinStay                   // 最短入住夜数不足
	CodeOrphan                    // 产生孤夜
)

// Error 是服务返回的唯一错误类型，Code 为首要原因，Msg 为人读说明。
type Error struct {
	Code Code
	Msg  string
}

func (e *Error) Error() string { return e.Msg }

func newErr(code Code, msg string) *Error { return &Error{Code: code, Msg: msg} }

// errCode 便于测试与对照模型比较，nil 视为 -1。
func errCode(err error) int {
	if err == nil {
		return -1
	}
	if e, ok := err.(*Error); ok {
		return int(e.Code)
	}
	return -2
}

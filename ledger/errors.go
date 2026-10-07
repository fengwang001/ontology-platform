package ledger

import "fmt"

// ErrKind 对被拒绝的操作进行分类。常量的声明顺序即为报告优先级：
// 同一操作违反多项检查时，只报告优先级最高（数值最小）的那一类。
type ErrKind int

const (
	ErrInvalidParam  ErrKind = iota // 参数非法
	ErrClockRollback                // 时钟回退
	ErrReviewer                     // 复核人人数不足、为同一人或为申请人本人
	ErrAuth                         // 复核人授权无效
	ErrNotFound                     // 对象不存在
	ErrState                        // 状态不符
	ErrDeptLocked                   // 科室被锁定
	ErrSlipLimit                    // 超过未结清单据上限
	ErrStock                        // 库存不足
	ErrQuantity                     // 数量超出
)

func (k ErrKind) String() string {
	switch k {
	case ErrInvalidParam:
		return "参数非法"
	case ErrClockRollback:
		return "时钟回退"
	case ErrReviewer:
		return "复核人人数不足或为同一人或为申请人本人"
	case ErrAuth:
		return "复核人授权无效"
	case ErrNotFound:
		return "对象不存在"
	case ErrState:
		return "状态不符"
	case ErrDeptLocked:
		return "科室被锁定"
	case ErrSlipLimit:
		return "超过未结清单据上限"
	case ErrStock:
		return "库存不足"
	case ErrQuantity:
		return "数量超出"
	}
	return "未知错误"
}

// OpError 是一次被拒绝操作的完整判定依据。
type OpError struct {
	Kind ErrKind
	Msg  string
}

func (e *OpError) Error() string {
	return fmt.Sprintf("[%s] %s", e.Kind, e.Msg)
}

func errf(kind ErrKind, format string, args ...any) *OpError {
	return &OpError{Kind: kind, Msg: fmt.Sprintf(format, args...)}
}

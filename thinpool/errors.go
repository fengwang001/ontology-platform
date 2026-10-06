package thinpool

import "fmt"

// Kind 对可区分的错误原因分类。错误只报校验次序最靠前的一类。
type Kind int

const (
	KindInvalidArgument      Kind = iota + 1 // 参数非法
	KindVolumeNotFound                       // 卷不存在
	KindVolumeExists                         // 卷已存在
	KindOvercommit                           // 超分配
	KindReservationPool                      // 保留超池
	KindReservationVolume                    // 保留超卷
	KindDataInRange                          // 卷内仍有数据
	KindReservationShortfall                 // 保留不足
	KindPoolExhausted                        // 池耗尽
)

func (k Kind) String() string {
	switch k {
	case KindInvalidArgument:
		return "invalid_argument"
	case KindVolumeNotFound:
		return "volume_not_found"
	case KindVolumeExists:
		return "volume_exists"
	case KindOvercommit:
		return "overcommit"
	case KindReservationPool:
		return "reservation_exceeds_pool"
	case KindReservationVolume:
		return "reservation_exceeds_volume"
	case KindDataInRange:
		return "data_in_range"
	case KindReservationShortfall:
		return "reservation_shortfall"
	case KindPoolExhausted:
		return "pool_exhausted"
	default:
		return "unknown_error"
	}
}

// Error 携带可程序化判别的错误类别。
type Error struct {
	Kind Kind
	Msg  string
}

func (e *Error) Error() string {
	return e.Kind.String() + ": " + e.Msg
}

func errKind(k Kind, format string, args ...any) error {
	return &Error{Kind: k, Msg: fmt.Sprintf(format, args...)}
}

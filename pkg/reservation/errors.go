package reservation

type ErrorKind int

const (
	ErrInvalidParam ErrorKind = iota
	ErrClockRollback
	ErrNotFound
	ErrStateNotAllowed
	ErrHoldExpired
	ErrCancelled
	ErrCapacity
)

var kindNames = [...]string{
	ErrInvalidParam:    "参数非法",
	ErrClockRollback:   "时钟回退",
	ErrNotFound:        "预约不存在",
	ErrStateNotAllowed: "状态不允许",
	ErrHoldExpired:     "占位已到期",
	ErrCancelled:       "已取消",
	ErrCapacity:        "容量不足",
}

func (k ErrorKind) String() string {
	if int(k) < 0 || int(k) >= len(kindNames) {
		return "未知错误"
	}
	return kindNames[k]
}

type Level int

const (
	LevelFeeder Level = iota
	LevelTransformer
)

type CapacityError struct {
	Time  int
	Level Level
}

func (l Level) String() string {
	if l == LevelFeeder {
		return "馈线"
	}
	return "变压器"
}

func (e *CapacityError) Error() string {
	return "容量不足: 时刻 " + itoa(e.Time) + " " + e.Level.String()
}

type OpError struct {
	Kind    ErrorKind
	Detail  string
	CapInfo *CapacityError
}

func (e *OpError) Error() string {
	if e.CapInfo != nil {
		return e.CapInfo.Error()
	}
	if e.Detail != "" {
		return e.Kind.String() + ": " + e.Detail
	}
	return e.Kind.String()
}

func newError(kind ErrorKind, detail string) *OpError {
	return &OpError{Kind: kind, Detail: detail}
}

func newCapacityError(tm int, level Level) *OpError {
	return &OpError{Kind: ErrCapacity, CapInfo: &CapacityError{Time: tm, Level: level}}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	var b [24]byte
	i := len(b)
	for n != 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}

package ontology

// Kind 区分可拒绝操作的错误类别。
// 优先级（高 -> 低，同时成立时只报优先级最高者）：
// 参数非法 > 时钟回退 > 对象不存在 > 状态不符 > 住宿冲突。
type Kind int

const (
	KindInvalidParam  Kind = iota + 1 // 参数非法
	KindClockRollback                 // 时钟回退
	KindNotFound                      // 对象不存在
	KindStateConflict                 // 状态不符
	KindStayConflict                  // 住宿冲突
)

func (k Kind) String() string {
	switch k {
	case KindInvalidParam:
		return "参数非法"
	case KindClockRollback:
		return "时钟回退"
	case KindNotFound:
		return "对象不存在"
	case KindStateConflict:
		return "状态不符"
	case KindStayConflict:
		return "住宿冲突"
	}
	return "未知错误"
}

// Error 是系统拒绝操作时返回的错误，携带类别以便调用方区分。
type Error struct {
	Kind Kind
	Op   string
	Msg  string
}

func (e *Error) Error() string {
	return e.Op + ": " + e.Kind.String() + ": " + e.Msg
}

func newErr(op string, kind Kind, msg string) *Error {
	return &Error{Kind: kind, Op: op, Msg: msg}
}

// KindOf 从 error 中提取 Kind；非本系统错误返回 0。
func KindOf(err error) Kind {
	if e, ok := err.(*Error); ok {
		return e.Kind
	}
	return 0
}

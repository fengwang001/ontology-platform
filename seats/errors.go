package seats

// RejKind 分类一次操作被拒绝的原因。枚举值的数值顺序即题目规定的拒绝
// 次序：参数非法 > 时钟回退 > 条目或航段不存在 > 预占已过期 > 状态不符 >
// 库存不足。一次操作同时满足多个拒绝条件时，只报次序最靠前的一类。
type RejKind int

const (
	KindInvalidParam  RejKind = iota // 参数非法
	KindClockRollback                // 时钟回退
	KindNotFound                     // 条目或航段不存在
	KindHoldExpired                  // 预占已过期
	KindStateConflict                // 状态不符（已取消 / 已出票）
	KindInsufficient                 // 库存不足
)

func (k RejKind) String() string {
	switch k {
	case KindInvalidParam:
		return "invalid parameter"
	case KindClockRollback:
		return "clock rollback"
	case KindNotFound:
		return "not found"
	case KindHoldExpired:
		return "hold expired"
	case KindStateConflict:
		return "state conflict"
	case KindInsufficient:
		return "insufficient inventory"
	}
	return "unknown"
}

// Error 描述一次被拒绝的操作。被拒绝的操作不改变任何占用、授权量、时钟
// 或条目状态。
type Error struct {
	Kind    RejKind
	Msg     string
	Segment string // 相关航段，KindInsufficient / KindNotFound 时设置
	Status  Status // 条目当前状态，KindStateConflict 时设置，可区分已取消与已出票
}

func (e *Error) Error() string {
	if e.Msg != "" {
		return e.Kind.String() + ": " + e.Msg
	}
	return e.Kind.String()
}

func errInvalid(msg string) *Error { return &Error{Kind: KindInvalidParam, Msg: msg} }

func errNotFound(what string) *Error {
	return &Error{Kind: KindNotFound, Msg: what + " does not exist"}
}

func errConflict(st Status, id string) *Error {
	return &Error{Kind: KindStateConflict, Status: st, Msg: "entry " + id + " is " + st.String()}
}

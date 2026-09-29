package ontology

// RejectReason 描述一次输入被整体拒绝的可区分原因。
type RejectReason int

const (
	// ReasonNone 未发生拒绝。
	ReasonNone RejectReason = iota
	// ReasonRateOutOfRange 采样率不在 [0, MaxRate] 闭区间内。
	ReasonRateOutOfRange
	// ReasonEmptyKey 事件或判定请求携带了空键。
	ReasonEmptyKey
	// ReasonTooManyKnownKeys 接受本次输入后已知键数量会超过容量上限。
	ReasonTooManyKnownKeys
)

func (r RejectReason) String() string {
	switch r {
	case ReasonRateOutOfRange:
		return "rate out of range"
	case ReasonEmptyKey:
		return "empty key"
	case ReasonTooManyKnownKeys:
		return "too many known keys"
	default:
		return "none"
	}
}

// RejectError 携带可程序化区分的拒绝原因。
type RejectError struct {
	Reason RejectReason
}

func (e *RejectError) Error() string { return e.Reason.String() }

// AsReject 从任意错误中提取 *RejectError，未命中返回 nil。
func AsReject(err error) *RejectError {
	for err != nil {
		if re, ok := err.(*RejectError); ok {
			return re
		}
		type unwrapper interface{ Unwrap() error }
		u, ok := err.(unwrapper)
		if !ok {
			return nil
		}
		err = u.Unwrap()
	}
	return nil
}

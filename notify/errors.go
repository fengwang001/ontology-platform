package notify

// Reason 表示一次被拒绝操作的可区分原因。
type Reason int

const (
	ReasonOK Reason = iota
	ReasonInvalidParam
	ReasonInvalidLanguage
	ReasonSyntax
	ReasonNoTemplate
	ReasonMissingVars
	ReasonTooLong
)

// Error 携带拒绝原因及定位信息。
type Error struct {
	Reason     Reason
	Offset     int
	Missing    []string
	Codepoints int
}

func (e *Error) Error() string { return reasonString(e.Reason) }

func reasonString(r Reason) string {
	switch r {
	case ReasonInvalidParam:
		return "invalid parameter"
	case ReasonInvalidLanguage:
		return "invalid language"
	case ReasonSyntax:
		return "syntax error"
	case ReasonNoTemplate:
		return "no template"
	case ReasonMissingVars:
		return "missing variables"
	case ReasonTooLong:
		return "rendered text too long"
	default:
		return "ok"
	}
}

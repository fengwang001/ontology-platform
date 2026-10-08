package constexpr

import (
	"strconv"
	"strings"
)

// String 返回表达式的可读 S 表达式形式，用于日志与调试。
func (e *Expr) String() string {
	if e == nil {
		return "<nil>"
	}
	switch e.Op {
	case OpLit:
		switch e.LitKind {
		case KindInt:
			return e.IntVal.String()
		case KindRat:
			return e.RatVal.RatString()
		case KindBool:
			if e.BoolVal {
				return "true"
			}
			return "false"
		case KindString:
			return strconv.Quote(e.StrVal)
		}
		return "<lit?>"
	case OpRef:
		return "@" + e.Name
	case OpConv:
		return "(" + e.TypeName + " " + e.Operands[0].String() + ")"
	case OpDefaultConv:
		return "(default " + e.Operands[0].String() + ")"
	}
	if len(e.Operands) == 1 {
		return "(" + opName(e.Op) + " " + e.Operands[0].String() + ")"
	}
	parts := make([]string, len(e.Operands))
	for i, s := range e.Operands {
		parts[i] = s.String()
	}
	return "(" + opName(e.Op) + " " + strings.Join(parts, " ") + ")"
}

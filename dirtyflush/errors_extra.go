package dirtyflush

import "fmt"

func errf(format string, args ...any) error {
	return &OpError{Reason: ReasonInvalidArg, Op: "invariant", Msg: fmt.Sprintf(format, args...)}
}

package alarm

import "fmt"

type Error struct {
	Kind   ErrorKind
	Op     string
	Detail string
}

func (e *Error) Error() string {
	return fmt.Sprintf("%s: %s", e.Op, e.Detail)

}

func newError(kind ErrorKind, op, detail string) *Error {
	return &Error{Kind: kind, Op: op, Detail: detail}
}

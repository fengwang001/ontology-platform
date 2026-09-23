package lexer

import "fmt"

// Error classes, mutually distinguishable via errors.Is or Error.Code.
var (
	ErrQuoteSentinel        = errf{CodeQuote, "double quote in unquoted field"}
	ErrTrailingSentinel     = errf{CodeTrailing, "character after closed quoted field"}
	ErrUnterminatedSentinel = errf{CodeUnterminated, "unterminated quoted field at end of input"}
	ErrBareSentinel         = errf{CodeBareCR, "bare carriage return not followed by newline"}
)

const (
	CodeQuote = iota + 1
	CodeTrailing
	CodeUnterminated
	CodeBareCR
)

// Error is a located syntax error. Pos is the byte offset; Rec and Field are
// 1-based record/field numbers (0 when not yet assignable by the lexer).
type Error struct {
	Code  int
	Msg   string
	Pos   int
	Rec   int
	Field int
}

func (e Error) Error() string {
	return fmt.Sprintf("%s at byte %d, record %d, field %d", e.Msg, e.Pos, e.Rec, e.Field)
}

type errf struct {
	code int
	msg  string
}

func (f errf) Error() string { return f.msg }

func mkErr(code, pos int, msg string) error { return Error{Code: code, Msg: msg, Pos: pos} }

func ErrQuote(pos int) error        { return mkErr(CodeQuote, pos, ErrQuoteSentinel.msg) }
func ErrTrailing(pos int) error     { return mkErr(CodeTrailing, pos, ErrTrailingSentinel.msg) }
func ErrBare(pos int) error         { return mkErr(CodeBareCR, pos, ErrBareSentinel.msg) }
func ErrUnterminated(pos int) error { return mkErr(CodeUnterminated, pos, ErrUnterminatedSentinel.msg) }

// Is supports sentinel comparison for the four syntax classes.
func (e Error) Is(target error) bool {
	t, ok := target.(errf)
	return ok && t.code == e.Code
}

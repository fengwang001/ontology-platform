package intelhex

import "strconv"

// 哨兵错误，可用 errors.Is 精确区分拒绝原因。
var (
	ErrSyntax        = SyntaxError("intelhex: syntax error")
	ErrChecksum      = SyntaxError("intelhex: checksum mismatch")
	ErrUnknownType   = SyntaxError("intelhex: unknown record type")
	ErrInvalidRecord = SyntaxError("intelhex: invalid record for its type")
	ErrOutOfRange    = SyntaxError("intelhex: data crosses 64KiB offset boundary")
	ErrOverlap       = SyntaxError("intelhex: overlapping data")
	ErrAfterEOF      = SyntaxError("intelhex: line added after end-of-file record")
	ErrNotFinished   = SyntaxError("intelhex: Finish called before end-of-file record")
)

// SyntaxError 是一个稳定的、可比较的拒绝原因。
type SyntaxError string

func (e SyntaxError) Error() string { return string(e) }

// LineError 携带被拒绝记录的行序号（从 1 起，被拒绝的行也占序号）。
// 使用 errors.Is(err, ErrChecksum) 等可判定具体原因。
type LineError struct {
	Line int
	Err  error
}

func (e *LineError) Error() string {
	return "line " + strconv.Itoa(e.Line) + ": " + e.Err.Error()
}

func (e *LineError) Unwrap() error { return e.Err }

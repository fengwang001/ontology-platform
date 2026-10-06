package retention

type ID string

type Commit struct {
	ID       ID
	Parents  []ID
	Created  int64
	Contents []ID
	Written  int64
}

type Content struct {
	ID      ID
	Size    int64
	Written int64
}

type Policy struct {
	ReachableRetention   int64
	UnreachableRetention int64
	FreshGrace           int64
}

type Record struct {
	Old      ID
	New      ID
	At       int64
	Operator string
}

type GCResult struct {
	CommitsDeleted  int
	ContentsDeleted int
	BytesDeleted    int64
}

const (
	CodeInvalidArgument = iota + 1
	CodeClockMovedBack
	CodeRefNotFound
	CodeCommitNotFound
	CodeRecordNotFound
)

type Error struct {
	Code int
	msg  string
}

func (e Error) Error() string { return e.msg }

func newError(code int, msg string) error { return Error{Code: code, msg: msg} }

var (
	ErrInvalidArgument = newError(CodeInvalidArgument, "invalid argument")
	ErrClockMovedBack  = newError(CodeClockMovedBack, "clock moved back")
	ErrRefNotFound     = newError(CodeRefNotFound, "reference not found")
	ErrCommitNotFound  = newError(CodeCommitNotFound, "commit not found")
	ErrRecordNotFound  = newError(CodeRecordNotFound, "record not found")
)

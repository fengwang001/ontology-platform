package layout

type ErrorKind int

const (
	KindInvalidArgument ErrorKind = iota + 1
	KindNodeNotFound
	KindConflict
	KindReentrantCommit
)

type LayoutError struct {
	Kind ErrorKind
	Msg  string
}

func (e *LayoutError) Error() string { return e.Msg }

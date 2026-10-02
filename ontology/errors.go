package ontology

// ErrorKind 区分四类拒绝原因。
type ErrorKind int

const (
	KindInvalidConfig ErrorKind = iota
	KindNotFound
	KindConflict
	KindLimitExceeded
)

// OpError 携带拒绝类别与定位信息。
type OpError struct {
	Kind ErrorKind
	Op   string
	Msg  string
}

func (e *OpError) Error() string {
	return e.Op + ": " + e.Msg
}

func errConfig(op, msg string) *OpError {
	return &OpError{Kind: KindInvalidConfig, Op: op, Msg: msg}
}

func errNotFound(op string, id []byte) *OpError {
	return &OpError{Kind: KindNotFound, Op: op, Msg: "certificate not found: " + string(id)}
}

func errConflict(op string, id []byte) *OpError {
	return &OpError{Kind: KindConflict, Op: op, Msg: "certificate id already exists: " + string(id)}
}

func errLimit(op string) *OpError {
	return &OpError{Kind: KindLimitExceeded, Op: op, Msg: "registry size would exceed Nmax"}
}

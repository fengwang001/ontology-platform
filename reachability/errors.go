package reachability

import "fmt"

// ErrorKind 以稳定字符串标识操作被拒绝的原因，便于调用方区分与断言。
type ErrorKind string

const (
	// KindEmptyNodeName：节点名为空字符串。
	KindEmptyNodeName ErrorKind = "empty_node_name"
	// KindInvalidArgument：参数非法（例如增删的边数为 0）。
	KindInvalidArgument ErrorKind = "invalid_argument"
	// KindEdgeNotFound：删除一条当前不存在（重数为 0）的边。
	KindEdgeNotFound ErrorKind = "edge_not_found"
	// KindMultiplicityOverflow：增边会使重数超过 MaxEdgeMultiplicity。
	KindMultiplicityOverflow ErrorKind = "multiplicity_overflow"
)

// 可区分的哨兵错误，调用方可用 errors.Is 判断；具体上下文见 OpError。
var (
	ErrEmptyNodeName        = &OpError{Kind: KindEmptyNodeName, Msg: "node name must not be empty"}
	ErrInvalidArgument      = &OpError{Kind: KindInvalidArgument, Msg: "invalid argument"}
	ErrEdgeNotFound         = &OpError{Kind: KindEdgeNotFound, Msg: "edge does not exist"}
	ErrMultiplicityOverflow = &OpError{Kind: KindMultiplicityOverflow, Msg: "edge multiplicity overflow"}
)

// OpError 描述一次被拒绝的图操作及其原因。
//
// 被拒绝的操作不会改变边重数、可达集合与任何索引。
type OpError struct {
	// Kind 是机器可判别的错误类别。
	Kind ErrorKind
	// Op 是被拒绝的操作名称，如 "add_edge"。
	Op string
	// From、To 是操作涉及的边（可能为空）。
	From string
	To   string
	// Msg 提供人类可读的细节。
	Msg string
}

func (e *OpError) Error() string {
	op := e.Op
	if op == "" {
		op = "operation"
	}
	if e.From != "" || e.To != "" {
		return fmt.Sprintf("%s rejected for edge %q -> %q: %s: %s",
			op, e.From, e.To, e.Kind, e.Msg)
	}
	return fmt.Sprintf("%s rejected: %s: %s", op, e.Kind, e.Msg)
}

// Is 使 errors.Is(err, ErrXxx) 按 Kind 匹配，
// 这样既能断言具体错误类别，又能保留每次操作的上下文。
func (e *OpError) Is(target error) bool {
	t, ok := target.(*OpError)
	if !ok {
		return false
	}
	return e.Kind == t.Kind
}

func newOpError(kind ErrorKind, op, from, to, msg string) *OpError {
	return &OpError{Kind: kind, Op: op, From: from, To: to, Msg: msg}
}

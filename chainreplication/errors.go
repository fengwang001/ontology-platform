package chainreplication

// 可区分的拒绝原因。被拒绝的操作不会改变任何协调器状态。
var (
	ErrEmptyChain      = rejectionError("chainreplication: chain must contain at least one node")
	ErrEmptyNodeID     = rejectionError("chainreplication: node id must not be empty")
	ErrDuplicateNodeID = rejectionError("chainreplication: duplicate node id in chain")
	ErrUnknownNode     = rejectionError("chainreplication: unknown node id")
	ErrNodeFailed      = rejectionError("chainreplication: node has already failed")
	ErrLastNodeAlive   = rejectionError("chainreplication: refusing to fail the last alive node")
	ErrWriteNotAtHead  = rejectionError("chainreplication: writes are only accepted at the chain head")
	ErrReadNotAtTail   = rejectionError("chainreplication: reads are only served at the chain tail")
)

// Rejection marks an error as a rejected operation.
type Rejection interface {
	error
	Rejected() bool
}

type rejectionError string

func (e rejectionError) Error() string  { return string(e) }
func (e rejectionError) Rejected() bool { return true }

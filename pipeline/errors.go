package pipeline

// 建图阶段的可区分错误。
var (
	ErrUnknownTaskEndpoint = plannerError("edge references unknown task")
	ErrDuplicateEdge       = plannerError("duplicate edge for same ordered task pair")
	ErrIntraRegionBlock    = plannerError("blocking edge endpoints within same region")
	ErrRegionCycle         = plannerError("region graph contains a cycle")
)

// 上报阶段的可区分错误。
var (
	ErrTaskNotFound      = plannerError("task does not exist")
	ErrTaskNotRunning    = plannerError("task is not running")
	ErrEdgeNotFound      = plannerError("edge does not exist")
	ErrNonBlockingEdge   = plannerError("edge is not a blocking edge")
	ErrResultNotProduced = plannerError("blocking result has not been produced yet")
)

type plannerError string

func (e plannerError) Error() string { return string(e) }

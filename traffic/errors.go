package traffic

import "errors"

// 错误按校验次序定义，调用方可用 errors.Is 区分。
var (
	ErrInvalidArgument      = errors.New("traffic: invalid argument")
	ErrClockRewind          = errors.New("traffic: clock rewind")
	ErrLinkNotFound         = errors.New("traffic: link not found")
	ErrIncidentNotFound     = errors.New("traffic: incident not found")
	ErrIncidentAlreadyEnded = errors.New("traffic: incident already ended")
	ErrReductionOutOfRange  = errors.New("traffic: reduction out of range")
	ErrFlowExceedsCapacity  = errors.New("traffic: arrival flow exceeds capacity")
)

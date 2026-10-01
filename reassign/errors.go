package reassign

import "errors"

var (
	ErrPartitionNotFound  = errors.New("reassign: partition not found")
	ErrReassignInProgress = errors.New("reassign: reassignment already in progress")
	ErrInvalidTarget      = errors.New("reassign: invalid target list (empty, duplicate, or unknown node)")
	ErrTargetNodeDown     = errors.New("reassign: target contains a dead node")
	ErrTargetUnchanged    = errors.New("reassign: target identical to current replica list")
	ErrConcurrencyLimit   = errors.New("reassign: global concurrency limit reached")
	ErrNotInReplicas      = errors.New("reassign: node not in replica list")
	ErrNodeDown           = errors.New("reassign: node is down")
	ErrAlreadyInISR       = errors.New("reassign: node already in ISR")
	ErrNoReassign         = errors.New("reassign: no reassignment in progress")
)

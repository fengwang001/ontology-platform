package netcode

type RejectionReason string

const (
	RejectionInvalidArgument RejectionReason = "invalid_argument"
	RejectionClockRewound    RejectionReason = "clock_rewound"
	RejectionDuplicate       RejectionReason = "duplicate"
	RejectionGap             RejectionReason = "gap"
	RejectionBacklogFull     RejectionReason = "backlog_full"
	RejectionStepTooLarge    RejectionReason = "step_too_large"
)

type Config struct {
	WorldWidth int64
	Quota      int
	MaxStep    int64
	Backlog    int
}

type Move struct {
	Sequence int64
	Delta    int64
}

type ReceiveResult struct {
	Accepted bool
	Reason   RejectionReason
	Existing *ReceiveResult
}

type Confirmation struct {
	ProcessedSequence int64
	Position          int64
	RejectedSequences map[int64]struct{}
}

type TickResult struct {
	Now           int64
	Accepted      bool
	Reason        RejectionReason
	Confirmations map[string]Confirmation
	Players       []string
}

type ReconcileResult struct {
	Accepted bool
	Position int64
}

package quicloss

import "errors"

// Distinguishable error causes. All validation failures wrap one of these
// sentinel errors, so callers can use errors.Is.
var (
	ErrInvalidSpace    = errors.New("invalid packet number space")
	ErrInvalidSize     = errors.New("invalid packet size")
	ErrInvalidTime     = errors.New("timestamp out of range")
	ErrInvalidPN       = errors.New("negative packet number")
	ErrEmptyAck        = errors.New("empty acknowledged packet set")
	ErrInvalidAckDelay = errors.New("invalid ack delay")
	ErrClockBackward   = errors.New("clock moved backwards")
	ErrSpaceDiscarded  = errors.New("packet number space discarded")
	ErrPNNotIncreasing = errors.New("packet number not strictly increasing")
	ErrAckedNeverSent  = errors.New("acknowledged packet number was never sent")
	ErrTimeoutEarly    = errors.New("timeout fired before scheduled time")
)

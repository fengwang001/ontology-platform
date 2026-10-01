package liveness

import "errors"

var (
	ErrNegativeBlockID  = errors.New("liveness: block id must be non-negative")
	ErrDuplicateBlockID = errors.New("liveness: block id already exists")
	ErrAlreadySealed    = errors.New("liveness: analyzer already sealed")

	ErrSealedTwice = errors.New("liveness: analyzer already sealed")
	ErrNoBlocks    = errors.New("liveness: no blocks have been added")
	ErrMissingSucc = errors.New("liveness: successor block does not exist")

	ErrNotSealed   = errors.New("liveness: analyzer is not sealed yet")
	ErrNoSuchBlock = errors.New("liveness: block does not exist")
)

// MissingSuccessorError reports the first missing successor found while sealing.
type MissingSuccessorError struct {
	BlockID   int
	Successor int
}

func (e *MissingSuccessorError) Error() string {
	return "liveness: successor " + itoa(e.Successor) + " of block " + itoa(e.BlockID) + " does not exist"
}

func (e *MissingSuccessorError) Unwrap() error { return ErrMissingSucc }

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

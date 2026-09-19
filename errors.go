package ontology

import (
	"errors"
	"time"
)

var (
	// ErrInvalidConfig reports that limiter configuration is unusable.
	ErrInvalidConfig = errors.New("ontology: invalid limiter configuration")

	// ErrNegativeTokens reports that a caller asked for a negative token count.
	ErrNegativeTokens = errors.New("ontology: requested token count must not be negative")

	// ErrRequestExceedsCapacity reports that a request can never fit in a bucket.
	ErrRequestExceedsCapacity = errors.New("ontology: requested token count exceeds bucket capacity")

	// ErrTimeRewound reports that an injected timestamp is earlier than the
	// previous observation for the same tenant. The call changes no state.
	ErrTimeRewound = errors.New("ontology: injected time moved backwards")
)

// InsufficientTokensError reports that a request could not be served yet.
// RetryAfter is the time from the rejected request's now argument until the
// bucket is expected to contain at least the requested number of tokens.
type InsufficientTokensError struct {
	Available  int
	Requested  int
	RetryAfter time.Duration
}

func (err *InsufficientTokensError) Error() string {
	return "ontology: insufficient tokens"
}

// RetryAfter returns the supplied error's suggested wait duration when it is
// an InsufficientTokensError. Other rejections return 0 and false.
func RetryAfter(err error) (time.Duration, bool) {
	var insufficient *InsufficientTokensError
	if errors.As(err, &insufficient) {
		return insufficient.RetryAfter, true
	}
	return 0, false
}

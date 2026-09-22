package policy

// Retryable decides whether an execution error qualifies for another attempt.
type Retryable func(err error) bool

// Policy configures execution retries and compensation retries.
//
// Semantics (left-closed, right-open): MaxRetries=N permits N retries after
// the initial attempt, i.e. at most N+1 total executions. The (N+1)-th
// failure is the terminal failure.
//
// BackoffMillis[attempt-1] is the wait before attempt number attempt
// (2..N+1); the last element is reused when the slice is too short and a
// missing/zero entry means no waiting.
type Policy struct {
	MaxRetries        int       // extra attempts after the first one
	BackoffMillis     []int64   // waits before retries
	IsRetryable       Retryable // nil: no error is retryable
	CompMaxRetries    int       // retries for a failing compensation
	CompBackoffMillis []int64
	CompIsRetryable   Retryable // nil: compensation errors are not retried
}

// Default returns a no-retry policy.
func Default() Policy { return Policy{} }

// Backoff returns the configured wait before the given attempt (1-based).
func (p Policy) Backoff(attempt int) int64 {
	return pickBackoff(p.BackoffMillis, attempt)
}

// CompBackoff returns the configured wait before compensation attempt n.
func (p Policy) CompBackoff(attempt int) int64 {
	return pickBackoff(p.CompBackoffMillis, attempt)
}

func pickBackoff(b []int64, attempt int) int64 {
	idx := attempt - 2
	if idx < 0 || len(b) == 0 {
		return 0
	}
	if idx >= len(b) {
		idx = len(b) - 1
	}
	v := b[idx]
	if v < 0 {
		return 0
	}
	return v
}

// RetryAllowed reports whether attempt totalSoFar+1 may proceed given the
// error from attempt totalSoFar.
func (p Policy) RetryAllowed(totalSoFar int, err error) bool {
	if totalSoFar >= p.MaxRetries || err == nil {
		return false
	}
	if p.IsRetryable == nil {
		return false
	}
	return p.IsRetryable(err)
}

// CompRetryAllowed mirrors RetryAllowed for compensation attempts.
func (p Policy) CompRetryAllowed(totalSoFar int, err error) bool {
	if totalSoFar >= p.CompMaxRetries || err == nil {
		return false
	}
	if p.CompIsRetryable == nil {
		return false
	}
	return p.CompIsRetryable(err)
}

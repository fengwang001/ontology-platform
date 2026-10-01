package quota

import "errors"

var (
	ErrInvalid       = errors.New("quota: invalid argument")
	ErrNotFound      = errors.New("quota: node not found")
	ErrWrongType     = errors.New("quota: node type mismatch")
	ErrRoot          = errors.New("quota: root node")
	ErrNotEmpty      = errors.New("quota: directory not empty")
	ErrBadStructure  = errors.New("quota: invalid move structure")
	ErrInsuffReserve = errors.New("quota: reserve insufficient")
	ErrBytesQuota    = errors.New("quota: bytes quota exceeded")
	ErrEntriesQuota  = errors.New("quota: entries quota exceeded")
	ErrBelowUsage    = errors.New("quota: quota below current usage")
	ErrBatch         = errors.New("quota: batch aborted")
)

// QuotaError carries the directory id where a quota limit was violated.
type QuotaError struct {
	Op  string
	Dir ID
	Err error
}

func (e *QuotaError) Error() string {
	return e.Err.Error() + " at directory " + itoa(int64(e.Dir))
}

func (e *QuotaError) Unwrap() error { return e.Err }

// EmptyBatchError is returned by Batch with no ops; it is both a batch
// failure and an invalid-argument error.
type EmptyBatchError struct{}

func (e *EmptyBatchError) Error() string   { return "quota: empty batch" }
func (e *EmptyBatchError) Unwrap() []error { return []error{ErrBatch, ErrInvalid} }

func itoa(n int64) string {
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
		buf[i] = byte('0' + byte(n%10))
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

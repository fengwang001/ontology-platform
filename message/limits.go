package message

import "fmt"

// Limits configures the resource ceilings enforced while parsing.
// A non-positive value for MaxBytes, MaxFieldPayload or MaxUnknown
// disables that specific check. MaxDepth is always enforced: a
// non-positive value selects a safe built-in default, so deeply
// nested input is rejected deterministically instead of overflowing
// the stack.
type Limits struct {
	// MaxBytes is the maximum total size of the top-level message.
	MaxBytes int
	// MaxFieldPayload is the maximum payload size of a single
	// bytes or message field.
	MaxFieldPayload int
	// MaxUnknown is the maximum number of unknown fields per
	// message level (each nested message gets its own allowance).
	MaxUnknown int
	// MaxDepth is the maximum nesting depth; the top-level message
	// is depth 1.
	MaxDepth int
}

// defaultMaxDepth bounds recursion when Limits.MaxDepth is unset.
const defaultMaxDepth = 128

// DefaultLimits returns conservative limits suitable for most uses.
func DefaultLimits() Limits {
	return Limits{
		MaxBytes:        1 << 20,
		MaxFieldPayload: 1 << 20,
		MaxUnknown:      1024,
		MaxDepth:        32,
	}
}

// LimitError reports that parsing was rejected because a configured
// limit was exceeded. The check happens before the offending data is
// buffered, and no partial state is left behind.
type LimitError struct {
	Resource string // "message-bytes", "field-payload", "unknown-fields" or "depth"
	Limit    int
	Offset   int // byte offset at which the limit was hit
}

func (e *LimitError) Error() string {
	return fmt.Sprintf("message: %s limit %d exceeded at byte offset %d",
		e.Resource, e.Limit, e.Offset)
}

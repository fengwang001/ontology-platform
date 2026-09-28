package vv

import "fmt"

// ErrorKind 标识同步或写入被拒绝的具体原因类别。
type ErrorKind string

const (
	KindUnregisteredReplica ErrorKind = "unregistered replica"
	KindInvalidVector       ErrorKind = "invalid version vector"
	KindNonContiguousChange ErrorKind = "non-contiguous change sequence"
	KindLogLimitExceeded    ErrorKind = "log limit exceeded"
)

// SyncError 携带可区分的拒绝原因。每一类原因互不相同。
type SyncError struct {
	Kind   ErrorKind
	Detail string
}

func (e *SyncError) Error() string {
	return string(e.Kind) + ": " + e.Detail
}

func newError(kind ErrorKind, format string, args ...any) *SyncError {
	return &SyncError{Kind: kind, Detail: fmt.Sprintf(format, args...)}
}

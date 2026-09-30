package node

import "errors"

// 构造与入参非法类错误。
var (
	ErrInvalidAllocatable = errors.New("node: allocatable memory must be positive")
	ErrInvalidOversell    = errors.New("node: oversell factor must be >= 1")
	ErrNegativeRequest    = errors.New("node: request must not be negative")
	ErrInvalidLimit       = errors.New("node: limit must be positive and >= request")
	ErrDuplicateID        = errors.New("node: container id already registered")
	ErrContainerNotFound  = errors.New("node: container not found")
	ErrInvalidUsage       = errors.New("node: usage must be within [0, limit]")
)

// 准入被拒类错误（独立于入参非法）。
var (
	ErrRequestExhausted = errors.New("node: admission denied: total requests would exceed allocatable")
	ErrLimitOversold    = errors.New("node: admission denied: total limits would exceed oversell cap")
)

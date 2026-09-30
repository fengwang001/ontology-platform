package allocator

import "errors"

var (
	ErrEmptyTenant    = errors.New("allocator: tenant is empty")
	ErrTenantExists   = errors.New("allocator: tenant already registered")
	ErrTenantNotFound = errors.New("allocator: tenant not registered")
	ErrInvalidParam   = errors.New("allocator: N, B, V must be positive and B must not exceed N")
	ErrKeysExhausted  = errors.New("allocator: key versions exhausted")
	// ErrReserveFailed 表示预留明确未落盘（持久化失败），与业务校验错误可区分。
	ErrReserveFailed = errors.New("allocator: reservation failed: definitely not persisted")
	// ErrReserveUnknown 表示预留结果未知：该批已按落盘处理并作废，本次分配失败。
	ErrReserveUnknown = errors.New("allocator: reservation outcome unknown: batch discarded")
)

// ReserveStatus 表示一次预留落盘的结果。
type ReserveStatus int

const (
	// ReserveOK 明确已落盘。
	ReserveOK ReserveStatus = iota
	// ReserveFailed 明确未落盘：内存与持久化高水位都不变。
	ReserveFailed
	// ReserveUnknown 结果未知：可能已落盘，按已落盘处理，该批作废。
	ReserveUnknown
)

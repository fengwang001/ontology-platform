package gesture

import "errors"

var (
	ErrInvalidDebounceCount      = errors.New("debounce count must be at least 1")
	ErrInvalidLongPressThreshold = errors.New("long press threshold must be at least 1")
	ErrInvalidDoubleClickWindow  = errors.New("double click window must not be negative")
	ErrInvalidLevel              = errors.New("level must be 0 or 1")
)

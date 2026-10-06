package srcmap

import (
	"errors"
	"fmt"
)

// ErrorCategory 对错误进行分类，调用方可通过 Category 判别。
type ErrorCategory string

const (
	CategoryInvalidArgument  ErrorCategory = "invalid_argument"
	CategoryNotFound         ErrorCategory = "name_not_found"
	CategoryDuplicate        ErrorCategory = "duplicate_name"
	CategoryPositionOverflow ErrorCategory = "position_overflow"
)

// MapError 携带错误类别与可读信息。
type MapError struct {
	Category ErrorCategory
	Message  string
}

func (e *MapError) Error() string { return string(e.Category) + ": " + e.Message }

func invalidf(format string, args ...any) error {
	return &MapError{Category: CategoryInvalidArgument, Message: fmt.Sprintf(format, args...)}
}

func notFoundf(format string, args ...any) error {
	return &MapError{Category: CategoryNotFound, Message: fmt.Sprintf(format, args...)}
}

func duplicatef(format string, args ...any) error {
	return &MapError{Category: CategoryDuplicate, Message: fmt.Sprintf(format, args...)}
}

func overflowf(format string, args ...any) error {
	return &MapError{Category: CategoryPositionOverflow, Message: fmt.Sprintf(format, args...)}
}

// CategoryOf 返回错误的类别；非本包错误返回空串。
func CategoryOf(err error) ErrorCategory {
	var me *MapError
	if errors.As(err, &me) {
		return me.Category
	}
	return ""
}

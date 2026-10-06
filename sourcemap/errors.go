package sourcemap

import "errors"

type ErrorCategory string

const (
	CategoryInvalidArgument  ErrorCategory = "invalid_argument"
	CategoryNotFound         ErrorCategory = "not_found"
	CategoryDuplicateName    ErrorCategory = "duplicate_name"
	CategoryPositionOverflow ErrorCategory = "position_overflow"
)

type Error struct {
	Category ErrorCategory
	Message  string
}

func (e Error) Error() string { return e.Message }

func invalidArgument(message string) error {
	return Error{Category: CategoryInvalidArgument, Message: message}
}

func notFound(message string) error {
	return Error{Category: CategoryNotFound, Message: message}
}

func duplicateName(message string) error {
	return Error{Category: CategoryDuplicateName, Message: message}
}

func positionOverflow(message string) error {
	return Error{Category: CategoryPositionOverflow, Message: message}
}

func CategoryOf(err error) ErrorCategory {
	var mappingError Error
	if errors.As(err, &mappingError) {
		return mappingError.Category
	}
	return ""
}

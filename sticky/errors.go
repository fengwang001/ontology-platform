package sticky

import "errors"

var (
	ErrEmptyMemberID   = errors.New("sticky: empty member id")
	ErrDuplicateJoin   = errors.New("sticky: duplicate join in batch")
	ErrMemberNotFound  = errors.New("sticky: leave member not found")
	ErrTooManyMembers  = errors.New("sticky: member count exceeds limit")
	ErrInvalidArgument = errors.New("sticky: invalid argument")
)

package cron

import "errors"

var (
	// ErrSyntax means a cron expression has wrong number of fields,
	// an empty item or illegal characters.
	ErrSyntax = errors.New("cron: syntax error")

	// ErrValueOutOfRange means a field value is outside its range.
	ErrValueOutOfRange = errors.New("cron: value out of range")

	// ErrReversedRange means an a-b range has a > b.
	ErrReversedRange = errors.New("cron: reversed range")

	// ErrInvalidStep means a step s is out of its allowed range.
	ErrInvalidStep = errors.New("cron: invalid step")

	// ErrNoNextFire means no matching minute exists within the search horizon.
	ErrNoNextFire = errors.New("cron: no next fire time")

	// ErrInvalidTime means a time argument is outside [0, maxMinute].
	ErrInvalidTime = errors.New("cron: invalid time")

	// ErrInvalidArgument means a NewJob argument is illegal.
	ErrInvalidArgument = errors.New("cron: invalid argument")

	// ErrClockBackwards means a Sync now precedes a previous Sync now.
	ErrClockBackwards = errors.New("cron: clock moved backwards")

	// ErrTooManyMissed means a window contains more than 100 fire times.
	ErrTooManyMissed = errors.New("cron: too many missed firings")

	// ErrTaskNotFound means Finish references an unknown task id.
	ErrTaskNotFound = errors.New("cron: task not found")
)

package indexorder

import "errors"

var (
	ErrEmptyName             = errors.New("register: index name is empty")
	ErrDuplicateName         = errors.New("register: index name already exists")
	ErrEmptyColumns          = errors.New("register: column list is empty")
	ErrEmptyColumnName       = errors.New("register: column name is empty")
	ErrInvalidDirection      = errors.New("register: invalid direction")
	ErrInvalidNullsOrder     = errors.New("register: invalid nulls ordering")
	ErrDuplicateColumn       = errors.New("register: duplicate column within index")
	ErrIndexNotFound         = errors.New("drop: index name not found")
	ErrEqEmptyColumnName     = errors.New("choose: eq contains empty column name")
	ErrOrderEmptyColumnName  = errors.New("choose: order by contains empty column name")
	ErrOrderInvalidDirection = errors.New("choose: invalid direction in order by")
	ErrOrderInvalidNulls     = errors.New("choose: invalid nulls ordering in order by")
	ErrNoMatchingIndex       = errors.New("choose: no registered index satisfies the ordering")
)

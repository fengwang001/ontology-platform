package tier

import "errors"

var (
	ErrInvalidArg = errors.New("tier: invalid argument")
	ErrDuplicate  = errors.New("tier: duplicate name or rank")
	ErrNoTier     = errors.New("tier: no tier registered")
)

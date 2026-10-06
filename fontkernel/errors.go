package fontkernel

import "errors"

// 六类可区分错误。拒绝次序见 register/advance 的校验顺序。
var (
	ErrInvalidArgument = errors.New("fontkernel: invalid argument")
	ErrClockRewound    = errors.New("fontkernel: clock rewound")
	ErrFamilyMissing   = errors.New("fontkernel: family not registered")
	ErrFaceMissing     = errors.New("fontkernel: face not found")
	ErrDuplicate       = errors.New("fontkernel: duplicate registration")
	ErrInvalidLoadOp   = errors.New("fontkernel: load state operation not allowed")
)

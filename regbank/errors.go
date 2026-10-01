package regbank

import "errors"

var (
	ErrLayout     = errors.New("regbank: invalid register layout")
	ErrUnknownReg = errors.New("regbank: register does not exist")
	ErrBadBE      = errors.New("regbank: invalid byte enable")
	ErrUnknownFld = errors.New("regbank: field does not exist")
	ErrValueRange = errors.New("regbank: value out of field range")
)

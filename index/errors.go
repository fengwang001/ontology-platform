package index

import (
	"errors"
	"fmt"
)

// 可区分的整体验证失败原因。
var (
	ErrUnknownColumn = errors.New("index: unknown column")
	ErrTypeMismatch  = errors.New("index: value type does not match column type")
	ErrEmptySet      = errors.New("index: IN set must not be empty")
	ErrInvalidLimit  = errors.New("index: combination limit L must be positive")
	ErrKeyLength     = errors.New("index: key length does not match column count")
	ErrKeyType       = errors.New("index: key value type does not match column type")
)

func unknownColumnErr(name string) error {
	return fmt.Errorf("%w: %q", ErrUnknownColumn, name)
}

func typeMismatchErr(col Column, v Value) error {
	return fmt.Errorf("%w: column %q is %s, got %s value %v", ErrTypeMismatch, col.Name, col.Type, v.Kind, v)
}

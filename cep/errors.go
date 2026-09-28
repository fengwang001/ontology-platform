package cep

import (
	"errors"
	"fmt"
)

// 可区分的拒绝原因。调用方可用 errors.Is 精确判断。
var (
	ErrInvalidConfig  = errors.New("cep: invalid config")
	ErrEmptyKey       = errors.New("cep: empty key")
	ErrEmptyType      = errors.New("cep: empty type")
	ErrTimeRegression = errors.New("cep: time regression within same key")
	ErrQueueOverflow  = errors.New("cep: pending queue overflow")
)

// RejectError 携带被拒绝事件在批次中的下标，便于定位。
type RejectError struct {
	Err   error
	Index int
}

func (e *RejectError) Error() string {
	return fmt.Sprintf("%v (batch index %d)", e.Err, e.Index)
}

func (e *RejectError) Unwrap() error { return e.Err }

// IsReject 判断 err 是否属于某一种拒绝原因。
func IsReject(err, target error) bool { return errors.Is(err, target) }

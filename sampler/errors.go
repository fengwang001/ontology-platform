package sampler

import "errors"

// 每一种输入失败都对应一个互不相同的哨兵错误，调用方可用 errors.Is 精确区分。
var (
	ErrInvalidSampleSize = errors.New("sampler: sample size must be positive")
	ErrEmptyID           = errors.New("sampler: element id must not be empty")
	ErrDuplicateID       = errors.New("sampler: duplicate element id")
	ErrIllegalWeight     = errors.New("sampler: weight must be a finite positive number")
	ErrZeroWeight        = errors.New("sampler: weight must not be zero")
	ErrIllegalRandom     = errors.New("sampler: random value must be within (0,1)")
	ErrRandomsExhausted  = errors.New("sampler: random source exhausted")
)

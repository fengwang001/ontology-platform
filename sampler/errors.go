package sampler

// 各类输入错误使用互不相同、可区分的哨兵错误，调用方可用 errors.Is 精确判别。
var (
	// ErrInvalidWeight 权重非法（为 NaN、Inf 或负数）。
	ErrInvalidWeight = weightError{}
	// ErrZeroWeight 权重为零。
	ErrZeroWeight = zeroWeightError{}
	// ErrEmptyID 元素标识为空。
	ErrEmptyID = emptyIDError{}
	// ErrDuplicateID 元素标识与已接受元素重复。
	ErrDuplicateID = duplicateIDError{}
	// ErrRandomExhausted 随机源已用尽，无法再取随机数。
	ErrRandomExhausted = randomExhaustedError{}
	// ErrInvalidRandom 随机数非法（不在 (0,1) 内，或为 NaN/Inf）。
	ErrInvalidRandom = invalidRandomError{}
	// ErrInvalidSampleSize 样本数非法（构造时 k<=0）。
	ErrInvalidSampleSize = invalidSampleSizeError{}
	// ErrInvalidRandomSource 注入的随机源为 nil。
	ErrInvalidRandomSource = invalidRandomSourceError{}
)

type weightError struct{}

func (weightError) Error() string { return "sampler: weight must be a finite positive number" }

type zeroWeightError struct{}

func (zeroWeightError) Error() string { return "sampler: weight must not be zero" }

type emptyIDError struct{}

func (emptyIDError) Error() string { return "sampler: element id must not be empty" }

type duplicateIDError struct{ id string }

func (duplicateIDError) Error() string { return "sampler: duplicate element id" }

type randomExhaustedError struct{}

func (randomExhaustedError) Error() string { return "sampler: random source exhausted" }

type invalidRandomError struct{}

func (invalidRandomError) Error() string { return "sampler: random value must be within (0,1)" }

type invalidSampleSizeError struct{}

func (invalidSampleSizeError) Error() string { return "sampler: sample size must be positive" }

type invalidRandomSourceError struct{}

func (invalidRandomSourceError) Error() string { return "sampler: random source must not be nil" }

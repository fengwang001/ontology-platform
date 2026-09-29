package slottedpage

import "errors"

// 按优先级排列的可区分拒绝原因。
var (
	ErrEmptyRecord       = errors.New("slottedpage: record must not be empty")
	ErrRecordTooLarge    = errors.New("slottedpage: record plus one slot exceeds empty-page capacity")
	ErrInvalidSlotID     = errors.New("slottedpage: slot id out of range")
	ErrEmptySlot         = errors.New("slottedpage: slot is empty")
	ErrInsufficientSpace = errors.New("slottedpage: insufficient free bytes in page")
)

// 配置与映像相关的错误（不属于操作拒绝原因序列）。
var (
	errInvalidConfig = errors.New("slottedpage: invalid page configuration")
	errInvalidImage  = errors.New("slottedpage: invalid page image")
)

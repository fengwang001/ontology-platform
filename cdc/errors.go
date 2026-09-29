package cdc

import "errors"

// 可区分的拒绝原因，调用方可用 errors.Is 判定。
var (
	// ErrInvalidColumnType 非法列类型。
	ErrInvalidColumnType = errors.New("cdc: invalid column type")
	// ErrEmptyColumnName 空列名。
	ErrEmptyColumnName = errors.New("cdc: empty column name")
	// ErrDuplicateColumn 同一模式内列名重复。
	ErrDuplicateColumn = errors.New("cdc: duplicate column name")
	// ErrColumnNotFound 增删改目标列不存在。
	ErrColumnNotFound = errors.New("cdc: column not found")
	// ErrColumnExists 新增列已存在。
	ErrColumnExists = errors.New("cdc: column already exists")
	// ErrVersionNotFound 事件或目标模式版本未注册。
	ErrVersionNotFound = errors.New("cdc: schema version not registered")
	// ErrVersionExists 注册重复版本。
	ErrVersionExists = errors.New("cdc: schema version already registered")
	// ErrMissingRequired 必填列在事件中缺失。
	ErrMissingRequired = errors.New("cdc: required column missing")
	// ErrConversionFailed 字符串转整数失败。
	ErrConversionFailed = errors.New("cdc: type conversion failed")
	// ErrInvalidValue 事件字段值类型不受支持（仅允许 int/int64/string）。
	ErrInvalidValue = errors.New("cdc: unsupported event value type")
)

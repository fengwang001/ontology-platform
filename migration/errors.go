// Package migration 提供键控状态的惰性（lazy）模式迁移。
//
// 模式版本可以升级而不触碰任何键；每个键只在被读取时，沿着已登记的
// 迁移函数链，从其存储版本逐版本迁移到当前版本，并在全部步骤成功后
// 原子写回。后续读取直接命中当前版本，已执行过的步骤不会重复执行。
package migration

import "errors"

// 调用方可通过 errors.Is 区分以下互不相同的拒绝原因。
var (
	// ErrInvalidVersion 表示版本参数非法（非正、越界或方向错误）。
	ErrInvalidVersion = errors.New("migration: invalid schema version")

	// ErrInvalidKey 表示键参数非法（空键）。
	ErrInvalidKey = errors.New("migration: invalid key")

	// ErrInvalidArgument 表示其它参数非法（如迁移函数为 nil）。
	ErrInvalidArgument = errors.New("migration: invalid argument")

	// ErrKeyNotFound 表示读取或查询的键不存在。
	ErrKeyNotFound = errors.New("migration: key not found")

	// ErrMissingMigration 表示迁移链不完整：某来源版本缺少登记的迁移函数。
	ErrMissingMigration = errors.New("migration: missing migration step")

	// ErrMigrationFailed 表示链上某一步迁移函数返回错误；存储保持原样。
	ErrMigrationFailed = errors.New("migration: migration step failed")

	// ErrMigrationExists 表示同一来源版本重复登记迁移函数。
	ErrMigrationExists = errors.New("migration: migration already registered for source version")
)

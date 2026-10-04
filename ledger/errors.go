package ledger

import (
	"errors"

	"ontology/plan"
)

// 账本操作的哨兵错误，均可通过 errors.Is 区分。
var (
	// ErrInvalidArgument 参数非法（含计划结构越界与开销超 10^12）。
	ErrInvalidArgument = errors.New("ledger: invalid argument")
	// ErrClockRewind now 小于已接受操作的最大 now。
	ErrClockRewind = errors.New("ledger: clock rewind")
	// ErrDuplicateQID 预留使用了已存在的 qid。
	ErrDuplicateQID = errors.New("ledger: duplicate query id")
	// ErrUnknownDataset 引用了未登记的数据集。
	ErrUnknownDataset = errors.New("ledger: unknown dataset")
	// ErrUnknownAnalyst 引用了未登记的分析师。
	ErrUnknownAnalyst = errors.New("ledger: unknown analyst")
	// ErrNotDisjoint Par 计划的子分区相交（转发自 plan 包）。
	ErrNotDisjoint = plan.ErrNotDisjoint
	// ErrDatasetExhausted 某数据集终身额度不足。
	ErrDatasetExhausted = errors.New("ledger: dataset budget exhausted")
	// ErrAnalystExhausted 分析师当前窗口额度不足。
	ErrAnalystExhausted = errors.New("ledger: analyst window budget exhausted")
	// ErrUnknownQuery qid 不存在。
	ErrUnknownQuery = errors.New("ledger: unknown query")
	// ErrWrongState 查询当前状态不允许该操作（含对已结束查询的任何操作）。
	ErrWrongState = errors.New("ledger: query in wrong state")
	// ErrOverspend Commit 的 actual 超过预留开销。
	ErrOverspend = errors.New("ledger: commit actual cost overspends reservation")
)

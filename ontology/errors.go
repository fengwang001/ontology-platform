package ontology

import "errors"

// 固定判定次序的四类错误（互斥，按次序只报第一类）：
//  1. 对象实例不存在
//  2. 目标属性历史记录不存在
//  3. 对象整体删除态下尝试属性历史单独删除/撤销
//  4. 对已单独删除的记录重复发起单独删除
var (
	ErrObjectNotFound        = errors.New("object instance not found")
	ErrHistoryNotFound       = errors.New("property history record not found")
	ErrObjectTombstoned      = errors.New("object is globally tombstoned")
	ErrHistoryAlreadyDeleted = errors.New("history record already individually deleted")

	// 以下错误不属于规定的四类，用于对象级操作的非法状态跃迁。
	ErrObjectAlreadyTombstoned = errors.New("object already globally tombstoned")
	ErrObjectNotTombstoned     = errors.New("object is not globally tombstoned")

	// 辅助性错误（不属于规定的四类）。
	ErrDuplicateHistory  = errors.New("history record id already exists")
	ErrInvalidArgument   = errors.New("invalid argument")
	ErrHistoryNotDeleted = errors.New("history record is not individually deleted")
)

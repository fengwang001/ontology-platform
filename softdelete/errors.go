package softdelete

import "errors"

var (
	// ErrNotFound 表示对象从未存在（已创建后被物理删除不算此错误）。
	ErrNotFound = errors.New("softdelete: object not found")
	// ErrPhysicallyDeleted 表示对象已被物理删除，唯一键已释放。
	ErrPhysicallyDeleted = errors.New("softdelete: object has been physically deleted")
	// ErrAlreadyDeleted 表示对象已处于软删状态，不能重复软删。
	ErrAlreadyDeleted = errors.New("softdelete: object is already soft-deleted")
	// ErrNotDeleted 表示对象处于存活状态，无法复活。
	ErrNotDeleted = errors.New("softdelete: object is not soft-deleted")
	// ErrKeyOccupied 表示唯一键仍被存活或软删对象占用。
	ErrKeyOccupied = errors.New("softdelete: unique key is occupied")
)

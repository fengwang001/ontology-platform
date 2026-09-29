package rbac

import "errors"

var (
	// ErrRoleNotFound 表示引用了未注册的角色。
	ErrRoleNotFound = errors.New("rbac: role not found")
	// ErrRoleExists 表示重复创建同名角色。
	ErrRoleExists = errors.New("rbac: role already exists")
	// ErrCycle 表示新增继承关系会形成循环继承。
	ErrCycle = errors.New("rbac: cyclic role inheritance")
	// ErrDuplicate 表示重复赋予角色、继承关系或权限。
	ErrDuplicate = errors.New("rbac: duplicate assignment")
	// ErrGrantNotFound 表示撤销的授权（含角色赋予/继承关系）不存在。
	ErrGrantNotFound = errors.New("rbac: grant not found")
)

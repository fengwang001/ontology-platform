package rbac

import "errors"

var (
	ErrRoleNotFound        = errors.New("rbac: role does not exist")
	ErrRoleExists          = errors.New("rbac: role already exists")
	ErrRoleCycle           = errors.New("rbac: role inheritance cycle detected")
	ErrDuplicateGrant      = errors.New("rbac: grant already exists")
	ErrDuplicateAssignment = errors.New("rbac: subject already has the role")
	ErrDuplicateInherit    = errors.New("rbac: role inheritance already exists")
	ErrGrantNotFound       = errors.New("rbac: grant to revoke does not exist")
	ErrAssignmentNotFound  = errors.New("rbac: subject-role assignment does not exist")
)

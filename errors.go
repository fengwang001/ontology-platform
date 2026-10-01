package depsolver

import "errors"

// 可区分的失败原因。
var (
	ErrInvalidVersion   = errors.New("depsolver: invalid version format")
	ErrDuplicateVersion = errors.New("depsolver: package@version already published")
	ErrInvalidRange     = errors.New("depsolver: invalid dependency range")
	ErrVersionNotFound  = errors.New("depsolver: cannot yank version that does not exist")
	ErrPackageNotFound  = errors.New("depsolver: dependency refers to package not in repository")
	ErrNoSolution       = errors.New("depsolver: no set of mutually compatible versions exists")
	ErrEmptyPackageName = errors.New("depsolver: package name must not be empty")
	ErrInvalidArgument  = errors.New("depsolver: invalid argument")
)

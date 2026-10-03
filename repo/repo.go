package repo

import "errors"

// 错误哨兵；errors.Is 可区分，被拒操作不改任何状态。
var (
	ErrInvalidArg    = errors.New("repo: invalid argument")
	ErrDefNotFound   = errors.New("repo: definition not found")
	ErrInstanceExist = errors.New("repo: instance already exists")
	ErrNoInstance    = errors.New("repo: instance not found")
)

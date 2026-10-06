// Package submod 实现超级仓库的子模块固定与更新协调器。
//
// 协调器维护超级仓库当前提交上的子模块表（挂载路径 -> 固定提交），
// 支持状态查询、按固定递归对齐、按跟踪分支推进、添加与移除挂载。
// 所有公开操作都可并发调用，效果等价于某个串行顺序。
package submod

import "errors"

// 可区分的错误种类。每次调用只报告适用次序中最靠前的一个：
//
//	参数非法 > 循环挂载 > 路径冲突 > 仓库不存在 > 分支不存在 > 悬空固定 > 有未提交修改 > 跟踪分支非快进
//
// 使用 errors.Is 判定种类。
var (
	// ErrInvalidParam 参数非法：空路径、空仓库标识、挂载点不存在等。
	ErrInvalidParam = errors.New("submod: invalid parameter")
	// ErrCycle 循环挂载：某仓库出现在自己的祖先挂载链上。
	ErrCycle = errors.New("submod: cyclic mount")
	// ErrPathConflict 路径冲突：两条记录路径相同或互为祖先后代。
	ErrPathConflict = errors.New("submod: path conflict")
	// ErrRepoNotFound 目标仓库不存在。
	ErrRepoNotFound = errors.New("submod: repo not found")
	// ErrBranchNotFound 跟踪分支不存在。
	ErrBranchNotFound = errors.New("submod: branch not found")
	// ErrDanglingPin 悬空固定：固定提交不存在于目标仓库。
	ErrDanglingPin = errors.New("submod: dangling pin")
	// ErrDirty 挂载点有未提交修改。
	ErrDirty = errors.New("submod: uncommitted changes")
	// ErrNonFastForward 跟踪分支新顶端不是旧固定点的后代。
	ErrNonFastForward = errors.New("submod: tracking branch not fast-forward")
)

// Package api 是状态机复制的对外门面：所有操作经这里包装。
// 依赖方向：api -> repl -> sm（单向，无反向依赖）。
package api

import (
	"ontology/repl"
	"ontology/sm"
)

// 转出 repl 的哨兵错误，供外部用 errors.Is 判定；三者互不相同。
var (
	ErrEmptyCommand     = repl.ErrEmptyCommand
	ErrCommitOutOfRange = repl.ErrCommitOutOfRange
	ErrSnapOutOfRange   = repl.ErrSnapOutOfRange
)

// API 包装一个副本，对外暴露受控操作集合。
type API struct {
	r *repl.Replica
}

// New 返回一个日志为空、状态为 0 的副本门面。
func New() *API { return &API{r: repl.New()} }

// Append 把一条命令追加到日志末尾；空/未识别命令返回 ErrEmptyCommand。
func (a *API) Append(cmd sm.Command) error { return a.r.Append(cmd) }

// Commit 提交到下标 i：只增不减，超过日志长度返回 ErrCommitOutOfRange。
func (a *API) Commit(i int) error { return a.r.Commit(i) }

// Apply 按下标升序应用 lastApplied+1..committed；无增长则空操作。
func (a *API) Apply() { a.r.Apply() }

// Restart 从下标 snapIndex、状态 snapState 的快照恢复。
func (a *API) Restart(snapIndex, snapState int) error {
	return a.r.Restart(snapIndex, snapState)
}

// State / LastApplied / Committed 为只读访问，可被多 goroutine 并发调用。
func (a *API) State() int       { return a.r.State() }
func (a *API) LastApplied() int { return a.r.LastApplied() }
func (a *API) Committed() int   { return a.r.Committed() }

// SelfCheck 运行内置四条不变量（含 O(1) 续读）自检；通过返回 nil。
func (a *API) SelfCheck() error { return a.r.SelfCheck() }

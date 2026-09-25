// Package api 是审计日志对外的门面，只依赖 log（单向依赖）。
package api

import (
	"ontology/ent"
	"ontology/log"
)

// 可判定的哨兵错误（与 log 包同一实例，errors.Is 可直接判定）。
var (
	ErrNegativeTS   = log.ErrNegativeTS
	ErrEmptyWho     = log.ErrEmptyWho
	ErrEmptyOp      = log.ErrEmptyOp
	ErrTSOutOfOrder = log.ErrTSOutOfOrder
)

// Audit 是对外的 append-only 变更审计日志。
type Audit struct {
	l *log.Log
}

// New 返回含创世条目的审计日志。
func New() *Audit {
	return &Audit{l: log.New()}
}

// Append 追加一条，返回系统自动分配的 Seq；非法输入返回可判定哨兵错误。
func (a *Audit) Append(ts int64, who, op string) (int64, error) {
	return a.l.Append(ts, who, op)
}

// Entries 返回全部审计条目（含创世条目）的副本。
func (a *Audit) Entries() []ent.Entry {
	return a.l.Entries()
}

// Verify 逐条重算，返回首个被篡改条目的 Seq；无篡改返回 -1。
func (a *Audit) Verify() int64 {
	return a.l.Verify()
}

// Affected 返回从 seq 起到末尾的不可信条目的 Seq 列表。
func (a *Audit) Affected(seq int64) []int64 {
	return a.l.Affected(seq)
}

// SelfCheck 对内置操作序列核验四条不变量。
func (a *Audit) SelfCheck() error {
	return a.l.SelfCheck()
}

// Package notify 按分组节奏发送告警通知，并保留失败状态等待重试。
package notify

import (
	"ontology/alertstore"
	"ontology/suppress"
)

// 哨兵错误：被拒绝操作按 参数非法 → 时钟回退 → 状态类 的顺序只报第一个。
var (
	ErrInvalidArgument = errInvalid{}
	ErrClockRollback   = errClock{}
	ErrNotFiring       = alertstore.ErrNotFiring
	ErrSilenceExists   = suppress.ErrSilenceExists
	ErrSilenceMissing  = suppress.ErrSilenceMissing
	ErrAlertLimit      = alertstore.ErrAlertLimit
)

type errInvalid struct{}
type errClock struct{}

func (errInvalid) Error() string { return "invalid argument" }
func (errClock) Error() string   { return "clock moved backwards" }

// Notification 是一次分组通知的完整内容。
type Notification struct {
	Group    string
	Firing   []string
	Resolved []string
	At       int64
}

// SendFunc 由调用方提供，返回 nil 才算发送成功。
type SendFunc func(Notification) error

// TickResult 列出本次成功发送与发送失败的分组键，均按组键序。
type TickResult struct {
	Sent   []string
	Failed []string
}

// FireResult 是 Fire 的返回值；Deduped 表示本次为重复 Fire。
type FireResult struct {
	Alert   *alertstore.Alert
	Deduped bool
}

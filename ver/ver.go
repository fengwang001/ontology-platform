// Package ver 定义版本号、快照 id 与版本边界比较（<= 语义）。不依赖其他包。
package ver

// Visible 报告版本 v 是否在快照 snap 内可见：边界语义为 v <= snap（含等于）。
func Visible(snap, v int) bool { return v <= snap }

// ValidSnap 报告 snap 是否是合法快照 id：非负且不超过当前全局版本 current。
func ValidSnap(snap, current int) bool { return snap >= 0 && snap <= current }

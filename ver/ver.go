// Package ver 定义版本号、快照 id 与版本边界比较。不依赖其他包。
package ver

// Version 是全局版本号，也是快照 id（快照即某一时刻的 ver）。
type Version = int

// LEQ 报告版本 v 是否在快照 snap 边界内（含等于，v <= snap）。
func LEQ(v, snap Version) bool { return v <= snap }

// Valid 报告 snap 是否是合法的快照 id：非负且不超过当前版本 cur。
func Valid(snap, cur Version) bool { return snap >= 0 && snap <= cur }

// Package ws 做行尾空白的延迟判定：一串空格/制表符只有在见到行尾或
// 流结束时才知道是否位于行尾。本包不依赖其他包。
package ws

// Tracker 保存当前尚未判定的空白计数，非并发安全。
type Tracker struct{ n int }

// New 创建判定器。
func New() *Tracker { return &Tracker{} }

// IsWS 报告 b 是否为空格或制表符。
func IsWS(b byte) bool { return b == ' ' || b == '\t' }

// Add 登记一个空白字节，返回待定空白总数。
func (t *Tracker) Add() int { t.n++; return t.n }

// Pending 返回当前待定空白字节数。
func (t *Tracker) Pending() int { return t.n }

// Keep 在见到非空白、非行尾字节时调用：待定空白属于行中间，
// 全部原样保留；随后该触发字节由调用方自行输出。
func (t *Tracker) Keep() int { n := t.n; t.n = 0; return n }

// Drop 在见到行尾或流结束时调用：待定空白位于行尾，全部删除。
func (t *Tracker) Drop() int { n := t.n; t.n = 0; return n }

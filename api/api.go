// Package api 是净零折叠变更日志的对外门面。
// 仅依赖 norm 包；并发安全由 norm 内部的读写锁保证。
package api

import "ontology/norm"

// Op 是一条带符号增量：正数表示 +k，负数表示 -k，零非法。
// k 必须为正整数，即 Op 的量值 |Op| > 0。
type Op int64

// Normalizer 对外暴露 Apply / Net / Changelog / SelfCheck。
type Normalizer struct {
	n *norm.Normalizer
}

// New 创建 changelog 长度上限为 maxDepth 的规范化器。
func New(maxDepth int) *Normalizer {
	return &Normalizer{n: norm.New(maxDepth)}
}

// Apply 推进一条操作；非法、超深、溢出均返回可判定哨兵错误且状态不变。
func (c *Normalizer) Apply(op Op) error {
	_, err := c.n.Apply(int64(op))
	return err
}

// Net 返回所有已接收操作的代数和（可为负）。
func (c *Normalizer) Net() int64 { return c.n.Net() }

// Changelog 返回未了结序列副本，正负号即方向。
func (c *Normalizer) Changelog() []int64 { return c.n.Changelog() }

// SelfCheck 对内置操作序列核验四条不变量。
func (c *Normalizer) SelfCheck() error { return c.n.SelfCheck() }

// 重新导出哨兵错误，供调用方 errors.Is 判定。
var (
	ErrInvalidIncrement = norm.ErrInvalidIncrement
	ErrDepthExceeded    = norm.ErrDepthExceeded
	ErrNetOverflow      = norm.ErrNetOverflow
)

// Package sink 定义下游抽象与可注入故障（短写、背压、断开、写错误）。
package sink

import "errors"

// ErrBackpressure 表示下游暂时不可写；数据必须保留，稍后重试。
var ErrBackpressure = errors.New("sink: temporarily not writable")

// ErrDisconnected 表示下游连接断开；可用断点状态重建管线续写。
var ErrDisconnected = errors.New("sink: disconnected")

// Sink 是下游抽象：一次 Write 可能只接受部分字节（短写）。
type Sink interface {
	Write(p []byte) (int, error)
}

// Func 把函数适配成 Sink。
type Func func(p []byte) (int, error)

// Write 实现 Sink。
func (f Func) Write(p []byte) (int, error) { return f(p) }

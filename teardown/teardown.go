// Package teardown 把事件映射到 conn 的转移表并执行。依赖 conn。
package teardown

import "ontology/conn"

// Engine 对单个 conn.Conn 做事件分派。
type Engine struct {
	C *conn.Conn
}

// New 构造分派器，twoMSL 为 TIME_WAIT 时长。
func New(twoMSL int64) *Engine { return &Engine{C: conn.New(twoMSL)} }

// Close 主动关闭。
func (e *Engine) Close() error { return e.C.Apply(conn.EvClose, 0) }

// RecvACK 收到对端 ACK，now 为事件逻辑时间。
func (e *Engine) RecvACK(now int64) error { return e.C.Apply(conn.EvACK, now) }

// RecvFIN 收到对端 FIN，now 为事件逻辑时间。
func (e *Engine) RecvFIN(now int64) error { return e.C.Apply(conn.EvFIN, now) }

// RecvData 收到对端数据（半关闭下仍合法）。
func (e *Engine) RecvData() error { return e.C.Apply(conn.EvData, 0) }

// RecvRST 收到重置。
func (e *Engine) RecvRST() error { return e.C.Apply(conn.EvRST, 0) }

// Tick 推进逻辑时钟到 now。
func (e *Engine) Tick(now int64) error { return e.C.Apply(conn.EvTick, now) }

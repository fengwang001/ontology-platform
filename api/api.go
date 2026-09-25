// Package api 对外接口：包装 teardown 分派，暴露只读观察与自检。依赖 teardown。
package api

import (
	"fmt"

	"ontology/conn"
	"ontology/teardown"
)

// Conn 是一条处于拆除流程中的连接。
type Conn struct{ e *teardown.Engine }

// New 创建连接，msl 使得 TIME_WAIT 时长为 2*msl（msl>0），初始 ESTABLISHED。
func New(msl int64) *Conn { return &Conn{e: teardown.New(2 * msl)} }

// Close 主动关闭。
func (c *Conn) Close() error { return c.e.Close() }

// RecvACK 收到对端 ACK，now 为事件逻辑时间。
func (c *Conn) RecvACK(now int64) error { return c.e.RecvACK(now) }

// RecvFIN 收到对端 FIN，now 为事件逻辑时间。
func (c *Conn) RecvFIN(now int64) error { return c.e.RecvFIN(now) }

// RecvData 收到对端数据。
func (c *Conn) RecvData() error { return c.e.RecvData() }

// RecvRST 收到重置。
func (c *Conn) RecvRST() error { return c.e.RecvRST() }

// Tick 推进逻辑时钟到 now。
func (c *Conn) Tick(now int64) error { return c.e.Tick(now) }

// State 返回当前状态名。
func (c *Conn) State() string { return c.e.C.State().String() }

// EnterTime 返回 TIME_WAIT 基准时间。
func (c *Conn) EnterTime() int64 { return c.e.C.EnterTime() }

// SentFIN 返回已发 FIN 计数。
func (c *Conn) SentFIN() int { return c.e.C.SentFIN() }

// SentACK 返回已回 ACK 计数。
func (c *Conn) SentACK() int { return c.e.C.SentACK() }

// SelfCheck 对内置事件序列核验四条不变量，全部通过返回 nil。
func SelfCheck() error {
	// 不变量 1+3：七步序列与转移表一致、TIME_WAIT 计时正确（2MSL=100）
	c := New(50)
	steps := []struct {
		run   func() error
		state string
		enter int64
	}{
		{func() error { return c.Close() }, "FIN_WAIT_1", 0},
		{func() error { return c.RecvACK(0) }, "FIN_WAIT_2", 0},
		{func() error { return c.RecvData() }, "FIN_WAIT_2", 0},
		{func() error { return c.RecvFIN(100) }, "TIME_WAIT", 100},
		{func() error { return c.RecvFIN(150) }, "TIME_WAIT", 150},
		{func() error { return c.Tick(249) }, "TIME_WAIT", 150},
		{func() error { return c.Tick(250) }, "CLOSED", 150},
	}
	for i, s := range steps {
		if err := s.run(); err != nil {
			return fmt.Errorf("selfcheck step %d: %w", i+1, err)
		}
		if c.State() != s.state || c.EnterTime() != s.enter {
			return fmt.Errorf("selfcheck step %d: got %s enter=%d, want %s enter=%d",
				i+1, c.State(), c.EnterTime(), s.state, s.enter)
		}
	}
	// 不变量 2：半关闭可收数据；CLOSED 拒绝报文
	h := New(50)
	h.Close()
	h.RecvACK(0)
	if err := h.RecvData(); err != nil || h.State() != "FIN_WAIT_2" {
		return fmt.Errorf("selfcheck half-close FIN_WAIT_2: %v %s", err, h.State())
	}
	// 不变量 4：非法事件零副作用，之后仍可用
	st, fin, ack := c.State(), c.SentFIN(), c.SentACK()
	if err := c.RecvData(); err != conn.ErrIllegalData {
		return fmt.Errorf("selfcheck CLOSED RecvData: %v", err)
	}
	if c.State() != st || c.SentFIN() != fin || c.SentACK() != ack {
		return fmt.Errorf("selfcheck side-effect after illegal event")
	}
	return nil
}

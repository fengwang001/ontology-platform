package raid

import "sync"

// CrashController 模拟断电：武装后允许再执行固定次数的设备写，
// 之后机器"掉电"，所有后续写被丢弃并返回 ErrCrashed。
// 计数对所有被包装的设备（数据盘 + 日志盘）全局生效，
// 因此同一写入序列与同一断电点产生逐字节相同的盘内容。
type CrashController struct {
	mu        sync.Mutex
	remaining int  // -1 表示未武装
	crashed   bool // 是否已掉电
	writes    int  // 已成功落盘的写次数
}

func NewCrashController() *CrashController {
	return &CrashController{remaining: -1}
}

// Arm 允许再执行 writes 次设备写，第 writes+1 次写触发断电。
func (c *CrashController) Arm(writes int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.remaining = writes
}

// Disarm 解除武装（模拟重新上电）。
func (c *CrashController) Disarm() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.remaining = -1
	c.crashed = false
}

func (c *CrashController) Crashed() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.crashed
}

// Writes 返回已成功落盘的设备写次数。
func (c *CrashController) Writes() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.writes
}

func (c *CrashController) gate() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.crashed {
		return ErrCrashed
	}
	if c.remaining == 0 {
		c.crashed = true
		return ErrCrashed
	}
	if c.remaining > 0 {
		c.remaining--
	}
	c.writes++
	return nil
}

// Wrap 包装设备，使其写入经过断电闸门；读不受影响。
func (c *CrashController) Wrap(d Device) Device {
	return &crashDevice{ctrl: c, dev: d}
}

type crashDevice struct {
	ctrl *CrashController
	dev  Device
}

func (w *crashDevice) NumBlocks() int { return w.dev.NumBlocks() }

func (w *crashDevice) BlockSize() int { return w.dev.BlockSize() }

func (w *crashDevice) ReadBlock(b int) ([]byte, error) { return w.dev.ReadBlock(b) }

func (w *crashDevice) WriteBlock(b int, data []byte) error {
	if err := w.ctrl.gate(); err != nil {
		return err
	}
	return w.dev.WriteBlock(b, data)
}

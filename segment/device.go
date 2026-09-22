package segment

import "sync"

// Device 是一个可注入的进程内字节缓冲，充当 WAL 的"磁盘"。
// 它不碰真实文件系统，所有状态都在内存里。
// 所有方法都可并发调用；Append 是原子的，多次追加绝不交错。
type Device struct {
	mu  sync.RWMutex
	buf []byte
}

// NewDevice 返回一个空的 Device。
func NewDevice() *Device {
	return &Device{}
}

// Append 把 p 整体追加到缓冲末尾，返回写入起始偏移。
// 追加是原子的：并发追加时 p 的字节不会与其他追加交错，
// 也绝不会修改已写入的历史字节。
func (d *Device) Append(p []byte) int64 {
	d.mu.Lock()
	defer d.mu.Unlock()
	off := int64(len(d.buf))
	d.buf = append(d.buf, p...)
	return off
}

// Len 返回当前缓冲的字节数。
func (d *Device) Len() int64 {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return int64(len(d.buf))
}

// Bytes 返回 [from, to) 区间的只读视图，越界时收敛到有效范围。
// 调用方不得修改返回的字节；需要修改时请用 Snapshot。
func (d *Device) Bytes(from, to int64) []byte {
	d.mu.RLock()
	defer d.mu.RUnlock()
	if from < 0 {
		from = 0
	}
	if to > int64(len(d.buf)) {
		to = int64(len(d.buf))
	}
	if from >= to {
		return nil
	}
	return d.buf[from:to]
}

// Flip 把 off 处的字节与 mask 做异或，用于测试与演示中模拟位翻转。
func (d *Device) Flip(off int64, mask byte) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if off < 0 || off >= int64(len(d.buf)) {
		return
	}
	d.buf[off] ^= mask
}

// Truncate 把缓冲截断到前 n 字节，丢弃其后的全部内容。
func (d *Device) Truncate(n int64) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if n < 0 {
		n = 0
	}
	if n < int64(len(d.buf)) {
		d.buf = d.buf[:n]
	}
}

// Snapshot 返回整个缓冲的独立副本，可安全地逐字节比对。
func (d *Device) Snapshot() []byte {
	d.mu.RLock()
	defer d.mu.RUnlock()
	out := make([]byte, len(d.buf))
	copy(out, d.buf)
	return out
}

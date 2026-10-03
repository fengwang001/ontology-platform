// Package scan 管理每个分区的扫描会话：空闲/打开状态、epoch 计数器与
// 本会话已见键集合。
package scan

import "errors"

var ErrBusy = errors.New("scan: partition session busy")
var ErrNoSession = errors.New("scan: no matching open session")

type partition struct {
	open  bool
	epoch int64
	seen  map[int64]struct{}
}

// Manager 跟踪所有分区的扫描会话状态。不是并发安全的，由调用方串行化。
type Manager struct {
	parts []partition
}

func NewManager(p int) *Manager {
	return &Manager{parts: make([]partition, p)}
}

// Begin 打开分区 part 的会话。已有打开会话时返回 ErrBusy 且不消耗 epoch；
// 成功时 epoch 加 1 并返回新 epoch。
func (m *Manager) Begin(part int) (int64, error) {
	p := &m.parts[part]
	if p.open {
		return 0, ErrBusy
	}
	p.epoch++
	p.open = true
	p.seen = make(map[int64]struct{})
	return p.epoch, nil
}

// Check 校验分区 part 当前打开的会话 epoch 是否匹配，不匹配返回 ErrNoSession。
func (m *Manager) Check(part int, epoch int64) error {
	p := &m.parts[part]
	if !p.open || p.epoch != epoch {
		return ErrNoSession
	}
	return nil
}

// Mark 把键 k 记为本会话已见。调用前须通过 Check。
func (m *Manager) Mark(part int, k int64) {
	m.parts[part].seen[k] = struct{}{}
}

// Close 关闭分区 part 的会话并返回已见集合。调用前须通过 Check。
func (m *Manager) Close(part int) map[int64]struct{} {
	p := &m.parts[part]
	seen := p.seen
	p.open = false
	p.seen = nil
	return seen
}

// Epoch 返回分区 part 当前的 epoch 计数（含未完成的会话）。
func (m *Manager) Epoch(part int) int64 {
	return m.parts[part].epoch
}

// Open 报告分区 part 是否有打开的会话。
func (m *Manager) Open(part int) bool {
	return m.parts[part].open
}

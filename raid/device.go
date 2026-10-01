package raid

import "sync"

// Device 抽象块设备：固定块大小的随机读写。
type Device interface {
	NumBlocks() int
	BlockSize() int
	ReadBlock(b int) ([]byte, error)
	WriteBlock(b int, data []byte) error
}

// MemDevice 内存块设备，用于测试与模拟。
type MemDevice struct {
	mu     sync.RWMutex
	blocks [][]byte
}

// NewMemDevice 创建 numBlocks 个全零块的内存盘。
func NewMemDevice(numBlocks, blockSize int) *MemDevice {
	blocks := make([][]byte, numBlocks)
	for i := range blocks {
		blocks[i] = make([]byte, blockSize)
	}
	return &MemDevice{blocks: blocks}
}

func (m *MemDevice) NumBlocks() int { return len(m.blocks) }

func (m *MemDevice) BlockSize() int { return len(m.blocks[0]) }

func (m *MemDevice) ReadBlock(b int) ([]byte, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if b < 0 || b >= len(m.blocks) {
		return nil, ErrBlockOutOfRange
	}
	out := make([]byte, len(m.blocks[b]))
	copy(out, m.blocks[b])
	return out, nil
}

func (m *MemDevice) WriteBlock(b int, data []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if b < 0 || b >= len(m.blocks) {
		return ErrBlockOutOfRange
	}
	if len(data) != len(m.blocks[b]) {
		return ErrBlockSizeMismatch
	}
	copy(m.blocks[b], data)
	return nil
}

// Snapshot 返回全部块的深拷贝，用于确定性比对。
func (m *MemDevice) Snapshot() [][]byte {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([][]byte, len(m.blocks))
	for i, blk := range m.blocks {
		out[i] = make([]byte, len(blk))
		copy(out[i], blk)
	}
	return out
}

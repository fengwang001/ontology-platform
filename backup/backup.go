// Package backup 实现块级增量备份链管理器。
//
// 卷由定长块组成。链首为全量备份，其余为增量备份；
// 增量备份只存与父备份还原结果不同的块。
// 还原某备份时，每块取从它回溯到链首遇到的第一份内容。
// 删除中间备份时其块并入直接后继（后继优先），
// 并入后后继中与新父还原结果相同的块被剔除。
package backup

import (
	"errors"
	"fmt"
	"sync"
)

// 可区分的拒绝原因。
var (
	ErrBlockOutOfRange      = errors.New("backup: 块号越界")
	ErrBackupNotFound       = errors.New("backup: 备份不存在")
	ErrStorageLimitExceeded = errors.New("backup: 存储块总数将超上限")
	ErrBackupInUse          = errors.New("backup: 备份处于未关闭还原会话的回溯路径上")
	ErrChainEmpty           = errors.New("backup: 链为空，首个备份必须是全量")
	ErrInvalidBlockSize     = errors.New("backup: 数据长度与块大小不一致")
	ErrSessionClosed        = errors.New("backup: 还原会话已关闭")
)

// Backup 是链上的一个备份节点。
type Backup struct {
	ID     int
	Full   bool
	Blocks map[int][]byte
}

// BackupInfo 是备份的只读概要。
type BackupInfo struct {
	ID           int
	Full         bool
	StoredBlocks int
}

// Manager 管理卷与备份链，所有方法可并发调用。
type Manager struct {
	mu          sync.Mutex
	numBlocks   int
	blockSize   int
	maxStored   int // <=0 表示不限
	volume      [][]byte
	chain       []*Backup
	nextID      int
	sessionRefs map[int]int // 备份 ID -> 引用它的未关闭会话数
	logf        func(format string, args ...any)
}

// NewManager 创建管理器。maxStoredBlocks<=0 表示不限制存储块总数。
func NewManager(numBlocks, blockSize, maxStoredBlocks int) (*Manager, error) {
	if numBlocks <= 0 || blockSize <= 0 {
		return nil, fmt.Errorf("backup: 非法的卷参数 numBlocks=%d blockSize=%d", numBlocks, blockSize)
	}
	m := &Manager{
		numBlocks:   numBlocks,
		blockSize:   blockSize,
		maxStored:   maxStoredBlocks,
		volume:      make([][]byte, numBlocks),
		sessionRefs: make(map[int]int),
	}
	for i := range m.volume {
		m.volume[i] = make([]byte, blockSize)
	}
	return m, nil
}

// SetLogger 设置决策日志函数（打印输入、输出与判定依据），传 nil 关闭。
func (m *Manager) SetLogger(logf func(format string, args ...any)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.logf = logf
}

func (m *Manager) log(format string, args ...any) {
	if m.logf != nil {
		m.logf(format, args...)
	}
}

func (m *Manager) checkBlock(block int) error {
	if block < 0 || block >= m.numBlocks {
		return fmt.Errorf("%w: block=%d numBlocks=%d", ErrBlockOutOfRange, block, m.numBlocks)
	}
	return nil
}

func (m *Manager) indexOfLocked(id int) int {
	for i, b := range m.chain {
		if b.ID == id {
			return i
		}
	}
	return -1
}

// restoreResultLocked 计算 chain[idx] 的还原结果：
// 每块取从它回溯到链首遇到的第一份内容。
func (m *Manager) restoreResultLocked(idx int) [][]byte {
	res := make([][]byte, m.numBlocks)
	for i := 0; i <= idx; i++ {
		for block, data := range m.chain[i].Blocks {
			res[block] = data
		}
	}
	return res
}

func (m *Manager) storedLocked() int {
	total := 0
	for _, b := range m.chain {
		total += len(b.Blocks)
	}
	return total
}

func cloneBlock(data []byte) []byte {
	out := make([]byte, len(data))
	copy(out, data)
	return out
}

// Write 写入卷的一个块。
func (m *Manager) Write(block int, data []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.checkBlock(block); err != nil {
		m.log("write 拒绝: block=%d 原因=%v", block, err)
		return err
	}
	if len(data) != m.blockSize {
		err := fmt.Errorf("%w: len=%d blockSize=%d", ErrInvalidBlockSize, len(data), m.blockSize)
		m.log("write 拒绝: block=%d 原因=%v", block, err)
		return err
	}
	copy(m.volume[block], data)
	m.log("write 接受: block=%d data=%x", block, data)
	return nil
}

// ReadVolume 读取卷当前内容的一个块（返回副本）。
func (m *Manager) ReadVolume(block int) ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.checkBlock(block); err != nil {
		return nil, err
	}
	return cloneBlock(m.volume[block]), nil
}

// Backup 创建备份，返回备份 ID。full 为 true 时做全量备份，
// 否则以链尾为父做增量备份。整个备份在锁内完成，
// 并发写入要么全部落在该备份内、要么全部不在。
func (m *Manager) Backup(full bool) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.chain) == 0 && !full {
		m.log("backup 拒绝: full=%v 原因=%v", full, ErrChainEmpty)
		return 0, ErrChainEmpty
	}
	blocks := make(map[int][]byte)
	if full {
		for i := 0; i < m.numBlocks; i++ {
			blocks[i] = cloneBlock(m.volume[i])
		}
		m.log("backup 全量: 存入全部 %d 块", m.numBlocks)
	} else {
		parent := m.chain[len(m.chain)-1]
		parentRes := m.restoreResultLocked(len(m.chain) - 1)
		for i := 0; i < m.numBlocks; i++ {
			if bytesEqual(m.volume[i], parentRes[i]) {
				m.log("backup 增量: block=%d 与父备份 %d 还原结果相同, 不存", i, parent.ID)
				continue
			}
			blocks[i] = cloneBlock(m.volume[i])
			m.log("backup 增量: block=%d 与父备份 %d 还原结果不同, 存入", i, parent.ID)
		}
	}
	if m.maxStored > 0 && m.storedLocked()+len(blocks) > m.maxStored {
		err := fmt.Errorf("%w: 当前=%d 新增=%d 上限=%d",
			ErrStorageLimitExceeded, m.storedLocked(), len(blocks), m.maxStored)
		m.log("backup 拒绝: %v", err)
		return 0, err
	}
	m.nextID++
	b := &Backup{ID: m.nextID, Full: full, Blocks: blocks}
	m.chain = append(m.chain, b)
	m.log("backup 接受: id=%d full=%v 存块数=%d 存储总量=%d",
		b.ID, full, len(blocks), m.storedLocked())
	return b.ID, nil
}

func bytesEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// Delete 删除备份。约束：目标备份不得处于任何未关闭还原会话的回溯路径上。
//   - 链尾：直接丢弃；
//   - 链首（全量）：直接后继变为全量，内容为后继还原结果的全部块；
//   - 中间备份：其块并入直接后继（后继已有的块以后继为准），
//     并入后后继中与新父还原结果相同的块被剔除。
//
// 任一失败都不改变链与卷。
func (m *Manager) Delete(id int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	idx := m.indexOfLocked(id)
	if idx < 0 {
		m.log("delete 拒绝: id=%d 原因=%v", id, ErrBackupNotFound)
		return fmt.Errorf("%w: id=%d", ErrBackupNotFound, id)
	}
	if m.sessionRefs[id] > 0 {
		err := fmt.Errorf("%w: id=%d 引用会话数=%d", ErrBackupInUse, id, m.sessionRefs[id])
		m.log("delete 拒绝: %v", err)
		return err
	}
	target := m.chain[idx]
	if idx == len(m.chain)-1 {
		m.chain = m.chain[:idx]
		m.log("delete 链尾: id=%d 直接丢弃, 存储总量=%d", id, m.storedLocked())
		return nil
	}
	succ := m.chain[idx+1]
	if idx == 0 {
		// 删除全量：后继变为全量，内容为后继还原结果的全部块。
		res := m.restoreResultLocked(1)
		merged := make(map[int][]byte, m.numBlocks)
		for block, data := range res {
			merged[block] = cloneBlock(data)
		}
		newTotal := m.storedLocked() - len(target.Blocks) - len(succ.Blocks) + len(merged)
		if m.maxStored > 0 && newTotal > m.maxStored {
			err := fmt.Errorf("%w: 删除全量后=%d 上限=%d", ErrStorageLimitExceeded, newTotal, m.maxStored)
			m.log("delete 拒绝: %v", err)
			return err
		}
		succ.Blocks = merged
		succ.Full = true
		m.chain = m.chain[1:]
		m.log("delete 全量: id=%d, 后继 id=%d 变为全量(存块数=%d), 存储总量=%d",
			id, succ.ID, len(merged), m.storedLocked())
		return nil
	}
	// 中间备份：并入后继，后继优先；再剔除与新父还原结果相同的块。
	parentRes := m.restoreResultLocked(idx - 1)
	merged := make(map[int][]byte, len(succ.Blocks)+len(target.Blocks))
	for block, data := range succ.Blocks {
		merged[block] = data
	}
	for block, data := range target.Blocks {
		if _, ok := merged[block]; ok {
			m.log("delete 合并: block=%d 后继 id=%d 已有, 以后继为准", block, succ.ID)
			continue
		}
		merged[block] = data
		m.log("delete 合并: block=%d 由被删备份 id=%d 并入后继 id=%d", block, id, succ.ID)
	}
	for block, data := range merged {
		if bytesEqual(data, parentRes[block]) {
			delete(merged, block)
			m.log("delete 剔除: block=%d 与新父 id=%d 还原结果相同", block, m.chain[idx-1].ID)
		}
	}
	succ.Blocks = merged
	m.chain = append(m.chain[:idx], m.chain[idx+1:]...)
	m.log("delete 中间备份: id=%d 并入后继 id=%d(存块数=%d), 存储总量=%d",
		id, succ.ID, len(merged), m.storedLocked())
	return nil
}

// OpenRestore 打开一个还原会话。会话未关闭期间，
// 其回溯路径（目标备份到链首）上的备份不可删除。
func (m *Manager) OpenRestore(id int) (*RestoreSession, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	idx := m.indexOfLocked(id)
	if idx < 0 {
		m.log("open-restore 拒绝: id=%d 原因=%v", id, ErrBackupNotFound)
		return nil, fmt.Errorf("%w: id=%d", ErrBackupNotFound, id)
	}
	path := make([]int, 0, idx+1)
	for i := 0; i <= idx; i++ {
		path = append(path, m.chain[i].ID)
		m.sessionRefs[m.chain[i].ID]++
	}
	m.log("open-restore 接受: id=%d 回溯路径=%v", id, path)
	return &RestoreSession{m: m, backupID: id, path: path}, nil
}

// Restore 便捷方法：还原整个备份并返回全部块内容。
func (m *Manager) Restore(id int) ([][]byte, error) {
	s, err := m.OpenRestore(id)
	if err != nil {
		return nil, err
	}
	defer s.Close()
	m.mu.Lock()
	defer m.mu.Unlock()
	res := m.restoreResultLocked(m.indexOfLocked(id))
	out := make([][]byte, m.numBlocks)
	for i := range res {
		out[i] = cloneBlock(res[i])
	}
	return out, nil
}

// Blocks 返回某备份实际存储的块（深拷贝），用于校验“只存必需的块”。
func (m *Manager) Blocks(id int) (map[int][]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	idx := m.indexOfLocked(id)
	if idx < 0 {
		return nil, fmt.Errorf("%w: id=%d", ErrBackupNotFound, id)
	}
	out := make(map[int][]byte, len(m.chain[idx].Blocks))
	for block, data := range m.chain[idx].Blocks {
		out[block] = cloneBlock(data)
	}
	return out, nil
}

// Chain 返回链上各备份的概要（从链首到链尾）。
func (m *Manager) Chain() []BackupInfo {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]BackupInfo, 0, len(m.chain))
	for _, b := range m.chain {
		out = append(out, BackupInfo{ID: b.ID, Full: b.Full, StoredBlocks: len(b.Blocks)})
	}
	return out
}

// StoredBlockCount 返回当前所有备份存储的块总数。
func (m *Manager) StoredBlockCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.storedLocked()
}

// RestoreSession 是一个打开的还原会话。
type RestoreSession struct {
	m        *Manager
	backupID int
	path     []int
	closed   bool
}

// Read 读取会话对应备份还原结果中的一个块：
// 从该备份回溯到链首，取遇到的第一份内容。
func (s *RestoreSession) Read(block int) ([]byte, error) {
	s.m.mu.Lock()
	defer s.m.mu.Unlock()
	if s.closed {
		return nil, ErrSessionClosed
	}
	if err := s.m.checkBlock(block); err != nil {
		return nil, err
	}
	for i := s.m.indexOfLocked(s.backupID); i >= 0; i-- {
		if data, ok := s.m.chain[i].Blocks[block]; ok {
			return cloneBlock(data), nil
		}
	}
	return nil, fmt.Errorf("backup: 块 %d 在回溯路径上无内容", block)
}

// Close 关闭会话，解除对回溯路径上备份的删除约束。幂等。
func (s *RestoreSession) Close() error {
	s.m.mu.Lock()
	defer s.m.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	for _, id := range s.path {
		s.m.sessionRefs[id]--
		if s.m.sessionRefs[id] == 0 {
			delete(s.m.sessionRefs, id)
		}
	}
	s.m.log("close-restore: id=%d 回溯路径=%v 已解除", s.backupID, s.path)
	return nil
}

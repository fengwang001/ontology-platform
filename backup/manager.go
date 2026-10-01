// Package backup 实现块级增量备份链管理器。
//
// 卷由定长块组成。备份按时间顺序构成一条链：链首为全量备份，
// 其余为增量备份，父为链上紧邻的前一个备份。
package backup

import (
	"bytes"
	"errors"
	"sync"
)

// 可区分的拒绝原因。
var (
	// ErrBlockOutOfRange 块号越界或数据长度与块长不符。
	ErrBlockOutOfRange = errors.New("backup: block index out of range")
	// ErrBackupNotFound 指定的备份不存在。
	ErrBackupNotFound = errors.New("backup: backup not found")
	// ErrStorageLimitExceeded 存储块总数将超过上限。
	ErrStorageLimitExceeded = errors.New("backup: stored block count would exceed limit")
	// ErrDeleteBlockedByRestore 目标备份处于某个未关闭还原会话的回溯路径上。
	ErrDeleteBlockedByRestore = errors.New("backup: delete blocked by an open restore session")
	// ErrSessionClosed 会话已关闭。
	ErrSessionClosed = errors.New("backup: restore session closed")
)

// Backup 是链上的一个备份。
type Backup struct {
	ID     int
	Full   bool
	blocks map[int][]byte
	parent *Backup
	next   *Backup
}

// BlockCount 返回该备份实际存储的块数。
func (b *Backup) BlockCount() int { return len(b.blocks) }

// HasBlock 报告该备份是否直接存储了块 blockNo 的内容。
func (b *Backup) HasBlock(blockNo int) bool {
	_, ok := b.blocks[blockNo]
	return ok
}

// RestoreSession 是一次打开的还原会话。
// 会话关闭前，其回溯路径（目标备份到链首）上的备份不可删除。
type RestoreSession struct {
	m      *Manager
	target *Backup
	closed bool
}

// Manager 管理一个卷及其备份链。所有方法可并发调用。
type Manager struct {
	mu       sync.RWMutex
	volume   [][]byte
	numBlks  int
	blkSize  int
	maxStore int // 存储块总数上限，<=0 表示不限
	head     *Backup
	tail     *Backup
	chainLen int
	nextID   int
	sessions map[*RestoreSession]struct{}
}

// NewManager 创建管理器。numBlocks 为卷块数，blockSize 为块长（字节），
// maxStoredBlocks 为全链存储块总数上限（<=0 表示不限）。
func NewManager(numBlocks, blockSize, maxStoredBlocks int) *Manager {
	volume := make([][]byte, numBlocks)
	for i := range volume {
		volume[i] = make([]byte, blockSize)
	}
	return &Manager{
		volume:   volume,
		numBlks:  numBlocks,
		blkSize:  blockSize,
		maxStore: maxStoredBlocks,
		nextID:   1,
		sessions: make(map[*RestoreSession]struct{}),
	}
}

// Write 将 data 写入卷的第 blockNo 块。data 长度必须等于块长。
func (m *Manager) Write(blockNo int, data []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.checkBlock(blockNo, data); err != nil {
		return err
	}
	blk := make([]byte, m.blkSize)
	copy(blk, data)
	m.volume[blockNo] = blk
	return nil
}

func (m *Manager) checkBlock(blockNo int, data []byte) error {
	if blockNo < 0 || blockNo >= m.numBlks || (data != nil && len(data) != m.blkSize) {
		return ErrBlockOutOfRange
	}
	return nil
}

// ReadBlock 返回卷第 blockNo 块的当前内容副本。
func (m *Manager) ReadBlock(blockNo int) ([]byte, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if blockNo < 0 || blockNo >= m.numBlks {
		return nil, ErrBlockOutOfRange
	}
	return append([]byte(nil), m.volume[blockNo]...), nil
}

// Backup 创建一个新备份并返回其 ID。
// 链为空时创建全量备份，否则创建增量备份。
//
// 备份是时间点一致的：整个快照在互斥锁内完成，
// 并发写入要么全部落在该备份内、要么全部不在。
// 增量备份只存与父备份还原结果不同的块；
// 写过又写回原值的块与父还原结果相同，因此不存。
func (m *Manager) Backup() (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	b := &Backup{ID: m.nextID, blocks: make(map[int][]byte)}
	if m.tail == nil {
		b.Full = true
		for i := 0; i < m.numBlks; i++ {
			b.blocks[i] = append([]byte(nil), m.volume[i]...)
		}
	} else {
		b.parent = m.tail
		for i := 0; i < m.numBlks; i++ {
			if !bytes.Equal(m.volume[i], restoreBlock(m.tail, i)) {
				b.blocks[i] = append([]byte(nil), m.volume[i]...)
			}
		}
	}
	if m.maxStore > 0 && m.storedBlocksLocked()+len(b.blocks) > m.maxStore {
		return 0, ErrStorageLimitExceeded
	}
	m.nextID++
	if m.tail == nil {
		m.head = b
	} else {
		m.tail.next = b
	}
	m.tail = b
	m.chainLen++
	return b.ID, nil
}

// restoreBlock 从 b 回溯到链首，返回遇到的第一份块 blockNo 的内容。
// 调用方须持有锁。链首为全量，保证必然命中。
func restoreBlock(b *Backup, blockNo int) []byte {
	for cur := b; cur != nil; cur = cur.parent {
		if data, ok := cur.blocks[blockNo]; ok {
			return data
		}
	}
	return nil
}

// Delete 删除指定备份，并按规则把内容并入直接后继。
//
// 规则：
//   - 删除链尾：直接丢弃；
//   - 删除中间备份：其块并入直接后继，后继已有的块以后继为准；
//     并入后剔除后继中与新父还原结果相同的块；
//   - 删除全量（链首）：后继变为全量，不再剔除；
//   - 目标处于某个未关闭还原会话的回溯路径上时整体拒绝。
//
// 合并只迁移或剔除块，全链存储块总数不增，无需再校验上限。
func (m *Manager) Delete(id int) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	var target, prev *Backup
	for cur := m.head; cur != nil; cur = cur.next {
		if cur.ID == id {
			target = cur
			break
		}
		prev = cur
	}
	if target == nil {
		return ErrBackupNotFound
	}
	if m.blockedBySessionLocked(target) {
		return ErrDeleteBlockedByRestore
	}

	succ := target.next
	if succ == nil { // 链尾：直接丢弃
		if prev == nil {
			m.head, m.tail = nil, nil
		} else {
			prev.next = nil
			m.tail = prev
		}
		m.chainLen--
		return nil
	}

	// 并入：后继已有的块以后继为准。
	for blk, data := range target.blocks {
		if _, ok := succ.blocks[blk]; !ok {
			succ.blocks[blk] = data
		}
	}
	succ.parent = target.parent
	if target.parent == nil { // 删除的是全量：后继变全量，保留全部块
		succ.Full = true
		m.head = succ
	} else {
		// 剔除后继中与新父还原结果相同的块。
		for blk, data := range succ.blocks {
			if bytes.Equal(data, restoreBlock(succ.parent, blk)) {
				delete(succ.blocks, blk)
			}
		}
	}
	m.chainLen--
	return nil
}

// blockedBySessionLocked 报告 target 是否处于某个未关闭会话的回溯路径上。
func (m *Manager) blockedBySessionLocked(target *Backup) bool {
	for s := range m.sessions {
		for cur := s.target; cur != nil; cur = cur.parent {
			if cur == target {
				return true
			}
		}
	}
	return false
}

func (m *Manager) findLocked(id int) *Backup {
	for cur := m.head; cur != nil; cur = cur.next {
		if cur.ID == id {
			return cur
		}
	}
	return nil
}

// OpenRestore 打开一个指向指定备份的还原会话。
func (m *Manager) OpenRestore(id int) (*RestoreSession, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	b := m.findLocked(id)
	if b == nil {
		return nil, ErrBackupNotFound
	}
	s := &RestoreSession{m: m, target: b}
	m.sessions[s] = struct{}{}
	return s, nil
}

// Restore 便捷方法：直接还原指定备份的整卷内容。
func (m *Manager) Restore(id int) ([]byte, error) {
	s, err := m.OpenRestore(id)
	if err != nil {
		return nil, err
	}
	defer s.Close()
	return s.ReadAll()
}

// Backups 按链序返回所有保留备份。
func (m *Manager) Backups() []*Backup {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]*Backup, 0, m.chainLen)
	for cur := m.head; cur != nil; cur = cur.next {
		out = append(out, cur)
	}
	return out
}

// StoredBlockCount 返回全链当前实际存储的块总数。
func (m *Manager) StoredBlockCount() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.storedBlocksLocked()
}

func (m *Manager) storedBlocksLocked() int {
	total := 0
	for cur := m.head; cur != nil; cur = cur.next {
		total += len(cur.blocks)
	}
	return total
}

// ReadBlock 在会话内读取目标备份还原结果的第 blockNo 块。
func (s *RestoreSession) ReadBlock(blockNo int) ([]byte, error) {
	m := s.m
	m.mu.RLock()
	defer m.mu.RUnlock()
	if s.closed {
		return nil, ErrSessionClosed
	}
	if blockNo < 0 || blockNo >= m.numBlks {
		return nil, ErrBlockOutOfRange
	}
	return append([]byte(nil), restoreBlock(s.target, blockNo)...), nil
}

// ReadAll 在会话内读取目标备份的整卷还原结果。
func (s *RestoreSession) ReadAll() ([]byte, error) {
	m := s.m
	m.mu.RLock()
	defer m.mu.RUnlock()
	if s.closed {
		return nil, ErrSessionClosed
	}
	out := make([]byte, 0, m.numBlks*m.blkSize)
	for i := 0; i < m.numBlks; i++ {
		out = append(out, restoreBlock(s.target, i)...)
	}
	return out, nil
}

// Close 关闭会话。关闭后会话不可再读，且不再阻塞删除。
func (s *RestoreSession) Close() error {
	m := s.m
	m.mu.Lock()
	defer m.mu.Unlock()
	if s.closed {
		return ErrSessionClosed
	}
	s.closed = true
	delete(m.sessions, s)
	return nil
}

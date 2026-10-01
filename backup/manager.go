package backup

import (
	"bytes"
	"fmt"
	"io"
	"log"
	"os"
	"sort"
	"sync"
)

// bk 表示链上的一份备份。blocks 是该备份自身存储的块内容；
// 未出现的块号在还原时继续向父备份回溯。full 为 true 时该备份
// 不再依赖任何祖先（链首或删除原全量后由后继升格而来）。
type bk struct {
	id     string
	full   bool
	blocks map[int][]byte
}

// Manager 管理一个定长块卷的全量/增量备份链。
// 所有方法均可被并发调用；链的所有结构变更在同一把锁内完成，
// 因此并发块写入相对于一次备份要么全部可见、要么全部不可见。
type Manager struct {
	mu       sync.Mutex
	blocks   int
	size     int
	maxStore int
	volume   [][]byte
	chain    []*bk
	byID     map[string]*bk
	sessions map[*Session]struct{}
	log      *log.Logger
}

// New 创建一个包含 blockCount 个块、每块 blockSize 字节的卷管理器。
// 卷初始时每块均为全 0；存储的备份块总数不得超过 maxStoredBlocks。
// logger 为 nil 时日志默认写入 stderr，传入 io.Discard 可关闭日志。
func New(blockCount int, blockSize int, maxStoredBlocks int, logger io.Writer) *Manager {
	if blockCount <= 0 || blockSize <= 0 || maxStoredBlocks < 0 {
		panic("backup: invalid manager parameters")
	}
	if logger == nil {
		logger = os.Stderr
	}
	m := &Manager{
		blocks:   blockCount,
		size:     blockSize,
		maxStore: maxStoredBlocks,
		volume:   make([][]byte, blockCount),
		byID:     make(map[string]*bk),
		sessions: make(map[*Session]struct{}),
		log:      log.New(logger, "[backup] ", log.LstdFlags|log.Lmicroseconds),
	}
	zero := make([]byte, blockSize)
	for i := range m.volume {
		m.volume[i] = append([]byte(nil), zero...)
	}
	return m
}

// WriteBlock 覆盖卷中第 index 块的内容，返回写入前的旧内容。
func (m *Manager) WriteBlock(index int, data []byte) ([]byte, error) {
	if index < 0 || index >= m.blocks {
		m.log.Printf("WriteBlock 输入: index=%d len(data)=%d -> 输出: 拒绝, 判定依据: 块号越界(合法范围[0,%d))",
			index, len(data), m.blocks)
		return nil, ErrBlockIndexOutOfRange
	}
	if len(data) != m.size {
		m.log.Printf("WriteBlock 输入: index=%d len(data)=%d -> 输出: 拒绝, 判定依据: 块长度不等于块大小%d",
			index, len(data), m.size)
		return nil, ErrBlockSizeMismatch
	}
	m.mu.Lock()
	old := append([]byte(nil), m.volume[index]...)
	m.volume[index] = append([]byte(nil), data...)
	m.mu.Unlock()
	m.log.Printf("WriteBlock 输入: index=%d len(data)=%d -> 输出: 成功, 判定依据: 持锁原子覆盖, 旧内容与新内容不同=%v",
		index, len(data), !bytes.Equal(old, data))
	return old, nil
}

// Backup 创建一个时间点一致的备份。链上第一个备份为全量，
// 其余为相对紧邻前驱还原结果的增量；返回新备份 ID 与实际存储的块号集合。
func (m *Manager) Backup() (id string, stored []int, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	full := len(m.chain) == 0
	newBlocks := make(map[int][]byte, m.blocks)
	if full {
		for i := 0; i < m.blocks; i++ {
			newBlocks[i] = append([]byte(nil), m.volume[i]...)
		}
		m.log.Printf("Backup 输入: 无 -> 判定依据: 链为空, 本次为全量备份")
	} else {
		parent := m.chain[len(m.chain)-1]
		for i := 0; i < m.blocks; i++ {
			cur := m.volume[i]
			prev := m.resolveLocked(parent, i)
			if !bytes.Equal(cur, prev) {
				newBlocks[i] = append([]byte(nil), cur...)
			}
		}
		m.log.Printf("Backup 输入: 无 -> 判定依据: 父备份=%s, 逐块与其还原结果比较, %d 块不同需存储, 写回原值的块不存",
			parent.id, len(newBlocks))
	}

	if total := m.storedLocked() + len(newBlocks); total > m.maxStore {
		m.log.Printf("Backup 输出: 拒绝(%v), 判定依据: 新增%d块后总数%d将超过上限%d, 链与卷不变",
			ErrStorageLimitExceeded, len(newBlocks), total, m.maxStore)
		return "", nil, ErrStorageLimitExceeded
	}

	id = fmt.Sprintf("B%d", len(m.chain)+1)
	// ID 以链位置生成，重名在正常流程下不可能出现，仍做防御性处理。
	if _, exists := m.byID[id]; exists {
		id = fmt.Sprintf("B%d-%d", len(m.chain)+1, len(m.byID)+1)
	}
	b := &bk{id: id, full: full, blocks: newBlocks}
	m.chain = append(m.chain, b)
	m.byID[id] = b
	stored = sortedKeys(newBlocks)
	m.log.Printf("Backup 输出: 成功 id=%s full=%v 存储块=%v 存储总数=%d/%d, 判定依据: 备份在锁内对卷做时间点快照",
		id, full, stored, m.storedLocked(), m.maxStore)
	return id, stored, nil
}

// OpenRestore 打开指向某备份还原结果的会话，会话期间该备份及其
// 所有祖先备份不可被删除。
func (m *Manager) OpenRestore(id string) (*Session, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	b, ok := m.byID[id]
	if !ok {
		m.log.Printf("OpenRestore 输入: id=%q -> 输出: 拒绝(%v), 判定依据: 备份不存在", id, ErrBackupNotFound)
		return nil, ErrBackupNotFound
	}
	s := &Session{m: m, target: b}
	m.sessions[s] = struct{}{}
	m.log.Printf("OpenRestore 输入: id=%q -> 输出: 成功, 判定依据: 会话保护回溯路径 %v",
		id, m.protectedIDsLocked())
	return s, nil
}

// Delete 删除备份。中间备份并入直接后继，链尾直接丢弃。
func (m *Manager) Delete(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	idx := -1
	for i, b := range m.chain {
		if b.id == id {
			idx = i
			break
		}
	}
	if idx < 0 {
		m.log.Printf("Delete 输入: id=%q -> 输出: 拒绝(%v), 判定依据: 备份不存在", id, ErrBackupNotFound)
		return ErrBackupNotFound
	}
	target := m.chain[idx]
	protected := m.protectedIDsLocked()
	for _, pid := range protected {
		if pid == id {
			m.log.Printf("Delete 输入: id=%q -> 输出: 拒绝(%v), 判定依据: 该备份处于未关闭还原会话的回溯路径%v上, 全部状态不变",
				id, ErrBackupInRestorePath, protected)
			return ErrBackupInRestorePath
		}
	}

	if idx == len(m.chain)-1 {
		m.chain = m.chain[:idx]
		delete(m.byID, id)
		m.log.Printf("Delete 输入: id=%q -> 输出: 成功, 判定依据: 链尾备份直接丢弃, 其余备份未改动, 存储总数=%d",
			id, m.storedLocked())
		return nil
	}

	suc := m.chain[idx+1]
	// 1) 被删备份的块并入直接后继；后继已有的同号块以后继为准，
	//    因此只补入后继缺失的块。
	mergedIn := 0
	for i, data := range target.blocks {
		if _, ok := suc.blocks[i]; !ok {
			suc.blocks[i] = append([]byte(nil), data...)
			mergedIn++
		}
	}
	// 2) 被删的是全量（链首）时，后继升格为新全量并保留全部块。
	if target.full {
		suc.full = true
		m.chain = append(m.chain[:idx], m.chain[idx+1:]...)
		delete(m.byID, id)
		m.log.Printf("Delete 输入: id=%q -> 输出: 成功, 判定依据: 删除全量链首, 后继=%s 补入%d块后升格为全量, 无需剔除, 存储总数=%d",
			id, suc.id, mergedIn, m.storedLocked())
		return nil
	}
	// 3) 增量中间备份：以新父（被删者的原父）还原结果为基准，
	//    剔除后继中与新父还原结果相同的块。
	newParent := m.chain[idx-1]
	pruned := 0
	for i, data := range suc.blocks {
		if bytes.Equal(data, m.resolveLocked(newParent, i)) {
			delete(suc.blocks, i)
			pruned++
		}
	}
	m.chain = append(m.chain[:idx], m.chain[idx+1:]...)
	delete(m.byID, id)
	m.log.Printf("Delete 输入: id=%q -> 输出: 成功, 判定依据: 块并入后继=%s(补入%d块, 后继已有块保留), 再按新父=%s剔除%d个相同块, 后继存储块=%v, 存储总数=%d, 保留备份还原结果逐块不变",
		id, suc.id, mergedIn, newParent.id, pruned, sortedKeys(suc.blocks), m.storedLocked())
	return nil
}

// StoredBlocks 返回某备份自身存储的块号集合（升序）。
func (m *Manager) StoredBlocks(id string) ([]int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	b, ok := m.byID[id]
	if !ok {
		return nil, ErrBackupNotFound
	}
	return sortedKeys(b.blocks), nil
}

// IsFull 判断某备份是否为全量备份。
func (m *Manager) IsFull(id string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	b, ok := m.byID[id]
	if !ok {
		return false, ErrBackupNotFound
	}
	return b.full, nil
}

// Backups 按链首到链尾的顺序返回全部备份 ID。
func (m *Manager) Backups() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	ids := make([]string, len(m.chain))
	for i, b := range m.chain {
		ids[i] = b.id
	}
	return ids
}

// StoredBlockCount 返回当前所有备份存储块的总数。
func (m *Manager) StoredBlockCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.storedLocked()
}

// storedLocked 返回存储块总数；调用方必须持有 m.mu。
func (m *Manager) storedLocked() int {
	total := 0
	for _, b := range m.chain {
		total += len(b.blocks)
	}
	return total
}

// resolveLocked 取备份 b 的还原结果中第 i 块：沿父链回溯，
// 命中第一份存有该块的备份即返回；全量备份一定终结回溯。
// 调用方必须持有 m.mu。
func (m *Manager) resolveLocked(b *bk, i int) []byte {
	for cur := b; cur != nil; {
		if data, ok := cur.blocks[i]; ok {
			return data
		}
		if cur.full {
			return m.zeroBlock()
		}
		cur = m.parentLocked(cur)
	}
	return m.zeroBlock()
}

// parentLocked 返回链上紧邻前驱；调用方必须持有 m.mu。
func (m *Manager) parentLocked(b *bk) *bk {
	for i := 1; i < len(m.chain); i++ {
		if m.chain[i] == b {
			return m.chain[i-1]
		}
	}
	return nil
}

// protectedIDsLocked 返回当前所有打开会话正在保护的备份 ID 集合，
// 即每个会话目标备份沿父链直到链首的整条回溯路径。
func (m *Manager) protectedIDsLocked() []string {
	protected := map[string]struct{}{}
	for s := range m.sessions {
		for cur := s.target; cur != nil; cur = m.parentLocked(cur) {
			protected[cur.id] = struct{}{}
		}
	}
	ids := make([]string, 0, len(protected))
	for id := range protected {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

func (m *Manager) zeroBlock() []byte {
	z := make([]byte, m.size)
	return z
}

func sortedKeys(blocks map[int][]byte) []int {
	keys := make([]int, 0, len(blocks))
	for k := range blocks {
		keys = append(keys, k)
	}
	sort.Ints(keys)
	return keys
}

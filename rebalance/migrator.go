package rebalance

import (
	"sort"
	"sync"
)

// Migrator 是一个内存中的一致性哈希分区存储，支持分区数变化时的
// 在线重均衡。所有公开方法均可被并发调用。
type Migrator struct {
	mu sync.RWMutex

	// n 是当前已提交生效的分区数。
	n int
	// parts 按分区 ID 索引；迁移期长度为 max(fromN,toN)，
	// 非迁移期长度为 n。
	parts []map[string]string

	// 迁移态：仅当 migrating==true 时其余字段有效。
	migrating bool
	fromN     int
	toN       int
	// plan 是归属会发生改变的键，按字典序排列，保证可复现。
	plan []string
	// planIndex 记录每个键在 plan 中的位置，用于游标路由判定。
	planIndex map[string]int
	// cursor 是已迁移条数：plan[:cursor] 已在新归属，其余在旧归属。
	cursor int
}

// New 创建一个初始分区数为 partitionCount 的存储。
func New(partitionCount int) (*Migrator, error) {
	if partitionCount < 1 {
		return nil, ErrInvalidPartitionCount
	}
	parts := make([]map[string]string, partitionCount)
	for i := range parts {
		parts[i] = make(map[string]string)
	}
	return &Migrator{n: partitionCount, parts: parts}, nil
}

// PartitionCount 返回当前已提交的分区数（迁移期仍是旧分区数）。
func (m *Migrator) PartitionCount() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.n
}

// StartRebalance 开始一次从当前分区数到 targetCount 的重均衡：
// 计算归属改变的记录清单并按键排序，游标清零。
func (m *Migrator) StartRebalance(targetCount int) error {
	if targetCount < 1 {
		return ErrInvalidPartitionCount
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.migrating {
		return ErrRebalanceInProgress
	}

	fromN := m.n
	plan := make([]string, 0)
	for p := 0; p < fromN; p++ {
		for k := range m.parts[p] {
			if ownerOf(k, targetCount) != p {
				plan = append(plan, k)
			}
		}
	}
	sort.Strings(plan)

	if need := maxInt(fromN, targetCount); len(m.parts) < need {
		grown := make([]map[string]string, need)
		copy(grown, m.parts)
		for i := len(m.parts); i < need; i++ {
			grown[i] = make(map[string]string)
		}
		m.parts = grown
	}
	index := make(map[string]int, len(plan))
	for i, k := range plan {
		index[k] = i
	}
	m.migrating = true
	m.fromN = fromN
	m.toN = targetCount
	m.plan = plan
	m.planIndex = index
	m.cursor = 0
	return nil
}

// Step 恰好迁移清单中的下一条记录：先复制到新归属，再从旧归属删除，
// 然后游标加一。清单已全部迁完时返回 (false, nil)。
func (m *Migrator) Step() (moved bool, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.migrating {
		return false, ErrNotMigrating
	}
	if m.cursor >= len(m.plan) {
		return false, nil
	}
	key := m.plan[m.cursor]
	oldP := ownerOf(key, m.fromN)
	newP := ownerOf(key, m.toN)
	value, ok := m.parts[oldP][key]
	if !ok {
		// 不变量被破坏：记录不在其旧归属，整体不做任何改动。
		return false, ErrMigrationIncomplete
	}
	m.parts[newP][key] = value
	delete(m.parts[oldP], key)
	m.cursor++
	return true, nil
}

// Commit 仅在清单全部迁完后提交，切换生效分区数并清理迁移态。
func (m *Migrator) Commit() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.migrating {
		return ErrNotMigrating
	}
	if m.cursor < len(m.plan) {
		return ErrMigrationIncomplete
	}

	toN := m.toN
	next := make([]map[string]string, toN)
	for p := 0; p < toN; p++ {
		next[p] = m.parts[p]
	}
	// 缩容时，旧的高位分区在迁移完成后必须已空。
	for p := toN; p < len(m.parts); p++ {
		if len(m.parts[p]) != 0 {
			return ErrMigrationIncomplete
		}
	}
	m.parts = next
	m.n = toN
	m.migrating = false
	m.fromN = 0
	m.toN = 0
	m.plan = nil
	m.planIndex = nil
	m.cursor = 0
	return nil
}

// Get 读取一个键。迁移期按游标路由到它的当前所在分区：
// 已迁移的键在新归属，未迁移的键在旧归属。
func (m *Migrator) Get(key string) (string, bool, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	p := m.currentOwnerLocked(key)
	v, ok := m.parts[p][key]
	return v, ok, nil
}

// Put 写入一个键。非迁移期按当前分区数归属；迁移期只允许更新
// 已存在的键，并写入其当前所在分区；全新键一律整体拒绝。
func (m *Migrator) Put(key, value string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.migrating {
		p := m.currentOwnerLocked(key)
		if _, ok := m.parts[p][key]; !ok {
			return ErrNewKeyDuringMigration
		}
		m.parts[p][key] = value
		return nil
	}
	p := ownerOf(key, m.n)
	m.parts[p][key] = value
	return nil
}

// Progress 返回迁移进度；游标单调不减。
func (m *Migrator) Progress() Progress {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return Progress{
		Migrating: m.migrating,
		From:      m.fromN,
		To:        m.toN,
		Cursor:    m.cursor,
		Total:     len(m.plan),
	}
}

// CurrentOwner 返回键当前所在分区（迁移期由游标判定）。
func (m *Migrator) CurrentOwner(key string) int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.currentOwnerLocked(key)
}

// OwnerOf 导出确定性归属规则 ownerOf(key, partitionCount)，
// 供外部重算归属、核对记录是否各居其位。
func OwnerOf(key string, partitionCount int) int {
	return ownerOf(key, partitionCount)
}

// currentOwnerLocked 的判定依据：
//   - 非迁移期：hash(key) mod n；
//   - 迁移期且键在清单中：plan 下标 < cursor 表示已迁移 -> 新归属，
//     否则仍在旧归属；
//   - 不在清单中的键旧新归属相同，取旧归属即可。
func (m *Migrator) currentOwnerLocked(key string) int {
	if !m.migrating {
		return ownerOf(key, m.n)
	}
	if idx, ok := m.planIndex[key]; ok && idx < m.cursor {
		return ownerOf(key, m.toN)
	}
	return ownerOf(key, m.fromN)
}

// Dump 返回状态的深拷贝快照，用于日志打印与核对。
func (m *Migrator) Dump() StateDump {
	m.mu.RLock()
	defer m.mu.RUnlock()
	limit := m.n
	if m.migrating {
		limit = maxInt(m.fromN, m.toN)
	}
	dumpParts := make(map[int]map[string]string, limit)
	for p := 0; p < limit; p++ {
		cp := make(map[string]string, len(m.parts[p]))
		for k, v := range m.parts[p] {
			cp[k] = v
		}
		dumpParts[p] = cp
	}
	return StateDump{
		PartitionCount: m.n,
		Migrating:      m.migrating,
		From:           m.fromN,
		To:             m.toN,
		Cursor:         m.cursor,
		Partitions:     dumpParts,
	}
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// Progress 描述一次重均衡的进度。
type Progress struct {
	Migrating bool
	From      int
	To        int
	Cursor    int
	Total     int
}

// StateDump 是某一时刻存储内容与迁移态的不可变拷贝。
type StateDump struct {
	PartitionCount int
	Migrating      bool
	From           int
	To             int
	Cursor         int
	Partitions     map[int]map[string]string
}

package compensate

import (
	"sync"
)

// memEntry 是存储中一个键的带版本条目；version 在每次写入后单调递增。
type memEntry struct {
	value   []byte
	present bool
	deleted bool
	version uint64
}

// MemoryStore 是进程内的可串行化存储实现。
//
// 并发控制采用严格可串行化的乐观协议（快照读，提交时做读集合版本校验）：
//   - 事务开始时获取全库当前版本快照，所有读基于快照；
//   - 事务内记录读集合（键到读到的版本，含「键不存在」这一事实）；
//   - 提交时在全局临界区内校验读集合版本是否全部未变；
//   - 校验通过则原子地应用全部写并推进版本，否则整体中止并由 Update 重试。
//
// 由于每个事务的读集合都包含它实际依赖的全部键，任何与之冲突的已提交事务
// 都会使其中止；由此得到严格可串行化（事务真实实时顺序即一个合法串行顺序）。
// 同时也满足「多项副作用要么全部生效要么全部不生效」：崩溃发生在 Commit 临界区
// 之外时缓冲写全部丢弃，提交是临界区内一次性应用。
type MemoryStore struct {
	mu      sync.Mutex
	data    map[string]memEntry
	nextVer uint64
	// absentLocks 是「缺失键谓词锁」表：key → 持有该键「读为不存在」谓词的
	// 事务集合，committed 标记持有者是否已经提交。
	//
	// 冲突规则（first-committer-wins，仅针对领取/撤销裁决事务）：
	// 事务 T 要写入（插入）键 k，而 k 此刻在已提交存储中仍不存在时，
	// 若存在另一个已提交或进行中的持有者 H，T 中止重试。重试时 T 会看到
	// H 提交所创建的键（ver>0），从而走到正确的重复/已裁决分支，不再冲突。
	// 键一旦存在，该锁即失效，因此不会影响之后对同键的正常读写（无活锁）。
	absentLocks map[string]map[*memTxn]bool
	// live 是所有已开始尚未结束（提交或中止）的事务集合，
	// 使「先结束的只读事务」与「后提交的写事务」之间也能双向检出插入冲突。
	live map[*memTxn]struct{}
}

// NewMemoryStore 创建空存储。
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{data: make(map[string]memEntry), nextVer: 1,
		absentLocks: map[string]map[*memTxn]bool{},
		live:        map[*memTxn]struct{}{}}
}

// View 在只读可串行化事务中执行 fn（读快照，冲突无需重试）。
func (s *MemoryStore) View(fn func(txn Txn) error) error {
	txn := s.begin()
	if err := fn(txn); err != nil {
		s.abort(txn)
		return err
	}
	return nil
}

// Update 在可串行化读写事务中执行 fn，提交冲突时自动整体重试。
func (s *MemoryStore) Update(fn func(txn Txn) error) error {
	for attempt := 0; ; attempt++ {
		if attempt > 10000 {
			return ErrConflict
		}
		txn := s.begin()
		err := fn(txn)
		if err == nil {
			err = s.commit(txn)
		}
		if err == ErrConflict {
			s.abort(txn)
			continue
		}
		if err != nil {
			s.abort(txn)
		}
		return err
	}
}

func (s *MemoryStore) abort(t *memTxn) {
	s.mu.Lock()
	s.releaseLocks(t)
	delete(s.live, t)
	s.mu.Unlock()
}

func (s *MemoryStore) begin() *memTxn {
	s.mu.Lock()
	defer s.mu.Unlock()
	snap := make(map[string]memEntry, len(s.data))
	for k, v := range s.data {
		snap[k] = v
	}
	t := &memTxn{store: s, snap: snap, readSet: map[string]uint64{}, writes: map[string]memEntry{}}
	s.live[t] = struct{}{}
	return t
}

func (s *MemoryStore) commit(t *memTxn) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for k, readVer := range t.readSet {
		cur, ok := s.data[k]
		curVer := uint64(0)
		if ok {
			curVer = cur.version
		}
		if curVer != readVer {
			return ErrConflict
		}
	}
	// 插入冲突：本事务要写入的每个键，若另一个仍在进行的事务此前曾把它读为
	// 「不存在」，或此前已有事务以该谓词提交，且键此刻仍不存在，
	// 则本插入事务（后来者）中止。自身读过缺失键并自行插入不算冲突。
	for k := range t.writes {
		if _, exists := s.data[k]; exists {
			continue // 键已存在：普通版本校验已覆盖，缺失谓词锁失效
		}
		for holder, committed := range s.absentLocks[k] {
			if holder == t {
				continue
			}
			// 合法兑现：本事务的读集合（已通过上面的 OCC 版本校验，与当前已提交
			// 状态一致）观察到了配对裁决键的存在。例如领取写 record 时，同事务
			// 读到同 EventID 的 undo 键存在——处理器 fn 已据此走 SUPERSEDED 分支，
			// 此插入是对撤销裁决的兑现，消费该谓词锁并放行。
			if committed && t.observesResolution(k) {
				delete(s.absentLocks[k], holder)
				if len(s.absentLocks[k]) == 0 {
					delete(s.absentLocks, k)
				}
				continue
			}
			// 与「进行中」持有者的顺序由提交临界区天然决定：后入临界区者
			// 此时看到的对方必然已提交，故这里直接跳过进行中持有者，避免忙等。
			if !committed {
				continue
			}
			s.releaseLocks(t)
			delete(s.live, t)
			return ErrConflict
		}
	}
	// 提交成功：把本事务的缺失谓词从「活跃」移到「已完成」历史，
	// 使后提交的插入者仍能与之冲突。
	s.commitLocks(t)
	delete(s.live, t)
	for k, e := range t.writes {
		ver := s.nextVer
		s.nextVer++
		e.version = ver
		if e.deleted {
			delete(s.data, k)
		} else {
			s.data[k] = e
		}
	}
	t.committed = true
	return nil
}

type memTxn struct {
	store     *MemoryStore
	snap      map[string]memEntry
	readSet   map[string]uint64
	writes    map[string]memEntry
	committed bool
	// predicateLocked 为 false 时，对缺失键的读取不登记谓词锁。
	// 处理器只在「领取/放弃裁决」这一需要 first-committer-wins 的事务中开启它；
	// 领取完成后的工作事务（含崩溃续作）依赖已存在的补偿记录做版本校验，
	// 不需要互斥缺失谓词，从而避免同事件并发工作事务之间的假冲突活锁。
	predicateLocked bool
	// predicatePrefixes 非空时，仅对这些前缀下的缺失键登记谓词锁
	//（避免审计序号等基础设施键参与裁决冲突）。
	predicatePrefixes []string
}

func (t *memTxn) Get(key string) ([]byte, error) {
	if e, ok := t.writes[key]; ok {
		if e.deleted {
			t.recordRead(key)
			return nil, ErrNotFound
		}
		t.recordRead(key)
		return cloneBytes(e.value), nil
	}
	e, ok := t.snap[key]
	if !ok || e.deleted {
		// 严格以「事务开始快照」为准登记版本 0：若该键在事务期间被其他事务
		// 创建，提交时的版本校验会发现 0→存在 而中止重试。
		// 绝不能在此二次查当前存储，否则会登记一个与返回值（ErrNotFound）
		// 矛盾的版本，导致幻读漏检。
		t.store.mu.Lock()
		t.recordReadLocked(key, 0)
		t.store.mu.Unlock()
		return nil, ErrNotFound
	}
	t.store.mu.Lock()
	t.recordReadLocked(key, e.version)
	t.store.mu.Unlock()
	return cloneBytes(e.value), nil
}

// recordRead 在 store 锁内登记读集合（命中自身缓冲写时版本以全局当前值为准）。
func (t *memTxn) recordRead(key string) {
	t.store.mu.Lock()
	cur, exists := t.store.data[key]
	defer t.store.mu.Unlock()
	if _, ok := t.readSet[key]; ok {
		return
	}
	t.recordReadLocked(key, func() uint64 {
		if exists {
			return cur.version
		}
		return 0
	}())
}

// recordReadLocked 要求调用方已持有 store 锁。
func (t *memTxn) recordReadLocked(key string, ver uint64) {
	if _, exists := t.readSet[key]; exists {
		return
	}
	t.readSet[key] = ver
	if ver == 0 && t.predicateLocked && t.matchesPrefix(key) {
		readers := t.store.absentLocks[key]
		if readers == nil {
			readers = map[*memTxn]bool{}
			t.store.absentLocks[key] = readers
		}
		readers[t] = false
	}
}

func (t *memTxn) matchesPrefix(key string) bool {
	if len(t.predicatePrefixes) == 0 {
		return true
	}
	for _, p := range t.predicatePrefixes {
		if len(key) >= len(p) && key[:len(p)] == p {
			return true
		}
	}
	return false
}

// releaseLocks 在事务中止时调用（已持锁）：移除其全部谓词持有。
func (s *MemoryStore) releaseLocks(t *memTxn) {
	for k, holders := range s.absentLocks {
		if _, ok := holders[t]; ok {
			delete(holders, t)
			if len(holders) == 0 {
				delete(s.absentLocks, k)
			}
		}
	}
}

// observesResolution 判断本事务是否已在当前（已通过版本校验的）读集合中
// 观察到键 k 的配对裁决键存在（版本>0）。
// record 与 undo 为同一 EventID 的两个裁决键，互为配对。
func (t *memTxn) observesResolution(k string) bool {
	const recPfx = "comp/rec/"
	const undoPfx = "comp/undo/"
	if len(k) > len(recPfx) && k[:len(recPfx)] == recPfx {
		return t.readSet[undoPfx+k[len(recPfx):]] > 0
	}
	if len(k) > len(undoPfx) && k[:len(undoPfx)] == undoPfx {
		return t.readSet[recPfx+k[len(undoPfx):]] > 0
	}
	return false
}

// commitLocks 在事务成功提交后调用（已持锁）：
// 本事务插入的键，锁随键存在而删除；本事务仅读过缺失的键，锁保留并标记已提交，
// 以拦截后到的插入者。
func (s *MemoryStore) commitLocks(t *memTxn) {
	for k, holders := range s.absentLocks {
		if _, ok := holders[t]; !ok {
			continue
		}
		if _, inserted := t.writes[k]; inserted {
			// 本事务自己读过缺失又自己插入了同一键：谓词被自己兑现，移除持有。
			// 注意不能因为「本事务插入了别的键」就移除本键的锁——例如撤销事务
			// 插入 undo 键，但它对 record 键的缺失谓词必须保留并标记已提交，
			// 以拦截后到的领取者。
			delete(holders, t)
			if len(holders) == 0 {
				delete(s.absentLocks, k)
			}
			continue
		}
		holders[t] = true // 已提交的缺失谓词，继续拦截后来插入者
	}
}

func (t *memTxn) Put(key string, value []byte) {
	t.writes[key] = memEntry{value: cloneBytes(value), present: true}
}

func (t *memTxn) Delete(key string) {
	t.writes[key] = memEntry{deleted: true}
}

// CreateIfAbsent 在本事务缓冲内做条件插入：key 不存在才写入并返回 true。
// 并发安全性由提交时的缺失谓词/版本校验保证——两个并发事务不可能都成功
// 插入同一此前不存在的键（后提交者读集合版本 0 与现实不符而中止重试，
// 重试时该键已存在从而返回 false）。
func (t *memTxn) CreateIfAbsent(key string, value []byte) bool {
	if e, ok := t.writes[key]; ok {
		return !e.deleted
	}
	if _, err := t.Get(key); err == nil {
		return false
	}
	t.writes[key] = memEntry{value: cloneBytes(value), present: true}
	return true
}

// EnablePredicateLocksFor 开启谓词锁并限定键前缀。
func (t *memTxn) EnablePredicateLocksFor(prefixes ...string) {
	t.predicateLocked = true
	t.predicatePrefixes = prefixes
}

func cloneBytes(b []byte) []byte {
	out := make([]byte, len(b))
	copy(out, b)
	return out
}

// SnapshotPrefix 返回当前已提交存储中所有以 prefix 开头的键值（值为拷贝），
// 供测试与审计读取使用；不经过用户事务，读到的必然是某个已提交状态。
func (s *MemoryStore) SnapshotPrefix(prefix string) map[string][]byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string][]byte)
	for k, e := range s.data {
		if len(k) >= len(prefix) && k[:len(prefix)] == prefix {
			out[k] = cloneBytes(e.value)
		}
	}
	return out
}

// SnapshotGet 返回已提交的单个键值。
func (s *MemoryStore) SnapshotGet(key string) ([]byte, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.data[key]
	if !ok || e.deleted {
		return nil, false
	}
	return cloneBytes(e.value), true
}

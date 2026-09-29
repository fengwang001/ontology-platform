// Package dimcache 实现一个由变更数据捕获（CDC）驱动的维表查询缓存。
//
// 每个键在源头带单调递增的版本号：更新与删除都使版本加一，删除留下
// 墓碑且版本继续递增。缓存为每个键维护“已收事件的最大版本”作为栅栏
// （fence）：收到更旧或重复的事件不做任何事；否则推进栅栏并删除版本
// 过旧的缓存条目。读取分两步：先取源头状态令牌，再用令牌回填；令牌
// 版本不低于回填下限（栅栏）才被接受，负缓存条目（墓碑）同样写入。
package dimcache

import (
	"fmt"
	"strconv"
	"sync"
)

// Event 是一个上游变更事件。
type Event struct {
	Key     string
	Version int64
	// Deleted 为 true 时 Value 必须为空，表示墓碑事件。
	Deleted bool
	Value   string
}

// tokenRecord 是一次源头快照的内部记录。
type tokenRecord struct {
	key     string
	version int64
	deleted bool
	value   string
	used    bool
}

// config 保存管理器的可调参数。
type config struct {
	maxTrackedKeys int
}

// Manager 是线程安全的源头 + CDC 队列 + 栅栏缓存管理器。
type Manager struct {
	mu sync.Mutex

	// 源头：墓碑保留在 source 中，版本继续递增。
	source map[string]sourceRow
	// CDC 事件队列，按入队顺序投递。
	queue []Event
	// 每个键已收事件的最大版本（栅栏）。
	fences map[string]int64
	// 缓存条目，含负缓存（墓碑）条目；长度即“跟踪键数”。
	cache map[string]cacheRow
	// 已签发但尚未使用的一次性令牌。
	pendingTokens map[string]*tokenRecord
	// 已使用或已撤销的令牌 id，用于区分“未知”与“已用”。
	usedTokens map[string]struct{}

	tokenSeq    int64
	sourceReads int64

	cfg config
	log Logger
}

type sourceRow struct {
	version int64
	deleted bool
	value   string
}

type cacheRow struct {
	version int64
	deleted bool
	value   string
}

// New 创建一个管理器。maxTrackedKeys 为缓存可跟踪键数上限，必须为正。
func New(maxTrackedKeys int, logger Logger) *Manager {
	if maxTrackedKeys <= 0 {
		panic("dimcache: maxTrackedKeys must be positive")
	}
	if logger == nil {
		logger = discardLogger{}
	}
	return &Manager{
		source:        map[string]sourceRow{},
		fences:        map[string]int64{},
		cache:         map[string]cacheRow{},
		pendingTokens: map[string]*tokenRecord{},
		usedTokens:    map[string]struct{}{},
		cfg:           config{maxTrackedKeys: maxTrackedKeys},
		log:           logger,
	}
}

// Update 在源头写入/更新一行，返回新版本号。
func (m *Manager) Update(key, value string) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if key == "" {
		m.log.Logf("UPDATE rejected key=%q reason=empty-key state-unchanged", key)
		return 0, ErrEmptyKey
	}
	row := m.source[key]
	newVersion := row.version + 1
	m.source[key] = sourceRow{version: newVersion, value: value}
	m.log.Logf("UPDATE accepted key=%q version=%d (prev=%d deleted=%v) value=%q", key, newVersion, row.version, row.deleted, value)
	return newVersion, nil
}

// Delete 删除源头中的一行并留下墓碑；行不存在则整体拒绝。
func (m *Manager) Delete(key string) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if key == "" {
		m.log.Logf("DELETE rejected key=%q reason=empty-key state-unchanged", key)
		return 0, ErrEmptyKey
	}
	row, ok := m.source[key]
	if !ok || row.deleted {
		m.log.Logf("DELETE rejected key=%q reason=delete-missing state-unchanged", key)
		return 0, ErrDeleteMissing
	}
	newVersion := row.version + 1
	m.source[key] = sourceRow{version: newVersion, deleted: true}
	m.log.Logf("DELETE accepted key=%q tombstone-version=%d", key, newVersion)
	return newVersion, nil
}

// Emit 产生一条变更事件放入队列。
func (m *Manager) Emit(ev Event) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.validateEventLocked(ev); err != nil {
		m.log.Logf("EMIT rejected key=%q version=%d reason=%v state-unchanged", ev.Key, ev.Version, err)
		return err
	}
	m.queue = append(m.queue, ev)
	m.log.Logf("EMIT accepted key=%q version=%d deleted=%v queue-len=%d", ev.Key, ev.Version, ev.Deleted, len(m.queue))
	return nil
}

// PendingEvents 返回队列中尚未投递的事件数。
func (m *Manager) PendingEvents() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.queue)
}

// DeliverNext 投递队首事件，返回是否有事件被处理。
func (m *Manager) DeliverNext() (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.queue) == 0 {
		return false, nil
	}
	ev := m.queue[0]
	m.queue = m.queue[1:]
	if err := m.validateEventLocked(ev); err != nil {
		// 不可达：Emit 已做校验。失败不留痕：事件不被应用、不回队。
		m.log.Logf("DELIVER rejected key=%q version=%d reason=%v state-unchanged", ev.Key, ev.Version, err)
		return false, err
	}
	m.applyEventLocked(ev)
	return true, nil
}

func (m *Manager) validateEventLocked(ev Event) error {
	if ev.Key == "" {
		return ErrEmptyKey
	}
	if ev.Version <= 0 {
		return ErrInvalidVersion
	}
	if ev.Deleted && ev.Value != "" {
		return ErrInvalidTombstone
	}
	return nil
}

// applyEventLocked 应用栅栏规则：
// 更旧或重复（版本 <= 栅栏）的事件不做任何事；
// 否则推进栅栏，并删除版本过旧（缓存版本 < 新栅栏）的缓存条目。
func (m *Manager) applyEventLocked(ev Event) {
	fence := m.fences[ev.Key]
	if ev.Version <= fence {
		m.log.Logf("EVENT ignored key=%q version=%d fence=%d reason=stale-or-duplicate (cache=%s)", ev.Key, ev.Version, fence, m.cacheStringLocked(ev.Key))
		return
	}
	m.fences[ev.Key] = ev.Version
	if entry, hit := m.cache[ev.Key]; hit && entry.version < ev.Version {
		delete(m.cache, ev.Key)
		m.log.Logf("EVENT advanced-fence key=%q %d->%d cache-invalidated version=%d", ev.Key, fence, ev.Version, entry.version)
	} else {
		m.log.Logf("EVENT advanced-fence key=%q %d->%d cache-kept (%s)", ev.Key, fence, ev.Version, m.cacheStringLocked(ev.Key))
	}
}

func (m *Manager) cacheStringLocked(key string) string {
	if entry, hit := m.cache[key]; hit {
		if entry.deleted {
			return fmt.Sprintf("hit tombstone@%d", entry.version)
		}
		return fmt.Sprintf("hit value@%d=%q", entry.version, entry.value)
	}
	return "miss"
}

// BeginRead 第一步：读取源头状态并签发一次性令牌。
func (m *Manager) BeginRead(key string) (tokenID string, version int64, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if key == "" {
		m.log.Logf("BEGIN-READ rejected key=%q reason=empty-key state-unchanged", key)
		return "", 0, ErrEmptyKey
	}
	m.sourceReads++
	m.tokenSeq++
	id := "tok-" + strconv.FormatInt(m.tokenSeq, 10)
	row, exists := m.source[key]
	rec := &tokenRecord{
		key:     key,
		version: row.version,
		deleted: !exists || row.deleted,
		value:   row.value,
	}
	m.pendingTokens[id] = rec
	m.log.Logf("BEGIN-READ key=%q token=%s source-version=%d deleted=%v source-reads=%d fence=%d", key, id, rec.version, rec.deleted, m.sourceReads, m.fences[key])
	return id, rec.version, nil
}

// Backfill 第二步：用一次性令牌回填缓存。
func (m *Manager) Backfill(tokenID string) (err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if tokenID == "" {
		m.log.Logf("BACKFILL rejected token=%q reason=unknown-token state-unchanged", tokenID)
		return ErrUnknownToken
	}
	if _, used := m.usedTokens[tokenID]; used {
		m.log.Logf("BACKFILL rejected token=%s reason=token-used state-unchanged", tokenID)
		return ErrTokenUsed
	}
	rec, ok := m.pendingTokens[tokenID]
	if !ok {
		m.log.Logf("BACKFILL rejected token=%s reason=unknown-token state-unchanged", tokenID)
		return ErrUnknownToken
	}

	fence := m.fences[rec.key]
	// 令牌版本不低于回填下限（栅栏）才接受写入，否则拒绝。
	if rec.version < fence {
		m.log.Logf("BACKFILL rejected token=%s key=%q token-version=%d fence=%d reason=token-stale state-unchanged", tokenID, rec.key, rec.version, fence)
		return ErrTokenStale
	}

	newEntry := cacheRow{version: rec.version, deleted: rec.deleted, value: rec.value}
	if old, hit := m.cache[rec.key]; hit {
		// 只接受更新或同版本的写入，拒绝倒退。
		if rec.version < old.version {
			m.log.Logf("BACKFILL rejected token=%s key=%q token-version=%d cache-version=%d reason=token-stale state-unchanged", tokenID, rec.key, rec.version, old.version)
			return ErrTokenStale
		}
		// 接受时令牌才一次性消费；被拒不留痕。
		delete(m.pendingTokens, tokenID)
		m.usedTokens[tokenID] = struct{}{}
		m.cache[rec.key] = newEntry
		m.log.Logf("BACKFILL accepted token=%s key=%q overwrite %d->%d negative=%v", tokenID, rec.key, old.version, rec.version, rec.deleted)
		return nil
	}
	if len(m.cache) >= m.cfg.maxTrackedKeys {
		m.log.Logf("BACKFILL rejected token=%s key=%q reason=too-many-tracked-keys tracked=%d limit=%d state-unchanged", tokenID, rec.key, len(m.cache), m.cfg.maxTrackedKeys)
		return ErrTooManyTrackedKeys
	}
	// 接受时令牌才一次性消费；被拒不留痕。
	delete(m.pendingTokens, tokenID)
	m.usedTokens[tokenID] = struct{}{}
	m.cache[rec.key] = newEntry
	m.log.Logf("BACKFILL accepted token=%s key=%q insert version=%d negative=%v tracked=%d", tokenID, rec.key, rec.version, rec.deleted, len(m.cache))
	return nil
}

// Query 查询键：命中（含负缓存）直接返回；未命中走源头两阶段读取。
func (m *Manager) Query(key string) (value string, found bool, err error) {
	if key == "" {
		m.log.Logf("QUERY rejected key=%q reason=empty-key state-unchanged", key)
		return "", false, ErrEmptyKey
	}
	// 读取回填与失效可能交错：令牌可能因栅栏推进而被拒。
	// 有限重试后返回最后一次源头快照结果，保证与直读源头同效。
	for attempt := 1; ; attempt++ {
		m.mu.Lock()
		if entry, hit := m.cache[key]; hit {
			m.log.Logf("QUERY key=%q cache-hit version=%d negative=%v attempt=%d", key, entry.version, entry.deleted, attempt)
			m.mu.Unlock()
			if entry.deleted {
				return "", false, nil
			}
			return entry.value, true, nil
		}
		fence := m.fences[key]
		m.sourceReads++
		m.tokenSeq++
		id := "tok-" + strconv.FormatInt(m.tokenSeq, 10)
		row, exists := m.source[key]
		rec := tokenRecord{
			key:     key,
			version: row.version,
			deleted: !exists || row.deleted,
			value:   row.value,
		}
		m.pendingTokens[id] = &rec
		m.log.Logf("QUERY key=%q cache-miss fence=%d begin-read token=%s source-version=%d attempt=%d", key, fence, id, rec.version, attempt)

		// 复用回填判定，但不接受 ErrTooManyTrackedKeys 之外的失败作为终态。
		bfErr := m.backfillLocked(id, &rec)
		m.mu.Unlock()

		switch {
		case bfErr == nil:
			if rec.deleted {
				return "", false, nil
			}
			return rec.value, true, nil
		case bfErr == ErrTokenStale:
			m.log.Logf("QUERY key=%q retry=%d reason=backfill-stale", key, attempt)
			if attempt >= 8 {
				// 极端竞争下的保底：返回刚取到的源头快照，结果仍与直读一致。
				m.log.Logf("QUERY key=%q fallback-to-snapshot version=%d after=%d attempts", key, rec.version, attempt)
				if rec.deleted {
					return "", false, nil
				}
				return rec.value, true, nil
			}
			continue
		case bfErr == ErrTooManyTrackedKeys:
			// 容量满时不写缓存，直接返回快照（等同于直读）。
			m.log.Logf("QUERY key=%q bypass-cache reason=too-many-tracked-keys version=%d", key, rec.version)
			if rec.deleted {
				return "", false, nil
			}
			return rec.value, true, nil
		default:
			return "", false, bfErr
		}
	}
}

// backfillLocked 在已持锁状态下对“内存中的令牌”执行回填判定。
// 与 Backfill 的区别：令牌由调用方现场签发，不经过 id 表的未知/已用检查。
func (m *Manager) backfillLocked(id string, rec *tokenRecord) error {
	delete(m.pendingTokens, id)
	m.usedTokens[id] = struct{}{}
	fence := m.fences[rec.key]
	if rec.version < fence {
		return ErrTokenStale
	}
	newEntry := cacheRow{version: rec.version, deleted: rec.deleted, value: rec.value}
	if old, hit := m.cache[rec.key]; hit {
		if rec.version < old.version {
			return ErrTokenStale
		}
		m.cache[rec.key] = newEntry
		return nil
	}
	if len(m.cache) >= m.cfg.maxTrackedKeys {
		return ErrTooManyTrackedKeys
	}
	m.cache[rec.key] = newEntry
	return nil
}

// DirectQuery 无缓存参照：直接读源头，用于一致性对照。
func (m *Manager) DirectQuery(key string) (value string, found bool, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if key == "" {
		m.log.Logf("DIRECT-QUERY rejected key=%q reason=empty-key state-unchanged", key)
		return "", false, ErrEmptyKey
	}
	m.sourceReads++
	row, exists := m.source[key]
	if !exists || row.deleted {
		m.log.Logf("DIRECT-QUERY key=%q not-found version=%d source-reads=%d", key, row.version, m.sourceReads)
		return "", false, nil
	}
	m.log.Logf("DIRECT-QUERY key=%q found version=%d source-reads=%d", key, row.version, m.sourceReads)
	return row.value, true, nil
}

// Quiescent 返回队列是否清空且无未完成（未使用）令牌。
func (m *Manager) Quiescent() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.queue) == 0 && len(m.pendingTokens) == 0
}

// SourceReads 返回源头直读计数（BeginRead / DirectQuery / Query 回源次数）。
func (m *Manager) SourceReads() int64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.sourceReads
}

// Fence 返回某键当前栅栏版本；键未被跟踪时返回 0。
func (m *Manager) Fence(key string) int64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.fences[key]
}

// CacheEntry 描述一个缓存条目快照。
type CacheEntry struct {
	Version int64
	Deleted bool
	Value   string
}

// CachedEntry 返回某键的缓存条目快照，第二个返回值表示是否在缓存中。
func (m *Manager) CachedEntry(key string) (CacheEntry, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	entry, hit := m.cache[key]
	if !hit {
		return CacheEntry{}, false
	}
	return CacheEntry{Version: entry.version, Deleted: entry.deleted, Value: entry.value}, true
}

package dimcache

import (
	"context"
	"sync"
)

// Entry 是缓存条目（含负缓存）。
type Entry struct {
	Version  int64
	Value    string
	Negative bool
}

// Token 是第一步读源头得到的一次性状态令牌。
type Token struct {
	ID      int64
	Key     string
	Version int64
	Value   string
	Deleted bool
}

type tokenState struct {
	tok  Token
	used bool
}

// Cache 是栅栏 + 条目 + 未完成令牌的整体，所有操作并发安全。
type Cache struct {
	mu sync.RWMutex

	src     *Source
	queue   *EventQueue
	maxKeys int
	logger  Logger

	tracked map[string]struct{}
	fences  map[string]int64
	entries map[string]Entry
	tokens  map[int64]*tokenState
	nextID  int64
}

// NewCache 组装系统。maxTrackedKeys<=0 表示不限制被跟踪键数量。
func NewCache(src *Source, q *EventQueue, maxTrackedKeys int, logger Logger) *Cache {
	return &Cache{
		src:     src,
		queue:   q,
		maxKeys: maxTrackedKeys,
		logger:  logger,
		tracked: map[string]struct{}{},
		fences:  map[string]int64{},
		entries: map[string]Entry{},
		tokens:  map[int64]*tokenState{},
	}
}

// touch 登记被跟踪的键；新键导致超限时返回错误且不修改任何状态。
func (c *Cache) touch(key string) error {
	if _, ok := c.tracked[key]; ok {
		return nil
	}
	if c.maxKeys > 0 && len(c.tracked) >= c.maxKeys {
		return ErrTrackedKeysExceeded
	}
	c.tracked[key] = struct{}{}
	return nil
}

// Query 查询缓存：命中（含负缓存）直接返回；未命中不读源头。
func (c *Cache) Query(ctx context.Context, key string) (Entry, bool, error) {
	if key == "" {
		c.log("query.reject", map[string]any{"key": key, "reason": "empty_key"})
		return Entry{}, false, ErrEmptyKey
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	e, ok := c.entries[key]
	c.log("query", map[string]any{
		"key": key, "hit": ok, "entry": e, "fence": c.fences[key],
	})
	return e, ok, nil
}

// IssueReadToken 读取流程第一步：取源头状态并签发一次性令牌（计入未完成令牌）。
func (c *Cache) IssueReadToken(ctx context.Context, key string) (*Token, error) {
	if key == "" {
		c.log("issue.reject", map[string]any{"key": key, "reason": "empty_key"})
		return nil, ErrEmptyKey
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.touch(key); err != nil {
		c.log("issue.reject", map[string]any{
			"key": key, "reason": "tracked_keys_exceeded", "tracked": len(c.tracked),
		})
		return nil, err
	}
	rec, found := c.src.Read(ctx, key)
	c.nextID++
	tok := Token{ID: c.nextID, Key: key, Version: rec.Version}
	if !found || rec.Deleted {
		tok.Deleted = true
	} else {
		tok.Value = rec.Value
	}
	c.tokens[tok.ID] = &tokenState{tok: tok}
	c.log("issue.ok", map[string]any{
		"token": tok, "found": found, "fence": c.fences[key], "pending": c.pendingLocked(),
	})
	return &tok, nil
}

// Backfill 读取流程第二步：用令牌回填。令牌必须未知→未用→版本不低于回填下限，
// 任一条件不满足整体拒绝，且任何拒绝都不改变任何状态。
func (c *Cache) Backfill(ctx context.Context, tok *Token) error {
	if tok == nil {
		c.log("backfill.reject", map[string]any{"reason": "token_unknown"})
		return ErrTokenUnknown
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	st, ok := c.tokens[tok.ID]
	if !ok {
		c.log("backfill.reject", map[string]any{"token": tok, "reason": "token_unknown"})
		return ErrTokenUnknown
	}
	if st.used {
		c.log("backfill.reject", map[string]any{"token": tok, "reason": "token_used"})
		return ErrTokenUsed
	}
	fence := c.fences[st.tok.Key]
	if st.tok.Version < fence {
		// 过期令牌永不可能再被接受，回收它使系统可收敛到静止；
		// 拒绝判定先于回收，栅栏、缓存、源头均不受影响。
		delete(c.tokens, tok.ID)
		c.log("backfill.reject", map[string]any{
			"token": st.tok, "fence": fence, "floor": fence, "reason": "token_stale",
		})
		return ErrTokenStale
	}
	st.used = true
	c.entries[st.tok.Key] = Entry{
		Version:  st.tok.Version,
		Value:    st.tok.Value,
		Negative: st.tok.Deleted,
	}
	c.log("backfill.ok", map[string]any{
		"token": st.tok, "fence": fence, "floor": fence,
		"entry": c.entries[st.tok.Key], "pending": c.pendingLocked(),
	})
	return nil
}

// Deliver 投递一个 CDC 事件：版本不大于栅栏（旧/重复）则忽略；
// 否则推进栅栏并删除版本过旧的缓存条目。
func (c *Cache) Deliver(e Event) error {
	if e.Key == "" {
		c.log("deliver.reject", map[string]any{"event": e, "reason": "empty_key"})
		return ErrEmptyKey
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.touch(e.Key); err != nil {
		c.log("deliver.reject", map[string]any{
			"event": e, "reason": "tracked_keys_exceeded", "tracked": len(c.tracked),
		})
		return err
	}
	oldFence := c.fences[e.Key]
	if e.Version <= oldFence {
		reason := "stale"
		if e.Version == oldFence {
			reason = "duplicate"
		}
		c.log("deliver.ignore", map[string]any{
			"event": e, "fence": oldFence, "reason": reason,
		})
		return nil
	}
	c.fences[e.Key] = e.Version
	if ent, ok := c.entries[e.Key]; ok && ent.Version < e.Version {
		delete(c.entries, e.Key)
		c.log("cache.evict", map[string]any{"key": e.Key, "entry": ent, "new_fence": e.Version})
	}
	c.log("deliver.advance", map[string]any{
		"event": e, "old_fence": oldFence, "new_fence": e.Version,
	})
	return nil
}

// DrainDeliver 从队列取出一个事件并投递；队列空时返回 false。
func (c *Cache) DrainDeliver(ctx context.Context) (bool, error) {
	e, ok := c.queue.DrainOne()
	if !ok {
		return false, nil
	}
	if err := c.Deliver(e); err != nil {
		return true, err
	}
	return true, nil
}

// Fence 返回某键当前的栅栏版本（未见事件时为 0）。
func (c *Cache) Fence(key string) int64 {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.fences[key]
}

// SnapshotEntry 返回当前缓存条目（含负缓存）的只读快照。
func (c *Cache) SnapshotEntry(key string) (Entry, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	e, ok := c.entries[key]
	return e, ok
}

// SourceReads 返回源头被直接读取的总次数。
func (c *Cache) SourceReads() int64 {
	return c.src.ReadCount()
}

// PendingTokens 返回已签发但尚未完成（回填或未使用）的令牌数。
func (c *Cache) PendingTokens() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.pendingLocked()
}

func (c *Cache) pendingLocked() int {
	n := 0
	for _, st := range c.tokens {
		if !st.used {
			n++
		}
	}
	return n
}

// TrackedKeys 返回系统跟踪的不同键数量。
func (c *Cache) TrackedKeys() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.tracked)
}

// Quiescent 报告系统是否已静止：事件队列清空且无未完成令牌。
func (c *Cache) Quiescent() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.queue.Len() == 0 && c.pendingLocked() == 0
}

func (c *Cache) log(step string, fields map[string]any) {
	if c.logger != nil {
		c.logger.Log(step, fields)
	}
}

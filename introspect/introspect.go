// Package introspect 缓存不透明令牌的上游内省结果，并对同令牌并发缺失做单飞。
package introspect

import "sync"

// Result 是上游内省函数的成功返回。
type Result struct {
	Active bool
	Sub    string
	Iat    int64
	Exp    int64
	Scopes []string
}

// Upstream 是上游内省函数。
type Upstream func(token string) (Result, error)

// Source 表示取得记录的方式。
type Source int

const (
	SourceCache Source = iota // 新鲜缓存命中
	SourceFresh               // 本次（或同组单飞）调用 f 取得
	SourceStale               // f 失败后复用的陈旧正向记录
)

// Entry 是缓存中的一条内省记录。
type Entry struct {
	Res Result
	F0  int64 // 发起产生该记录的上游调用时的 now
	U   int64 // 鲜期终点：正向 min(f0+P, Exp)，负向 f0+N
}

// inflight 表示一次进行中的单飞上游调用，等待者在 done 上阻塞。
type inflight struct {
	done    chan struct{}
	entry   Entry // 成功时为新记录；失败时保持零值
	err     error
	waiters int // 已加入等待的调用数（含发起者）
}

// Cache 是按令牌的内省缓存。
type Cache struct {
	P, N, G int64
	f       Upstream

	mu      sync.Mutex
	records map[string]Entry
	flights map[string]*inflight
	calls   int64
}

// New 创建缓存。P 为正向缓存期、N 为负向缓存期、G 为陈旧宽限（毫秒）。
func New(P, N, G int64, f Upstream) *Cache {
	return &Cache{P: P, N: N, G: G, f: f, records: make(map[string]Entry), flights: make(map[string]*inflight)}
}

// Calls 返回实际进入上游函数 f 的次数。
func (c *Cache) Calls() int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calls
}

// TouchedByRevoke 返回 Revoke 操作触及（遍历/修改/失效）的缓存条目计数。
// Revoke 不经由本缓存执行，该计数恒为 0。
func (c *Cache) TouchedByRevoke() int64 { return 0 }

// Len 返回当前缓存的令牌条目数。
func (c *Cache) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.records)
}

// freshEnd 计算记录的鲜期终点。
func freshEnd(res Result, f0, P, N int64) int64 {
	if res.Active {
		u := f0 + P
		if res.Exp < u {
			u = res.Exp
		}
		return u
	}
	return f0 + N
}

// Obtain 取得令牌记录。
//   - 存在新鲜记录（now < u）：直接返回，来源 Cache，不调用 f；
//   - 否则发起（或加入）该令牌的单飞调用：成功则以 f0=发起者 now 覆盖缓存，
//     所有同组等待者共用同一记录，来源 Fresh；
//   - f 失败：缓存不变，err 非 nil，同时返回当前缓存中的旧记录（可能为零值），
//     由 Gate 按各自的 now 与需求风险决定陈旧复用或 Unavailable。
func (c *Cache) Obtain(token string, now int64) (Entry, Source, error) {
	c.mu.Lock()
	if e, ok := c.records[token]; ok && now < e.U {
		c.mu.Unlock()
		return e, SourceCache, nil
	}
	if call, ok := c.flights[token]; ok {
		call.waiters++
		c.mu.Unlock()
		<-call.done
		c.mu.Lock()
		if call.err == nil {
			e := call.entry
			c.mu.Unlock()
			return e, SourceFresh, nil
		}
		old := c.records[token]
		c.mu.Unlock()
		return old, 0, call.err
	}
	call := &inflight{done: make(chan struct{})}
	call.waiters = 1
	c.flights[token] = call
	f := c.f
	c.mu.Unlock()

	res, err := f(token)

	c.mu.Lock()
	c.calls++
	delete(c.flights, token)
	if err != nil {
		call.err = err
		old := c.records[token]
		c.mu.Unlock()
		close(call.done)
		return old, 0, err
	}
	e := Entry{Res: res, F0: now, U: freshEnd(res, now, c.P, c.N)}
	c.records[token] = e
	call.entry = e
	c.mu.Unlock()
	close(call.done)
	return e, SourceFresh, nil
}

// Waiters 返回某令牌正在进行的单飞调用总数（含发起者）；无进行中调用时为 0。
// 仅供并发测试同步等待者入队使用。
func (c *Cache) Waiters(token string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	if call, ok := c.flights[token]; ok {
		return call.waiters
	}
	return 0
}

// Peek 返回某令牌当前的缓存记录（不论鲜期），不调用上游、不改变状态。
// 供 Gate 在已知主体被撤销时短路：该令牌 Iat 已不大于撤销纪元，
// 撤销不可恢复，无需再问上游。
func (c *Cache) Peek(token string) (Entry, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.records[token]
	return e, ok
}

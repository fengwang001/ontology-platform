package aliascache

import (
	"bytes"
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	kindAlias byte = iota + 1
	kindAddress
	kindNegative
)

type entry struct {
	kind      byte
	alias     string
	addresses []string
	expiresAt time.Time
}

type inflight struct {
	done chan struct{}
	// fresh 为本次查询新得的记录；TTL 为 0 时不写入缓存。
	fresh *entry
	err   error
}

type acquired struct {
	name     string
	e        *entry
	doCommit bool
}

// Cache 是带别名链解析的名字缓存。
type Cache struct {
	mu       sync.Mutex
	capacity int
	now      ClockFunc
	upstream UpstreamFunc
	logw     io.Writer
	entries  map[string]*entry
	flights  map[string]*inflight
	resolves map[string]*resolveCall
}

// resolveCall 合并对同一名字的并发解析：等待者得到与发起者完全相同的结果或错误。
type resolveCall struct {
	done    chan struct{}
	waiters int
	res     Result
	err     error
}

// Len 返回当前缓存条目数（不含已到期条目：会顺带清理）。
func (c *Cache) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.purgeExpiredLocked(c.now())
	return len(c.entries)
}

func (e *entry) aliveLocked(now time.Time) bool {
	return now.Before(e.expiresAt)
}

// purgeExpiredLocked 删除所有到期条目；恰在到期时刻（now == expiresAt）即失效。
func (c *Cache) purgeExpiredLocked(now time.Time) {
	for name, e := range c.entries {
		if !e.aliveLocked(now) {
			delete(c.entries, name)
		}
	}
}

// storeLocked 存入一条记录（同名替换）。存入前先清除失效条目；
// 仍满则淘汰到期最早者，并列时取名字字典序较小者。
// TTL 为 0 的记录不存入。
func (c *Cache) storeLocked(name string, e *entry, now time.Time) bool {
	if !e.aliveLocked(now) {
		return false
	}
	c.purgeExpiredLocked(now)
	if _, exists := c.entries[name]; !exists {
		for len(c.entries) >= c.capacity {
			victim := ""
			var earliest time.Time
			for nm, cur := range c.entries {
				if victim == "" || cur.expiresAt.Before(earliest) ||
					(cur.expiresAt.Equal(earliest) && nm < victim) {
					victim = nm
					earliest = cur.expiresAt
				}
			}
			delete(c.entries, victim)
		}
	}
	c.entries[name] = e
	return true
}

// fetch 返回 name 的记录：先查缓存，未命中则向上游查询。
// 并发查询同一未命中名字时只发起一次上游调用，等待者共享同一结果。
// 返回值 doCommit 为 true 表示本调用是该名字的查询发起者，
// 且该记录来自上游（而非缓存），解析成功后应由本调用负责提交。
func (c *Cache) fetch(name, startName string, now time.Time, log *bytes.Buffer) (e *entry, doCommit bool, err error) {
	c.mu.Lock()
	if e, ok := c.entries[name]; ok && e.aliveLocked(now) {
		c.mu.Unlock()
		fmt.Fprintf(log, "    hop %q -> cache hit kind=%d expires_in=%s\n",
			name, e.kind, e.expiresAt.Sub(now))
		return e, false, nil
	}
	if f, ok := c.flights[name]; ok {
		c.mu.Unlock()
		fmt.Fprintf(log, "    hop %q -> join inflight upstream query\n", name)
		<-f.done
		if f.err != nil {
			return nil, false, f.err
		}
		return cloneEntry(f.fresh), false, nil
	}
	f := &inflight{done: make(chan struct{})}
	c.flights[name] = f
	c.mu.Unlock()

	fmt.Fprintf(log, "    hop %q -> cache miss, querying upstream\n", name)
	answer, err := c.upstream(name)
	got := answerToEntry(answer, now)

	c.mu.Lock()
	f.err = err
	f.fresh = got
	close(f.done)
	delete(c.flights, name)
	c.mu.Unlock()

	if err != nil {
		fmt.Fprintf(log, "    hop %q -> upstream error: %v\n", name, err)
		return nil, false, err
	}
	fmt.Fprintf(log, "    hop %q -> upstream answer kind=%d ttl=%s\n", name, got.kind, got.expiresAt.Sub(now))
	return got, true, nil
}

// inflightWaiters 返回 name 当前挂接等待同一整链解析的并发数，仅供测试同步使用。
func (c *Cache) inflightWaiters(name string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	if rc, ok := c.resolves[name]; ok {
		return rc.waiters
	}
	return 0
}

func cloneEntry(e *entry) *entry {
	if e == nil {
		return nil
	}
	cp := *e
	cp.addresses = append([]string(nil), e.addresses...)
	return &cp
}

func answerToEntry(a UpstreamAnswer, now time.Time) *entry {
	switch {
	case a.Alias != nil:
		return &entry{kind: kindAlias, alias: a.Alias.Target, expiresAt: now.Add(a.Alias.TTL)}
	case a.Addresses != nil:
		addrs := dedupSorted(a.Addresses.Addresses)
		return &entry{kind: kindAddress, addresses: addrs, expiresAt: now.Add(a.Addresses.TTL)}
	default:
		ttl := time.Duration(0)
		if n := a.Negative; n != nil {
			ttl = n.SOATTL
			if n.MinTTL < ttl {
				ttl = n.MinTTL
			}
		}
		return &entry{kind: kindNegative, expiresAt: now.Add(ttl)}
	}
}

func dedupSorted(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

// Option 配置 Cache。
type Option func(*Cache)

// WithClock 注入时钟，便于测试到期判定。
func WithClock(clock ClockFunc) Option {
	return func(c *Cache) { c.now = clock }
}

// WithLogger 注入解析日志输出。
func WithLogger(w io.Writer) Option {
	return func(c *Cache) { c.logw = w }
}

// New 创建容量为 capacity 的缓存，upstream 为上游查询函数。
func New(capacity int, upstream UpstreamFunc, opts ...Option) *Cache {
	c := &Cache{
		capacity: capacity,
		upstream: upstream,
		now:      time.Now,
		entries:  map[string]*entry{},
		flights:  map[string]*inflight{},
		resolves: map[string]*resolveCall{},
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// Resolve 沿别名链解析 name，返回地址结果或否定结果。
func (c *Cache) Resolve(name string) (Result, error) {
	var log bytes.Buffer
	now := c.now()
	fmt.Fprintf(&log, "resolve input name=%q at=%s\n", name, now.Format(time.RFC3339Nano))

	if name == "" {
		log.WriteString("reject: name is empty (checked before any chain traversal)\n")
		c.emitLog(name, Result{}, ErrEmptyName, &log)
		return Result{}, ErrEmptyName
	}

	// 同名解析的 singleflight：非空名字的并发解析由唯一发起者走链，
	// 其余调用等待并共享同一结果或同一错误。
	c.mu.Lock()
	if rc, ok := c.resolves[name]; ok {
		rc.waiters++
		c.mu.Unlock()
		log.WriteString("join inflight resolution for same name\n")
		<-rc.done
		fmt.Fprintf(&log, "shared inflight result found=%t ttl=%s err=%v\n", rc.res.Found, rc.res.TTL, rc.err)
		c.emitLog(name, rc.res, rc.err, &log)
		return rc.res, rc.err
	}
	rc := &resolveCall{done: make(chan struct{})}
	c.resolves[name] = rc
	c.mu.Unlock()

	res, err := c.resolveChain(name, now, &log)

	c.mu.Lock()
	rc.res, rc.err = res, err
	delete(c.resolves, name)
	close(rc.done)
	c.mu.Unlock()

	c.emitLog(name, res, err, &log)
	return res, err
}

// resolveChain 走整条别名链。失败时不写入任何新记录（失败不污染缓存）；
// 成功时按遍历顺序提交本次新得记录，缓存因此保持调用前状态直到解析成功。
func (c *Cache) resolveChain(startName string, now time.Time, log *bytes.Buffer) (Result, error) {
	var chain []acquired
	visited := map[string]struct{}{startName: {}}
	aliases := []string{}
	current := startName
	minTTL := time.Duration(1<<63 - 1)

	for hop := 0; ; hop++ {
		e, doCommit, err := c.fetch(current, startName, now, log)
		if err != nil {
			fmt.Fprintf(log, "reject at hop %d (%q): upstream failure: %v; cache left unchanged\n", hop, current, err)
			return Result{}, err
		}

		remaining := e.expiresAt.Sub(now)
		if remaining < minTTL {
			minTTL = remaining
		}
		chain = append(chain, acquired{name: current, e: e, doCommit: doCommit})

		switch e.kind {
		case kindAddress:
			fmt.Fprintf(log, "decision: terminal address record after %d alias hop(s); min remaining ttl=%s\n",
				len(aliases), minTTL)
			c.commit(chain, now, log)
			return Result{
				Name:      startName,
				Aliases:   append([]string(nil), aliases...),
				Addresses: append([]string(nil), e.addresses...),
				Found:     true,
				TTL:       minTTL,
			}, nil
		case kindNegative:
			fmt.Fprintf(log, "decision: terminal negative record after %d alias hop(s); min remaining ttl=%s\n",
				len(aliases), minTTL)
			c.commit(chain, now, log)
			return Result{
				Name:    startName,
				Aliases: append([]string(nil), aliases...),
				Found:   false,
				TTL:     minTTL,
			}, nil
		}

		if _, seen := visited[e.alias]; seen {
			fmt.Fprintf(log, "reject: cycle detected at %q -> %q; cache left unchanged\n", current, e.alias)
			return Result{}, ErrCycle
		}
		if len(aliases) >= MaxAliasHops {
			fmt.Fprintf(log, "reject: alias chain too long (>%d aliases); cache left unchanged\n", MaxAliasHops)
			return Result{}, ErrTooLong
		}
		visited[e.alias] = struct{}{}
		aliases = append(aliases, e.alias)
		current = e.alias
	}
}

// commit 在解析成功后原子地提交本次新得记录。TTL 为 0 的记录不写入。
func (c *Cache) commit(chain []acquired, now time.Time, log *bytes.Buffer) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, a := range chain {
		if !a.doCommit {
			continue
		}
		if c.storeLocked(a.name, a.e, now) {
			fmt.Fprintf(log, "commit: store %q kind=%d expires_at=%s\n",
				a.name, a.e.kind, a.e.expiresAt.Format(time.RFC3339Nano))
		} else {
			fmt.Fprintf(log, "commit: skip %q ttl=0 (not cached, used for this resolution only)\n", a.name)
		}
	}
}

func (c *Cache) emitLog(name string, res Result, err error, log *bytes.Buffer) {
	if c.logw == nil {
		return
	}
	var b strings.Builder
	io.Copy(&b, log)
	if err != nil {
		fmt.Fprintf(&b, "resolve output name=%q error=%v\n", name, err)
	} else {
		fmt.Fprintf(&b, "resolve output name=%q found=%t aliases=%v addresses=%v ttl=%s\n",
			name, res.Found, res.Aliases, res.Addresses, res.TTL)
	}
	io.WriteString(c.logw, b.String())
}

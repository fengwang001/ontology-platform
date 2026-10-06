// Package ecscache 实现支持客户端子网（ECS，EDNS Client Subnet）扩展的
// 递归解析缓存：同一名称与记录类型可按应答声明的适用范围分别缓存多份结果，
// 查询时按客户端地址选取适用范围最长且覆盖该地址的未过期条目，并对同源前缀
// 的并发未命中合并为一次上游查询。
//
// 时钟与上游解析器均由调用方注入，便于测试与替换。
package ecscache

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

// 地址族：客户端地址的字节数。
const (
	FamilyIPv4 = 4  // 四字节地址，前缀长度上限 32
	FamilyIPv6 = 16 // 十六字节地址，前缀长度上限 128
)

// MaxTTLSeconds 为应答允许声明的最大生存时间（秒）。
const MaxTTLSeconds = 604800

// maxResolveAttempts 是单条查询在“等待者未被共享应答覆盖”时允许的最大
// 重新查询次数，用于防御持续返回不覆盖范围且 TTL 为 0 的病态上游造成的
// 活锁；达到上限后直接返回最近一次收到的应答。
const maxResolveAttempts = 8

var (
	// ErrInvalidArgument 表示查询参数非法（地址长度、前缀长度、名称为空等）。
	ErrInvalidArgument = errors.New("ecscache: invalid argument")
	// ErrUpstream 表示上游失败，包括上游返回错误以及上游应答本身非法。
	ErrUpstream = errors.New("ecscache: upstream failure")
)

// Kind 为应答结果的类别。
type Kind int

const (
	KindRecords  Kind = iota // 正常记录集
	KindNXDomain             // 名字不存在（否定结果）
	KindNoData               // 无此类型（否定结果）
)

func (k Kind) String() string {
	switch k {
	case KindRecords:
		return "records"
	case KindNXDomain:
		return "nxdomain"
	case KindNoData:
		return "nodata"
	default:
		return fmt.Sprintf("kind(%d)", int(k))
	}
}

// Result 为一次解析的结果。否定结果（NXDomain/NoData）不携带记录。
type Result struct {
	Kind    Kind
	Records []string // 仅当 Kind == KindRecords 时有意义
}

// Query 为一次客户端查询。SourcePrefixLen 为发往上游时声明的源前缀长度，
// 取 0 表示不暴露客户端地址。
type Query struct {
	Name            string
	Type            uint16
	ClientAddress   []byte // 4 或 16 字节
	SourcePrefixLen int
}

// Request 为发往上游解析器的请求。Name 已规范化（小写、去掉一个末尾点）；
// ClientAddress 为发起合并查询的代表客户端完整地址，SourcePrefixLen 为
// 对外声明的源前缀长度。
type Request struct {
	Name            string
	Type            uint16
	ClientAddress   []byte
	SourcePrefixLen int
}

// Response 为上游应答。Scope 为应答声明的适用范围前缀长度，0 表示对所有
// 客户端适用；TTL 以秒计，合法范围 [0, MaxTTLSeconds]。
type Response struct {
	Result Result
	TTL    int
	Scope  int
}

// Resolver 为调用方注入的上游解析器。
type Resolver interface {
	Resolve(ctx context.Context, req Request) (Response, error)
}

// ResolverFunc 使普通函数满足 Resolver 接口。
type ResolverFunc func(ctx context.Context, req Request) (Response, error)

// Resolve 实现 Resolver。
func (f ResolverFunc) Resolve(ctx context.Context, req Request) (Response, error) {
	return f(ctx, req)
}

// bucketKey 为容量桶的键：同名字同类型的条目共享容量上限 K（不限地址族）。
type bucketKey struct {
	name string
	typ  uint16
}

// entryKey 为缓存条目的键：地址族 + 有效适用范围长度 + 按该范围清零后的前缀。
type entryKey struct {
	family int
	scope  int
	prefix string // 掩码后的地址字节串
}

// groupKey 为并发合并键：同名字、同类型、同源前缀（掩码后地址相同且长度相同）。
type groupKey struct {
	name      string
	typ       uint16
	family    int
	sourceLen int
	prefix    string
}

type entry struct {
	family    int
	prefix    []byte // 已按 scope 掩码清零
	scope     int
	result    Result
	expiresAt time.Time
	seq       uint64 // 写入序号，用于淘汰时“更早写入者优先”的并列判定
}

type bucket struct {
	entries map[entryKey]*entry
}

// group 为一次进行中的合并上游查询。
type group struct {
	done    chan struct{}
	repAddr []byte // 代表客户端完整地址（即实际发往上游的地址）
	scope   int    // 上游应答声明的适用范围（未钳制）
	result  Result
	err     error
}

// Cache 为 ECS 感知的递归解析缓存。所有方法可并发调用，并发结果等价于
// 某个串行顺序。
type Cache struct {
	k        int
	clock    func() time.Time
	upstream Resolver

	mu      sync.Mutex
	buckets map[bucketKey]*bucket
	groups  map[groupKey]*group
	seq     uint64
}

// New 创建缓存。k 为同名字同类型下的条目数上限（>=1）；clock 与 upstream
// 由调用方注入。参数不满足时 panic（属于编程错误）。
func New(k int, clock func() time.Time, upstream Resolver) *Cache {
	if k < 1 {
		panic("ecscache: capacity K must be >= 1")
	}
	if clock == nil {
		panic("ecscache: clock is required")
	}
	if upstream == nil {
		panic("ecscache: upstream resolver is required")
	}
	return &Cache{
		k:        k,
		clock:    clock,
		upstream: upstream,
		buckets:  make(map[bucketKey]*bucket),
		groups:   make(map[groupKey]*group),
	}
}

// Resolve 解析一次查询。命中时返回缓存结果；未命中时向上游查询并按应答
// 声明的适用范围写入缓存。错误可区分：ErrInvalidArgument 优先于 ErrUpstream。
func (c *Cache) Resolve(ctx context.Context, q Query) (Result, error) {
	fam, err := validateQuery(q)
	if err != nil {
		return Result{}, err
	}
	name := normalizeName(q.Name)
	// 防御性拷贝，调用方或上游对切片的修改不影响缓存内部状态。
	addr := append([]byte(nil), q.ClientAddress...)
	sourcePrefix := maskAddr(addr, q.SourcePrefixLen)

	for attempt := 0; ; attempt++ {
		c.mu.Lock()
		now := c.clock()
		if b := c.buckets[bucketKey{name: name, typ: q.Type}]; b != nil {
			b.purgeExpired(now)
			if e := b.lookup(fam, addr, now); e != nil {
				res := e.result
				c.mu.Unlock()
				return res, nil
			}
		}
		gk := groupKey{
			name: name, typ: q.Type, family: fam,
			sourceLen: q.SourcePrefixLen, prefix: string(sourcePrefix),
		}
		if g, ok := c.groups[gk]; ok {
			// 已有同源前缀的并发查询在进行：等待并共享其结果。
			c.mu.Unlock()
			select {
			case <-g.done:
			case <-ctx.Done():
				return Result{}, ctx.Err()
			}
			if g.err != nil {
				return Result{}, g.err
			}
			// 覆盖判定使用上游声明的（未钳制）适用范围：若声明范围比
			// 源前缀更长，则本等待者的地址可能落在范围之外。
			if prefixEqual(g.repAddr, addr, g.scope) {
				return g.result, nil
			}
			if attempt+1 >= maxResolveAttempts {
				// 病态上游持续返回不覆盖本客户端的范围且结果无法
				// 缓存（TTL=0）时的兜底：返回最近一次应答。
				return g.result, nil
			}
			continue // 未被覆盖：独立重新查询（通常命中刚写入的缓存）
		}
		// 成为代表：登记合并组后在锁外调用上游。
		g := &group{done: make(chan struct{}), repAddr: addr}
		c.groups[gk] = g
		c.mu.Unlock()

		resp, uerr := c.upstream.Resolve(ctx, Request{
			Name:            name,
			Type:            q.Type,
			ClientAddress:   addr,
			SourcePrefixLen: q.SourcePrefixLen,
		})

		c.mu.Lock()
		now = c.clock()
		switch {
		case uerr != nil:
			g.err = fmt.Errorf("%w: %w", ErrUpstream, uerr)
		case !validResponse(resp, fam):
			g.err = fmt.Errorf("%w: invalid response (ttl=%d scope=%d)", ErrUpstream, resp.TTL, resp.Scope)
		default:
			g.scope = resp.Scope
			g.result = resp.Result
			if resp.TTL > 0 {
				// 有效适用范围 = min(声明范围, 源前缀长度)。
				c.storeLocked(now, name, q.Type, fam, addr,
					min(resp.Scope, q.SourcePrefixLen), resp.Result, resp.TTL)
			}
		}
		close(g.done)
		delete(c.groups, gk)
		c.mu.Unlock()

		if g.err != nil {
			return Result{}, g.err
		}
		// 代表自身的地址必然被应答覆盖。
		return g.result, nil
	}
}

// storeLocked 写入一份应答。同键整体覆盖并重新计时；超过容量 K 时按
// “剩余有效期最短、适用范围更长、写入更早”的次序淘汰。调用方须持有锁。
func (c *Cache) storeLocked(now time.Time, name string, typ uint16, fam int,
	addr []byte, scope int, res Result, ttl int) {
	bk := bucketKey{name: name, typ: typ}
	b := c.buckets[bk]
	if b == nil {
		b = &bucket{entries: make(map[entryKey]*entry)}
		c.buckets[bk] = b
	}
	b.purgeExpired(now)
	c.seq++
	masked := maskAddr(addr, scope)
	key := entryKey{family: fam, scope: scope, prefix: string(masked)}
	b.entries[key] = &entry{
		family:    fam,
		prefix:    masked,
		scope:     scope,
		result:    res,
		expiresAt: now.Add(time.Duration(ttl) * time.Second),
		seq:       c.seq,
	}
	for len(b.entries) > c.k {
		b.evictOne()
	}
}

// purgeExpired 删除已到期条目（到期时刻本身已失效，左闭右开）。
func (b *bucket) purgeExpired(now time.Time) {
	for k, e := range b.entries {
		if !now.Before(e.expiresAt) {
			delete(b.entries, k)
		}
	}
}

// lookup 在同名字同类型的桶内为客户端地址选取条目：未过期、同地址族、
// 前缀确实覆盖该地址，若有多个取适用范围最长者。开销只随地址位数与 K
// 变化，与缓存总条目数无关。
func (b *bucket) lookup(fam int, addr []byte, now time.Time) *entry {
	var best *entry
	for _, e := range b.entries {
		if e.family != fam {
			continue
		}
		if !now.Before(e.expiresAt) {
			continue
		}
		if !prefixEqual(e.prefix, addr, e.scope) {
			continue
		}
		if best == nil || e.scope > best.scope {
			best = e
		}
	}
	return best
}

// evictOne 淘汰一个条目：剩余有效期最短者优先；并列时适用范围更长者优先；
// 再并列时更早写入者优先。
func (b *bucket) evictOne() {
	var victim entryKey
	var ve *entry
	for k, e := range b.entries {
		if ve == nil || evictBefore(e, ve) {
			victim, ve = k, e
		}
	}
	delete(b.entries, victim)
}

// evictBefore 报告 a 是否应比 b 先被淘汰。
func evictBefore(a, b *entry) bool {
	if !a.expiresAt.Equal(b.expiresAt) {
		return a.expiresAt.Before(b.expiresAt) // 剩余有效期更短
	}
	if a.scope != b.scope {
		return a.scope > b.scope // 适用范围更长
	}
	return a.seq < b.seq // 写入更早
}

// validateQuery 校验查询参数，返回地址族（地址字节数）。
func validateQuery(q Query) (int, error) {
	var maxBits int
	switch len(q.ClientAddress) {
	case FamilyIPv4:
		maxBits = 32
	case FamilyIPv6:
		maxBits = 128
	default:
		return 0, fmt.Errorf("%w: client address must be 4 or 16 bytes, got %d",
			ErrInvalidArgument, len(q.ClientAddress))
	}
	if q.SourcePrefixLen < 0 || q.SourcePrefixLen > maxBits {
		return 0, fmt.Errorf("%w: source prefix length %d out of range [0, %d]",
			ErrInvalidArgument, q.SourcePrefixLen, maxBits)
	}
	if q.Name == "" {
		return 0, fmt.Errorf("%w: empty name", ErrInvalidArgument)
	}
	return len(q.ClientAddress), nil
}

// validResponse 校验上游应答的合法性；非法应答按上游失败处理。
func validResponse(r Response, fam int) bool {
	if r.TTL < 0 || r.TTL > MaxTTLSeconds {
		return false
	}
	if r.Scope < 0 || r.Scope > fam*8 {
		return false
	}
	if r.Result.Kind < KindRecords || r.Result.Kind > KindNoData {
		return false
	}
	return true
}

// normalizeName 规范化名字：去掉一个末尾的点并不区分大小写。
func normalizeName(name string) string {
	name = strings.TrimSuffix(name, ".")
	return strings.ToLower(name)
}

// maskAddr 返回将 addr 第 bits 位之后的低位清零后的副本。
func maskAddr(addr []byte, bits int) []byte {
	out := make([]byte, len(addr))
	full := bits / 8
	copy(out, addr[:full])
	if rem := uint(bits % 8); rem != 0 {
		out[full] = addr[full] & (0xff << (8 - rem))
	}
	return out
}

// prefixEqual 比较 a 与 b 的前 bits 位是否相同。a、b 长度须一致且
// bits 不超过其位数。
func prefixEqual(a, b []byte, bits int) bool {
	full := bits / 8
	for i := 0; i < full; i++ {
		if a[i] != b[i] {
			return false
		}
	}
	if rem := uint(bits % 8); rem != 0 {
		mask := byte(0xff << (8 - rem))
		return a[full]&mask == b[full]&mask
	}
	return true
}

package ontology

import (
	"sync"
	"time"
)

// maxTTLSeconds 是应答生存时间的硬上限（7 天，单位秒）。
const maxTTLSeconds = 604800

// entry 是一条按(名字,类型,族,前缀,范围长度)缓存的结果。
type entry struct {
	kind     Kind
	records  []byte
	deadline time.Time // 失效时刻；该时刻本身已失效（左闭右开）
	writeSeq int64     // 本次（含覆盖写入）的全局单调写入序号
}

// bucket 承载同一(名字,类型)下所有族、所有范围的条目；条目数上限为 K。
type bucket struct {
	entries map[prefixKey]*entry
}

// flightWaiter 是挂在某个在途上游查询上的一个并发等待者。
type flightWaiter struct {
	client Addr // 该等待者的原始客户端地址（用于按上游原始范围判定是否被覆盖）
	notify chan flightOutcome
}

type flightOutcome struct {
	res     Result
	err     error
	covered bool // false 时该等待者须独立重新进入完整查询流程
}

// flight 表示一次在途的上游查询及其全部等待者。
type flight struct {
	waiters []*flightWaiter
}

// Cache 是支持客户端子网扩展的递归解析缓存。
type Cache struct {
	mu       sync.Mutex
	k        int
	clock    Clock
	upstream Upstream
	buckets  map[nameKey]*bucket
	inFlight map[prefixKey]*flight
	seq      int64
}

// NewCache 创建缓存；k 为同一(名字,类型)下的有效条目数上限（k==0 表示不缓存）。
func NewCache(k int, clock Clock, upstream Upstream) *Cache {
	return &Cache{
		k:        k,
		clock:    clock,
		upstream: upstream,
		buckets:  make(map[nameKey]*bucket),
		inFlight: make(map[prefixKey]*flight),
	}
}

// validateQuery 校验调用参数；任何不合法都在触碰缓存/上游之前返回参数非法。
func validateQuery(q Query) error {
	nb, mb := familyWidth(q.Family)
	if nb == 0 || len(q.Client) != nb {
		return ErrInvalidArgument
	}
	if q.SrcPrefix < 0 || q.SrcPrefix > mb {
		return ErrInvalidArgument
	}
	return nil
}

// validateAnswer 校验上游应答是否符合约定；不符合约定按上游失败处理。
func validateAnswer(a Answer, maxBits int) error {
	if a.Kind != KindRecords && a.Kind != KindNoName && a.Kind != KindNoType {
		return ErrUpstreamFailure
	}
	if a.TTL < 0 || a.TTL > maxTTLSeconds {
		return ErrUpstreamFailure
	}
	if a.ScopePrefix < 0 || a.ScopePrefix > maxBits {
		return ErrUpstreamFailure
	}
	return nil
}

func (c *Cache) bucketLocked(k nameKey) *bucket {
	b := c.buckets[k]
	if b == nil {
		b = &bucket{entries: make(map[prefixKey]*entry)}
		c.buckets[k] = b
	}
	return b
}

// purgeExpired 删除桶内已到期条目（含恰到期）。惰性执行：任何命中判定前都调用，
// 因而过期条目在任何查询到达时都不可能被当作有效。
func (b *bucket) purgeExpired(now time.Time) {
	for pk, e := range b.entries {
		if !now.Before(e.deadline) {
			delete(b.entries, pk)
		}
	}
}

// lookup 在桶内选取覆盖 client 的最长未过期范围条目；无则返回 nil。
// 族不匹配的条目不参与比较；开销仅与桶内条目数（同名字同类型，上限 K）有关。
func (b *bucket) lookup(family AddrFamily, client Addr, now time.Time) (prefixKey, *entry) {
	var bestKey prefixKey
	var best *entry
	bestBits := -1
	for pk, e := range b.entries {
		if now.Before(e.deadline) && pk.family == family &&
			prefixCovers(Addr(pk.prefix), pk.bits, client) && pk.bits > bestBits {
			bestKey, best, bestBits = pk, e, pk.bits
		}
	}
	return bestKey, best
}

// insert 写入或整体覆盖一条目，并执行容量淘汰。TTL 为 0 的条目不写入。
func (c *Cache) insertLocked(pk prefixKey, a Answer, now time.Time) {
	if a.TTL == 0 || c.k <= 0 {
		return
	}
	b := c.bucketLocked(pk.name)
	b.purgeExpired(now)

	c.seq++
	records := append([]byte(nil), a.Records...)
	b.entries[pk] = &entry{
		kind:     a.Kind,
		records:  records,
		deadline: now.Add(time.Duration(a.TTL) * time.Second),
		writeSeq: c.seq,
	}

	for len(b.entries) > c.k {
		var victim prefixKey
		var victimE *entry
		for pk2, e := range b.entries {
			if victimE == nil || lessForEviction(pk2, e, victim, victimE) {
				victim, victimE = pk2, e
			}
		}
		delete(b.entries, victim)
	}
}

// lessForEviction 判定候选 x 是否应比当前 y 更早被淘汰：
//  1. 剩余有效期更短者先淘汰（deadline 更早）；
//  2. deadline 并列时，适用范围更长者先淘汰；
//  3. 仍并列时，写入更早者先淘汰（writeSeq 更小）。
func lessForEviction(xk prefixKey, x *entry, yk prefixKey, y *entry) bool {
	if !x.deadline.Equal(y.deadline) {
		return x.deadline.Before(y.deadline)
	}
	if xk.bits != yk.bits {
		return xk.bits > yk.bits
	}
	return x.writeSeq < y.writeSeq
}

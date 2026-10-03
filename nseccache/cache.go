package nseccache

import (
	"container/heap"
	"errors"
	"sync"
)

var (
	// ErrInvalidParam 参数非法（名字、类型集合、TTL、时间或 qtype 越界）。
	ErrInvalidParam = errors.New("nseccache: invalid parameter")
	// ErrClockRewind now 小于上一次被接受操作的 now。
	ErrClockRewind = errors.New("nseccache: clock rewind")
	// ErrNotValidated Insert 的记录未通过验证。
	ErrNotValidated = errors.New("nseccache: record not validated")
	// ErrOutOfZone 名字不在区域内。
	ErrOutOfZone = errors.New("nseccache: name out of zone")
)

const (
	maxTime   = 1_000_000_000_000
	maxTTL    = 86400
	maxCap    = 4096
	typeNSEC  = uint16(47)
	typeCNAME = uint16(5)
	typeSOA   = uint16(6)
	typeNS    = uint16(2)
)

// Record 是一条 NSEC 记录。
type Record struct {
	Owner     string
	Next      string
	Types     map[uint16]bool
	TTL       int
	Validated bool
}

// ResultKind 表示否定应答的判定结果。
type ResultKind int

const (
	// ResultMiss 无法给出否定结论（名字/类型可能存在）。
	ResultMiss ResultKind = iota
	// ResultNXDomain 名字不存在。
	ResultNXDomain
	// ResultNoData 名字存在但该类型不存在。
	ResultNoData
)

// Result 是一次 Lookup 的结果。Used 按 Owner 规范序升序。
type Result struct {
	Kind ResultKind
	Used []Record
	TTL  int
}

// Cache 是 DNSSEC 聚合 NSEC 否定应答缓存。
type Cache struct {
	mu     sync.Mutex
	apex   canonName
	soaMin int
	cap    int
	now    int
	hasNow bool

	byOwner map[string]*entry
	tree    *tree
	expHeap expHeap
	cmpCnt  int64
}

// New 构造一个缓存。zone 非空；soaMin 范围 0..86400；N 范围 1..4096。
func New(zone string, soaMin, N int) (*Cache, error) {
	apex, err := newCanonName(zone)
	if err != nil {
		return nil, ErrInvalidParam
	}
	if soaMin < 0 || soaMin > maxTTL || N < 1 || N > maxCap {
		return nil, ErrInvalidParam
	}
	c := &Cache{
		apex:    apex,
		soaMin:  soaMin,
		cap:     N,
		byOwner: make(map[string]*entry),
	}
	c.tree = &tree{}
	heap.Init(&c.expHeap)
	return c, nil
}

// Insert 插入或替换一条已验证 NSEC 记录。
func (c *Cache) Insert(now int, rec Record) error {
	// 参数校验优先于一切。
	if now < 0 || now > maxTime || rec.TTL < 0 || rec.TTL > maxTTL {
		return ErrInvalidParam
	}
	if rec.Types == nil || !rec.Types[typeNSEC] {
		return ErrInvalidParam
	}
	owner, err := newCanonName(rec.Owner)
	if err != nil {
		return ErrInvalidParam
	}
	next, err := newCanonName(rec.Next)
	if err != nil {
		return ErrInvalidParam
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if c.hasNow && now < c.now {
		return ErrClockRewind
	}
	if !rec.Validated {
		return ErrNotValidated
	}
	if !isInZone(owner, c.apex) || !isInZone(next, c.apex) {
		return ErrOutOfZone
	}

	c.sweep(now)
	c.now = now
	c.hasNow = true

	key := owner.text
	existing := c.byOwner[key]

	eff := rec.TTL
	if c.soaMin < eff {
		eff = c.soaMin
	}

	// 先清除同 Owner 旧记录（eff 为 0 也清除，且不存储）。
	if existing != nil {
		c.removeEntry(existing)
		existing = nil
	}
	if eff == 0 {
		return nil
	}

	if len(c.byOwner) >= c.cap {
		c.evictOne()
	}

	types := make(map[uint16]bool, len(rec.Types))
	for t := range rec.Types {
		types[t] = true
	}
	e := &entry{
		owner: owner,
		next:  next,
		types: types,
		ttl:   rec.TTL,
		exp:   now + eff,
		rec:   rec,
	}
	e.rec.Types = types
	c.tree.insert(e)
	c.byOwner[key] = e
	heap.Push(&c.expHeap, e)
	return nil
}

// Lookup 合成否定应答。
func (c *Cache) Lookup(now int, qname string, qtype uint16) (Result, error) {
	if now < 0 || now > maxTime || qtype < 1 || qtype > 65535 {
		return Result{}, ErrInvalidParam
	}
	qn, err := newCanonName(qname)
	if err != nil {
		return Result{}, ErrInvalidParam
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if c.hasNow && now < c.now {
		return Result{}, ErrClockRewind
	}
	if !isInZone(qn, c.apex) {
		return Result{}, ErrOutOfZone
	}

	c.sweep(now)
	c.now = now
	c.hasNow = true

	// Lookup 的名字比较由 tree 上的计数器记录。
	savedCmp := c.tree.cmp
	c.tree.cmp = &c.cmpCnt
	defer func() { c.tree.cmp = savedCmp }()

	// （一）Owner 精确匹配
	if e := c.byOwner[qn.text]; e != nil {
		if e.types[qtype] || (qtype != typeCNAME && e.types[typeCNAME]) {
			return Result{Kind: ResultMiss}, nil
		}
		return Result{Kind: ResultNoData, Used: []Record{e.rec}, TTL: e.exp - now}, nil
	}

	// （二）覆盖 qname 的记录
	r := c.tree.findCover(qn)
	if r == nil {
		return Result{Kind: ResultMiss}, nil
	}

	// （三）空非终结符判定
	k := commonSuffix(r.owner, qn)
	if ks := commonSuffix(r.next, qn); ks > k {
		k = ks
	}
	if k == len(qn.labels) {
		return Result{Kind: ResultNoData, Used: []Record{r.rec}, TTL: r.exp - now}, nil
	}

	// （四）最近封闭者与通配符否定
	ce := suffixName(qn, k)
	wild := wildcardName(ce)

	if we := c.byOwner[wild.text]; we != nil {
		return Result{Kind: ResultMiss}, nil
	}
	wcover := c.tree.findCover(wild)
	if wcover == nil {
		return Result{Kind: ResultMiss}, nil
	}

	usedEntries := []*entry{r}
	if wcover != r {
		usedEntries = append(usedEntries, wcover)
	}
	sortEntriesByOwner(usedEntries)

	used := make([]Record, len(usedEntries))
	ttl := -1
	for i, e := range usedEntries {
		used[i] = e.rec
		rem := e.exp - now
		if ttl < 0 || rem < ttl {
			ttl = rem
		}
	}
	return Result{Kind: ResultNXDomain, Used: used, TTL: ttl}, nil
}

// NameCmp 返回 Lookup 累计的名字规范序比较次数（Insert 不计入）。
func (c *Cache) NameCmp() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return int(c.cmpCnt)
}

// Len 返回当前存活记录数（含过期摊还）。
func (c *Cache) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.byOwner)
}

// sweep 在每次被接受操作开头按到期时刻摊还清除过期记录（now >= exp）。
// 堆中的陈旧元素（已被替换/淘汰）会被惰性跳过。
func (c *Cache) sweep(now int) {
	for c.expHeap.Len() > 0 {
		top := c.expHeap[0]
		if top.exp > now {
			return
		}
		heap.Pop(&c.expHeap)
		cur, ok := c.byOwner[top.owner.text]
		if !ok || cur != top {
			continue // 陈旧堆元素
		}
		c.removeEntry(cur)
	}
}

func (c *Cache) removeEntry(e *entry) {
	if n := c.tree.find(e.owner); n != nil && n.entry == e {
		c.tree.delete(n)
	}
	delete(c.byOwner, e.owner.text)
}

// evictOne 淘汰到期时刻最小者；并列取 Owner 规范序较小者。
func (c *Cache) evictOne() {
	for c.expHeap.Len() > 0 {
		top := heap.Pop(&c.expHeap).(*entry)
		if cur, ok := c.byOwner[top.owner.text]; ok && cur == top {
			c.removeEntry(cur)
			return
		}
	}
}

func sortEntriesByOwner(es []*entry) {
	for i := 1; i < len(es); i++ {
		for j := i; j > 0 && compareCanon(es[j].owner, es[j-1].owner) < 0; j-- {
			es[j], es[j-1] = es[j-1], es[j]
		}
	}
}

// ---- 到期最小堆：exp 升序，并列 owner 规范序升序 ----

type expHeap []*entry

func (h expHeap) Len() int { return len(h) }

func (h expHeap) Less(i, j int) bool {
	if h[i].exp != h[j].exp {
		return h[i].exp < h[j].exp
	}
	return compareCanon(h[i].owner, h[j].owner) < 0
}

func (h expHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }

func (h *expHeap) Push(x any) { *h = append(*h, x.(*entry)) }

func (h *expHeap) Pop() any {
	old := *h
	n := len(old)
	it := old[n-1]
	old[n-1] = nil
	*h = old[:n-1]
	return it
}

package cookie

import (
	"container/heap"
	"log"
	"os"
	"strings"
	"sync"
)

const hostPrefix = "__Host-"

// site 持有单站点的全部索引：键索引、按路径登记、LRU 堆与过期堆。
type site struct {
	domain string
	byKey  map[Key]*entry
	paths  *trieNode
	lru    lruHeap
	expiry expiryHeap
	count  *siteCount
}

// trieNode 是按路径段组织的登记树，保证附带判定只触及目标路径祖先段。
type trieNode struct {
	segment  string
	children map[string]*trieNode
	entries  map[Key]*entry
}

func newTrie() *trieNode {
	return &trieNode{segment: "", children: map[string]*trieNode{}, entries: map[Key]*entry{}}
}

// pathSegments 拆分路径为段列表；根路径对应空段列表。
func pathSegments(p string) []string {
	if p == "/" {
		return nil
	}
	parts := strings.Split(p, "/")
	return parts[1:] // p 以 '/' 开头，首元素为空
}

func (t *trieNode) insert(p string, key Key, e *entry) {
	node := t
	for _, seg := range pathSegments(p) {
		next := node.children[seg]
		if next == nil {
			next = &trieNode{segment: seg, children: map[string]*trieNode{}, entries: map[Key]*entry{}}
			node.children[seg] = next
		}
		node = next
	}
	node.entries[key] = e
}

func (t *trieNode) remove(p string, key Key) {
	node := t
	for _, seg := range pathSegments(p) {
		next := node.children[seg]
		if next == nil {
			return
		}
		node = next
	}
	delete(node.entries, key)
}

// walkAncestors 沿目标路径段下探，在根与每个存在的祖先节点上调用 fn。
// 访问的节点数仅取决于目标路径深度，与条目总数无关。
func (t *trieNode) walkAncestors(targetPath string, fn func(map[Key]*entry)) {
	fn(t.entries)
	node := t
	for _, seg := range pathSegments(targetPath) {
		next := node.children[seg]
		if next == nil {
			return
		}
		node = next
		// 登记路径以斜杠结尾（空段子节点）时，其条目对任意更深目标路径可见。
		if slashChild := node.children[""]; slashChild != nil {
			fn(slashChild.entries)
		}
		fn(node.entries)
	}
}

// Kernel 是并发安全的 Cookie 存储内核。
type Kernel struct {
	mu       sync.Mutex
	cfg      Config
	now      int64
	clockSet bool
	sites    map[string]*site
	entries  int
	gblExp   gblExpiryHeap
	siteCnt  siteCountHeap

	totalAdded      int64
	expiryEvicted   int64
	capacityEvicted int64
	explicitDelete  int64

	// pathProbes 统计最近一次附带判定逐条检验的候选条目数，
	// 用于可验证地证明开销不随其他站点条目总数增长。
	pathProbes int

	log Logger
}

// New 按配置构造内核；容量参数必须为正、宽限时长必须非负。
func New(cfg Config, logger Logger) (*Kernel, error) {
	if cfg.PerSiteLimit <= 0 {
		return nil, &InvalidArgError{"PerSiteLimit must be positive"}
	}
	if cfg.GlobalLimit <= 0 {
		return nil, &InvalidArgError{"GlobalLimit must be positive"}
	}
	if cfg.LaxGrace < 0 {
		return nil, &InvalidArgError{"LaxGrace must be non-negative"}
	}
	if logger == nil {
		logger = log.New(os.Stderr, "cookie: ", log.LstdFlags|log.Lmicroseconds)
	}
	return &Kernel{
		cfg:   cfg,
		sites: make(map[string]*site),
		log:   logger,
	}, nil
}

// checkClock 在参数校验之后校验时钟单调性；拒绝操作不改变任何状态。
func (k *Kernel) checkClock(now int64) error {
	if k.clockSet && now < k.now {
		return ErrClockRollback
	}
	return nil
}

func validateWrite(in WriteInput) error {
	if in.Name == "" {
		return ErrEmptyName
	}
	if in.Domain == "" || in.SourceDomain == "" {
		return ErrEmptyDomain
	}
	if in.Path == "" || in.Path[0] != '/' {
		return ErrBadPath
	}
	switch in.SameSite {
	case SameSiteStrict, SameSiteLax, SameSiteNone, "":
	default:
		return ErrUnknownSameSite
	}
	return nil
}

// Write 执行结构化写入。返回最终存活条目快照；过期写入时 ok 为 false。
func (k *Kernel) Write(in WriteInput) (out Entry, ok bool, err error) {
	k.mu.Lock()
	defer k.mu.Unlock()

	if err = validateWrite(in); err != nil {
		k.log.Printf("WRITE REJECTED input=%+v reason=%v", in, err)
		return Entry{}, false, err
	}
	if err = k.checkClock(in.Now); err != nil {
		k.log.Printf("WRITE REJECTED input=%+v reason=%v", in, err)
		return Entry{}, false, err
	}

	if strings.HasPrefix(in.Name, hostPrefix) {
		if !in.Secure || in.Path != "/" || in.Domain != in.SourceDomain {
			err = ErrHostPrefixViolation
			k.log.Printf("WRITE REJECTED input=%+v reason=%v", in, err)
			return Entry{}, false, err
		}
	}
	if in.Secure && !in.SourceSecure {
		err = ErrInsecureSource
		k.log.Printf("WRITE REJECTED input=%+v reason=%v", in, err)
		return Entry{}, false, err
	}
	if in.SameSite == SameSiteNone && !in.Secure {
		err = ErrSameSiteNoneInsecure
		k.log.Printf("WRITE REJECTED input=%+v reason=%v", in, err)
		return Entry{}, false, err
	}

	k.now = in.Now
	k.clockSet = true
	key := Key{Name: in.Name, Domain: in.Domain, Path: in.Path, Partition: in.Partition}

	// 过期时刻不晚于当前时刻：删除同键条目；不是拒绝，也不是新增。
	if in.Expires != nil && *in.Expires <= in.Now {
		removed := false
		if s, exists := k.sites[in.Domain]; exists {
			if _, has := s.byKey[key]; has {
				k.removeEntry(s, key)
				removed = true
				k.explicitDelete++
			}
		}
		k.log.Printf("WRITE expire-delete key=%+v removed=%v now=%d", key, removed, in.Now)
		return Entry{}, false, nil
	}

	s := k.sites[in.Domain]
	if s == nil {
		s = k.newSite(in.Domain)
	}

	created := in.Now
	overwrite := false
	if old, exists := s.byKey[key]; exists {
		created = old.createdAt
		// 覆盖：先从全部索引摘除旧条目，但保留站点壳与计数，随后由新条目重新计入。
		delete(s.byKey, key)
		k.unindexPath(s, key)
		heap.Remove(&s.lru, old.lruIndex)
		k.removeFromExpiryHeaps(s, old)
		old.lruIndex, old.expiryIndex, old.gblExpIndex = -1, -1, -1
		// 复用标准计数收敛；若站点归零壳被删除，下面重新取壳插入新条目。
		k.decSiteCount(s)
		if cur := k.sites[in.Domain]; cur != nil {
			s = cur
		} else {
			s = k.newSite(in.Domain)
		}
		overwrite = true
	}

	e := &entry{
		key:         key,
		value:       in.Value,
		secure:      in.Secure,
		httpOnly:    in.HTTPOnly,
		sameSite:    in.SameSite,
		expires:     clonePtr(in.Expires),
		createdAt:   created,
		lastAccess:  in.Now,
		lruIndex:    -1,
		expiryIndex: -1,
		gblExpIndex: -1,
	}
	k.insertEntry(s, e)
	if !overwrite {
		k.totalAdded++
	}

	if !overwrite {
		s = k.sites[in.Domain] // 丢弃可能存在的陈旧站点壳引用
		// 站点上限先于全局上限：两者都先清过期，再按 LRU 淘汰。
		k.purgeSiteExpired(s, in.Now)
		for s.count.count > k.cfg.PerSiteLimit {
			k.evictSiteLRU(s)
		}
		k.purgeGlobalExpired(in.Now)
		for k.entries > k.cfg.GlobalLimit {
			if k.gblExp.Len() > 0 && *k.gblExp[0].e.expires <= in.Now {
				k.purgeGlobalExpired(in.Now)
				continue
			}
			k.evictGlobal()
		}
	}

	if cur, alive := s.byKey[key]; alive {
		out, ok = cur.snapshot(), true
	}
	k.log.Printf("WRITE OK input=%+v overwrite=%v alive=%v result=%+v", in, overwrite, ok, out)
	return out, ok, nil
}

func (k *Kernel) newSite(domain string) *site {
	if existing, ok := k.sites[domain]; ok {
		return existing
	}
	s := &site{
		domain: domain,
		byKey:  make(map[Key]*entry),
		paths:  newTrie(),
	}
	sc := &siteCount{domain: domain, idx: -1}
	s.count = sc
	heap.Push(&k.siteCnt, sc)
	k.sites[domain] = s
	return s
}

func (k *Kernel) insertEntry(s *site, e *entry) {
	s.byKey[e.key] = e
	s.paths.insert(e.key.Path, e.key, e)
	heap.Push(&s.lru, e)
	if e.expires != nil {
		heap.Push(&s.expiry, e)
		heap.Push(&k.gblExp, gblExpiry{e: e, domain: s.domain})
	}
	s.count.count++
	heap.Fix(&k.siteCnt, s.count.idx)
	k.entries++
}

// removeEntry 从全部索引摘除键对应条目；不修改任何删除计数。
func (k *Kernel) removeEntry(s *site, key Key) bool {
	e, ok := s.byKey[key]
	if !ok {
		return false
	}
	delete(s.byKey, key)
	k.unindexPath(s, key)
	heap.Remove(&s.lru, e.lruIndex)
	k.removeFromExpiryHeaps(s, e)
	k.decSiteCount(s)
	return true
}

func (k *Kernel) unindexPath(s *site, key Key) {
	s.paths.remove(key.Path, key)
}

// removeFromExpiryHeaps 从站点与全局过期堆摘除条目（仅对仍在堆中的索引生效）。
func (k *Kernel) removeFromExpiryHeaps(s *site, e *entry) {
	if e.expires == nil {
		return
	}
	if e.expiryIndex >= 0 && e.expiryIndex < s.expiry.Len() && s.expiry[e.expiryIndex] == e {
		heap.Remove(&s.expiry, e.expiryIndex)
	}
	if e.gblExpIndex >= 0 && e.gblExpIndex < k.gblExp.Len() && k.gblExp[e.gblExpIndex].e == e {
		heap.Remove(&k.gblExp, e.gblExpIndex)
	}
}

func (k *Kernel) decSiteCount(s *site) {
	if cur := k.sites[s.domain]; cur != nil && cur != s {
		s = cur
	}
	s.count.count--
	k.entries--
	if s.count.count == 0 {
		k.removeSiteCountNode(s.domain)
		delete(k.sites, s.domain)
	} else {
		k.fixSiteCount(s.domain)
	}
	// 防御：任何情况下 map 中不应残留空站点，堆中不应残留其计数节点。
	if _, alive := k.sites[s.domain]; !alive {
		k.removeSiteCountNode(s.domain)
	}
}

// removeSiteCountNode 按 domain 摘除站点计数节点，对陈旧 idx 免疫。
func (k *Kernel) removeSiteCountNode(domain string) {
	for i, node := range k.siteCnt {
		if node.domain == domain {
			k.siteCnt = append(k.siteCnt[:i], k.siteCnt[i+1:]...)
			k.rebuildSiteCount()
			return
		}
	}
}

func (k *Kernel) fixSiteCount(domain string) {
	for i, node := range k.siteCnt {
		if node.domain == domain && i == node.idx {
			heap.Fix(&k.siteCnt, i)
			return
		}
	}
	k.rebuildSiteCount()
}

func (k *Kernel) rebuildSiteCount() {
	k.siteCnt = siteCountHeap{}
	for domain, s := range k.sites {
		sc := s.count
		sc.domain = domain
		sc.idx = -1
		heap.Push(&k.siteCnt, sc)
	}
}

// purgeSiteExpired 移除该站点过期时刻 <= now 的条目，计入过期淘汰。
func (k *Kernel) purgeSiteExpired(s *site, now int64) int {
	removed := 0
	for s.expiry.Len() > 0 && *s.expiry[0].expires <= now {
		if cur := k.sites[s.domain]; cur != nil {
			s = cur
		}
		e := heap.Pop(&s.expiry).(*entry)
		k.removeEntryNoExpiry(s, e.key)
		k.expiryEvicted++
		removed++
	}
	k.rebuildGlobalExpiry()
	return removed
}

func (k *Kernel) rebuildGlobalExpiry() {
	k.gblExp = gblExpiryHeap{}
	for domain, s := range k.sites {
		for _, e := range s.expiry {
			e.gblExpIndex = -1
			heap.Push(&k.gblExp, gblExpiry{e: e, domain: domain})
		}
	}
}

// removeEntryNoExpiry 用于条目已从站点过期堆弹出的场景。
func (k *Kernel) removeEntryNoExpiry(s *site, key Key) {
	if cur := k.sites[s.domain]; cur != nil {
		s = cur
	}
	e, ok := s.byKey[key]
	if !ok {
		return
	}
	delete(s.byKey, key)
	k.unindexPath(s, key)
	heap.Remove(&s.lru, e.lruIndex)
	k.decSiteCount(s)
}

// purgeGlobalExpired 清掉所有站点中已过期条目，计入过期淘汰。
func (k *Kernel) purgeGlobalExpired(now int64) int {
	removed := 0
	for k.gblExp.Len() > 0 && *k.gblExp[0].e.expires <= now {
		node := heap.Pop(&k.gblExp).(gblExpiry)
		s := k.sites[node.domain]
		if s == nil {
			continue
		}
		if _, ok := s.byKey[node.e.key]; !ok {
			continue
		}
		heap.Remove(&s.expiry, node.e.expiryIndex)
		k.removeEntryNoExpiry(s, node.e.key)
		k.expiryEvicted++
		removed++
	}
	return removed
}

// evictSiteLRU 淘汰该站点 LRU 栈顶（最早访问，并列最早创建）。
func (k *Kernel) evictSiteLRU(s *site) {
	if cur := k.sites[s.domain]; cur != nil {
		s = cur
	}
	before := s.lru.compares
	e := heap.Pop(&s.lru).(*entry)
	k.removeEntryNoLRU(s, e.key)
	k.capacityEvicted++
	k.log.Printf("EVICT site=%s key=%+v lastAccess=%d createdAt=%d heapCompares=%d",
		s.domain, e.key, e.lastAccess, e.createdAt, s.lru.compares-before)
}

// removeEntryNoLRU 用于条目已从 LRU 堆弹出的场景。
func (k *Kernel) removeEntryNoLRU(s *site, key Key) {
	if cur := k.sites[s.domain]; cur != nil {
		s = cur
	}
	e, ok := s.byKey[key]
	if !ok {
		return
	}
	delete(s.byKey, key)
	k.unindexPath(s, key)
	k.removeFromExpiryHeaps(s, e)
	k.decSiteCount(s)
}

// evictGlobal 从条目数最多的站点（并列名字序）淘汰其 LRU 条目。
func (k *Kernel) evictGlobal() {
	s := k.sites[k.siteCnt[0].domain]
	k.evictSiteLRU(s)
}

// Clear 按站点 / 分区键 / 创建时间区间（左闭右开）清除，计入显式删除。
func (k *Kernel) Clear(in ClearInput) (int, error) {
	k.mu.Lock()
	defer k.mu.Unlock()

	if in.Domain == "" {
		err := ErrEmptyDomain
		k.log.Printf("CLEAR REJECTED input=%+v reason=%v", in, err)
		return 0, err
	}
	if err := k.checkClock(in.Now); err != nil {
		k.log.Printf("CLEAR REJECTED input=%+v reason=%v", in, err)
		return 0, err
	}
	k.now = in.Now
	k.clockSet = true

	s := k.sites[in.Domain]
	if s == nil {
		k.log.Printf("CLEAR OK domain=%s removed=0", in.Domain)
		return 0, nil
	}

	keys := make([]Key, 0)
	for key, e := range s.byKey {
		if in.UsePartition && e.key.Partition != in.Partition {
			continue
		}
		if in.UseCreatedRange && (e.createdAt < in.CreatedFrom || e.createdAt >= in.CreatedTo) {
			continue
		}
		keys = append(keys, key)
	}
	for _, key := range keys {
		if cur, ok := k.sites[in.Domain]; ok {
			if k.removeEntry(cur, key) {
				k.explicitDelete++
			}
		}
	}
	k.log.Printf("CLEAR OK input=%+v removed=%d", in, len(keys))
	return len(keys), nil
}

// Advance 推进逻辑时钟；回退是可区分错误且不改变任何状态。
func (k *Kernel) Advance(now int64) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	if err := k.checkClock(now); err != nil {
		k.log.Printf("ADVANCE REJECTED now=%d reason=%v", now, err)
		return err
	}
	prev := k.now
	k.now = now
	k.log.Printf("ADVANCE OK %d -> %d", prev, now)
	return nil
}

// Stats 返回三类可区分的删除计数、累计新增与当前条目数。
func (k *Kernel) Stats() Stats {
	k.mu.Lock()
	defer k.mu.Unlock()
	return Stats{
		TotalAdded:      k.totalAdded,
		CurrentCount:    k.entries,
		ExpiryEvicted:   k.expiryEvicted,
		CapacityEvicted: k.capacityEvicted,
		ExplicitDelete:  k.explicitDelete,
		Now:             k.now,
	}
}

// Invariant 校验 累计新增 = 过期淘汰 + 容量淘汰 + 显式删除 + 当前条目。
func (k *Kernel) Invariant() bool {
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.totalAdded == k.expiryEvicted+k.capacityEvicted+k.explicitDelete+int64(k.entries)
}

// lruCompares 返回某站点 LRU 堆累计比较次数（性能可验证）。
func (k *Kernel) lruCompares(domain string) int64 {
	k.mu.Lock()
	defer k.mu.Unlock()
	if s, ok := k.sites[domain]; ok {
		return s.lru.compares
	}
	return 0
}

// lastPathProbes 返回最近一次附带判定逐条检验的候选条目数。
func (k *Kernel) lastPathProbes() int {
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.pathProbes
}

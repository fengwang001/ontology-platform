package cookie

import "sort"

// naiveModel 是独立编写的朴素参考模型：用切片线性扫描实现全部语义，
// 不依赖内核的任何堆与索引，供随机操作序列对照。
type naiveModel struct {
	cfg      Config
	now      int64
	clockSet bool
	items    []Entry

	totalAdded      int64
	expiryEvicted   int64
	capacityEvicted int64
	explicitDelete  int64
}

func newNaive(cfg Config) *naiveModel {
	return &naiveModel{cfg: cfg}
}

type naiveKey = Key

func (m *naiveModel) checkClock(now int64) error {
	if m.clockSet && now < m.now {
		return ErrClockRollback
	}
	return nil
}

// find 返回同键条目下标，不存在为 -1。
func (m *naiveModel) find(k Key) int {
	for i := range m.items {
		if m.items[i].Domain == k.Domain && m.items[i].Name == k.Name &&
			m.items[i].Path == k.Path && m.items[i].Partition == k.Partition {
			return i
		}
	}
	return -1
}

func (m *naiveModel) removeAt(i int) {
	m.items = append(m.items[:i], m.items[i+1:]...)
}

// write 与 Kernel.Write 同语义：返回快照、是否存活、错误。
func (m *naiveModel) write(in WriteInput) (Entry, bool, error) {
	if err := validateWrite(in); err != nil {
		return Entry{}, false, err
	}
	if err := m.checkClock(in.Now); err != nil {
		return Entry{}, false, err
	}
	if hostPrefixedName(in.Name) {
		if !in.Secure || in.Path != "/" || in.Domain != in.SourceDomain {
			return Entry{}, false, ErrHostPrefixViolation
		}
	}
	if in.Secure && !in.SourceSecure {
		return Entry{}, false, ErrInsecureSource
	}
	if in.SameSite == SameSiteNone && !in.Secure {
		return Entry{}, false, ErrSameSiteNoneInsecure
	}

	m.now = in.Now
	m.clockSet = true
	key := Key{Name: in.Name, Domain: in.Domain, Path: in.Path, Partition: in.Partition}

	if in.Expires != nil && *in.Expires <= in.Now {
		if i := m.find(key); i >= 0 {
			m.removeAt(i)
			m.explicitDelete++
		}
		return Entry{}, false, nil
	}

	created := in.Now
	overwrite := false
	if i := m.find(key); i >= 0 {
		created = m.items[i].CreatedAt
		m.removeAt(i)
		overwrite = true
	}

	m.items = append(m.items, Entry{
		Name:       in.Name,
		Value:      in.Value,
		Domain:     in.Domain,
		Path:       in.Path,
		Secure:     in.Secure,
		HTTPOnly:   in.HTTPOnly,
		SameSite:   in.SameSite,
		Expires:    clonePtr(in.Expires),
		Partition:  in.Partition,
		CreatedAt:  created,
		LastAccess: in.Now,
	})
	if !overwrite {
		m.totalAdded++
	}

	if !overwrite {
		m.purgeExpiredInDomain(in.Domain, in.Now)
		m.evictDomainToLimit(in.Domain, in.Now)
		m.purgeAllExpired(in.Now)
		m.evictGlobalToLimit(in.Now)
	}

	if i := m.find(key); i >= 0 {
		return m.items[i], true, nil
	}
	return Entry{}, false, nil
}

func hostPrefixedName(name string) bool {
	return len(name) >= len(hostPrefix) && name[:len(hostPrefix)] == hostPrefix
}

// purgeExpiredInDomain 线性清掉某站点所有已过期条目。
func (m *naiveModel) purgeExpiredInDomain(domain string, now int64) int {
	kept := m.items[:0]
	removed := 0
	for _, e := range m.items {
		if e.Domain == domain && e.Expires != nil && *e.Expires <= now {
			m.expiryEvicted++
			removed++
			continue
		}
		kept = append(kept, e)
	}
	m.items = append([]Entry(nil), kept...)
	return removed
}

func (m *naiveModel) purgeAllExpired(now int64) int {
	kept := make([]Entry, 0, len(m.items))
	removed := 0
	for _, e := range m.items {
		if e.Expires != nil && *e.Expires <= now {
			m.expiryEvicted++
			removed++
			continue
		}
		kept = append(kept, e)
	}
	m.items = kept
	return removed
}

// lruLess 定义容量淘汰次序：最近访问最早、并列创建最早、再按键序。
func lruLess(a, b Entry) bool {
	if a.LastAccess != b.LastAccess {
		return a.LastAccess < b.LastAccess
	}
	if a.CreatedAt != b.CreatedAt {
		return a.CreatedAt < b.CreatedAt
	}
	return keyString(Key{Name: a.Name, Domain: a.Domain, Path: a.Path, Partition: a.Partition}) <
		keyString(Key{Name: b.Name, Domain: b.Domain, Path: b.Path, Partition: b.Partition})
}

func (m *naiveModel) domainCount(domain string) int {
	n := 0
	for _, e := range m.items {
		if e.Domain == domain {
			n++
		}
	}
	return n
}

func (m *naiveModel) evictOne(domain string, now int64) {
	idx := -1
	for i := range m.items {
		if m.items[i].Domain != domain {
			continue
		}
		if idx == -1 || lruLess(m.items[i], m.items[idx]) {
			idx = i
		}
	}
	if idx >= 0 {
		m.removeAt(idx)
		m.capacityEvicted++
	}
}

func (m *naiveModel) evictDomainToLimit(domain string, now int64) {
	for m.domainCount(domain) > m.cfg.PerSiteLimit {
		m.evictOne(domain, now)
	}
}

func (m *naiveModel) evictGlobalToLimit(now int64) {
	for len(m.items) > m.cfg.GlobalLimit {
		counts := map[string]int{}
		for _, e := range m.items {
			counts[e.Domain]++
		}
		var top string
		topN := -1
		domains := make([]string, 0, len(counts))
		for d := range counts {
			domains = append(domains, d)
		}
		sort.Strings(domains)
		for _, d := range domains {
			if counts[d] > topN {
				top, topN = d, counts[d]
			}
		}
		m.evictOne(top, now)
	}
}

// candidate 是朴素模型在读取路径上遇到的条目（含被规则拒绝者）。
type naiveEval struct {
	entry  Entry
	send   bool
	reason string
}

// attach 线性扫描全部条目，产出与 Kernel.Attach 完全一致的顺序与判定。
func (m *naiveModel) attach(req RequestInput) ([]Entry, []naiveEval, error) {
	if err := validateRequest(req); err != nil {
		return nil, nil, err
	}
	if err := m.checkClock(req.Now); err != nil {
		return nil, nil, err
	}
	m.now = req.Now
	m.clockSet = true

	survivors := make([]Entry, 0, len(m.items))
	evals := []naiveEval{}
	for _, e := range m.items {
		if e.Domain == req.TargetDomain && pathMatch(e.Path, req.TargetPath) &&
			e.Expires != nil && *e.Expires <= req.Now {
			m.expiryEvicted++
			evals = append(evals, naiveEval{e, false, "expired (evicted on observation)"})
			continue
		}
		survivors = append(survivors, e)
	}
	m.items = survivors

	cand := []Entry{}
	for _, e := range m.items {
		if e.Domain != req.TargetDomain {
			continue
		}
		if pathMatch(e.Path, req.TargetPath) {
			cand = append(cand, e)
		}
	}
	sort.SliceStable(cand, func(i, j int) bool {
		if len(cand[i].Path) != len(cand[j].Path) {
			return len(cand[i].Path) > len(cand[j].Path)
		}
		if cand[i].CreatedAt != cand[j].CreatedAt {
			return cand[i].CreatedAt < cand[j].CreatedAt
		}
		return entryKeyString(cand[i]) < entryKeyString(cand[j])
	})

	sent := []Entry{}
	for i := range cand {
		e := cand[i]
		send, reason := m.evaluate(e, req)
		evals = append(evals, naiveEval{e, send, reason})
		if !send {
			continue
		}
		e.LastAccess = req.Now
		sent = append(sent, e)
		idx := m.find(Key{Name: e.Name, Domain: e.Domain, Path: e.Path, Partition: e.Partition})
		if idx >= 0 {
			m.items[idx].LastAccess = req.Now
		}
	}

	// 与内核一致：先输出被惰性删除的过期评估，再输出存活候选的评估。
	return sent, evals, nil
}

func (m *naiveModel) evaluate(e Entry, req RequestInput) (bool, string) {
	if e.Secure && !req.Secure {
		return false, "secure cookie over non-secure request"
	}
	if e.Partition != req.Partition {
		return false, "partition mismatch"
	}
	return sameSiteAllowed(e.SameSite, req, e.CreatedAt, req.Now, m.cfg.LaxGrace)
}

// readScript 与 Kernel.ReadScript 同语义。
func (m *naiveModel) readScript(domain, targetPath, partition string, now int64) ([]Entry, error) {
	if domain == "" {
		return nil, ErrEmptyDomain
	}
	if targetPath == "" || targetPath[0] != '/' {
		return nil, ErrBadPath
	}
	if err := m.checkClock(now); err != nil {
		return nil, err
	}
	m.now = now
	m.clockSet = true

	survivors := make([]Entry, 0, len(m.items))
	for _, e := range m.items {
		if e.Domain == domain && pathMatch(e.Path, targetPath) && e.Expires != nil && *e.Expires <= now {
			m.expiryEvicted++
			continue
		}
		survivors = append(survivors, e)
	}
	m.items = survivors

	out := []Entry{}
	for _, e := range m.items {
		if e.Domain == domain && !e.HTTPOnly && e.Partition == partition &&
			pathMatch(e.Path, targetPath) {
			out = append(out, e)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if len(out[i].Path) != len(out[j].Path) {
			return len(out[i].Path) > len(out[j].Path)
		}
		if out[i].CreatedAt != out[j].CreatedAt {
			return out[i].CreatedAt < out[j].CreatedAt
		}
		return entryKeyString(out[i]) < entryKeyString(out[j])
	})
	return out, nil
}

func entryKeyString(e Entry) string {
	return keyString(Key{Name: e.Name, Domain: e.Domain, Path: e.Path, Partition: e.Partition})
}

// clear 与 Kernel.Clear 同语义。
func (m *naiveModel) clear(in ClearInput) (int, error) {
	if in.Domain == "" {
		return 0, ErrEmptyDomain
	}
	if err := m.checkClock(in.Now); err != nil {
		return 0, err
	}
	m.now = in.Now
	m.clockSet = true

	kept := make([]Entry, 0, len(m.items))
	removed := 0
	for _, e := range m.items {
		if e.Domain != in.Domain {
			kept = append(kept, e)
			continue
		}
		if in.UsePartition && e.Partition != in.Partition {
			kept = append(kept, e)
			continue
		}
		if in.UseCreatedRange && (e.CreatedAt < in.CreatedFrom || e.CreatedAt >= in.CreatedTo) {
			kept = append(kept, e)
			continue
		}
		m.explicitDelete++
		removed++
	}
	m.items = kept
	return removed, nil
}

func (m *naiveModel) advance(now int64) error {
	if err := m.checkClock(now); err != nil {
		return err
	}
	m.now = now
	m.clockSet = true
	return nil
}

func (m *naiveModel) stats() Stats {
	return Stats{
		TotalAdded:      m.totalAdded,
		CurrentCount:    len(m.items),
		ExpiryEvicted:   m.expiryEvicted,
		CapacityEvicted: m.capacityEvicted,
		ExplicitDelete:  m.explicitDelete,
		Now:             m.now,
	}
}

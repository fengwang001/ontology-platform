package dlabel

import "sync"

import "sync/atomic"

// tagRule 是目录中的一条规则（标签名 → 判定表达式）。
type tagRule struct {
	tag  string
	body Expr
}

// grantKey 按 (主体, 标签) 定位授权，授权与实例无关。
type grantKey struct {
	subject string
	tag     string
}

// catalogState 是某一版本下不可变的模式快照。
// 规则表与授权表都是值语义的 map；目录推进时整体替换，旧版本保持原样。
type catalogState struct {
	version int64
	types   map[string]*objectType
	rules   map[string]map[string]Expr // objectType -> tag -> body
	grants  map[grantKey]Grant
}

type objectType struct {
	name  string
	attrs map[string]ValueKind // 属性名 -> 取值类型
}

func newCatalog(v int64) *catalogState {
	return &catalogState{
		version: v,
		types:   map[string]*objectType{},
		rules:   map[string]map[string]Expr{},
		grants:  map[grantKey]Grant{},
	}
}

func (c *catalogState) clone(v int64) *catalogState {
	nc := &catalogState{
		version: v,
		types:   make(map[string]*objectType, len(c.types)),
		rules:   make(map[string]map[string]Expr, len(c.rules)),
		grants:  make(map[grantKey]Grant, len(c.grants)),
	}
	for name, ot := range c.types {
		attrs := make(map[string]ValueKind, len(ot.attrs))
		for a, k := range ot.attrs {
			attrs[a] = k
		}
		nc.types[name] = &objectType{name: name, attrs: attrs}
	}
	for ot, m := range c.rules {
		nm := make(map[string]Expr, len(m))
		for tag, body := range m {
			nm[tag] = body
		}
		nc.rules[ot] = nm
	}
	for k, g := range c.grants {
		nc.grants[k] = g
	}
	return nc
}

// attrVersion 是单个属性的一条不可变版本记录。
type attrVersion struct {
	version int64
	value   Value
}

// attrHistory 是单个属性的版本链（按 version 递增追加）。
type attrHistory struct {
	mu       sync.RWMutex
	versions []attrVersion
}

// instanceData 是一个实例全部属性的版本链集合，每个属性独立成链。
type instanceData struct {
	mu    sync.RWMutex
	attrs map[string]*attrHistory
}

// getAt 返回属性在快照版本 v 下的取值；实例或属性不存在返回 null 与 false。
// 版本链为追加不可变，读路径只取读锁，且不依赖任何全局锁。
func (d *instanceData) getAt(attr string, v int64) (Value, bool) {
	d.mu.RLock()
	h := d.attrs[attr]
	d.mu.RUnlock()
	if h == nil {
		return NullValue(), false
	}
	return h.at(v)
}

func (h *attrHistory) at(v int64) (Value, bool) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	if len(h.versions) == 0 || v < h.versions[0].version {
		return NullValue(), false
	}
	lo, hi := 0, len(h.versions)
	for lo < hi {
		mid := (lo + hi) / 2
		if h.versions[mid].version <= v {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	return h.versions[lo-1].value, true
}

func (d *instanceData) existsAt(v int64) bool {
	d.mu.RLock()
	defer d.mu.RUnlock()
	for _, h := range d.attrs {
		h.mu.RLock()
		ok := len(h.versions) > 0 && h.versions[0].version <= v
		h.mu.RUnlock()
		if ok {
			return true
		}
	}
	return false
}

// mvccStore 保存属性与目录的多版本数据。
//
// commitMu 串行化所有提交（属性写入、规则变更、授权变更），
// 是系统的唯一线性化点；读操作不获取该锁，直接在不可变版本链与
// 不可变目录快照上完成，因而读写互不阻塞。
type mvccStore struct {
	commitMu sync.Mutex

	current atomic.Int64 // 当前已提交版本（单调递增）；提交由 commitMu 串行化，读取用原子操作

	catalogMu       sync.RWMutex
	catalogVersions []*catalogState // append-only，按版本稀疏存放

	instances sync.Map // key: instanceKey -> *instanceData

	// retainVersions 是保证快照可用的版本窗口；低于 current-retain 的
	// 快照可能因属性链裁剪而失效（报 ErrSnapshotUnavailable）。目录历史不裁剪。
	retainVersions int64
}

type instanceKey struct {
	objectType string
	id         string
}

func newMVCCStore(retain int64) *mvccStore {
	if retain <= 0 {
		retain = 1000
	}
	s := &mvccStore{retainVersions: retain}
	s.catalogVersions = []*catalogState{newCatalog(0)}
	return s
}

func (s *mvccStore) catalogAt(v int64) *catalogState {
	s.catalogMu.RLock()
	defer s.catalogMu.RUnlock()
	idx := s.catalogIndex(v)
	return s.catalogVersions[idx]
}

// catalogIndex 返回不晚于 v 的最新目录版本下标；调用方持目录读锁。
func (s *mvccStore) catalogIndex(v int64) int {
	lo, hi := 0, len(s.catalogVersions)
	for lo < hi {
		mid := (lo + hi) / 2
		if s.catalogVersions[mid].version <= v {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	return lo - 1
}

func (s *mvccStore) currentCatalog() *catalogState {
	s.catalogMu.RLock()
	defer s.catalogMu.RUnlock()
	return s.catalogVersions[len(s.catalogVersions)-1]
}

// advanceCatalog 在当前提交版本下追加一份目录快照。
func (s *mvccStore) advanceCatalog() *catalogState {
	v := s.current.Load()
	old := s.catalogAt(v)
	nc := old.clone(v)
	s.catalogMu.Lock()
	// 同一版本可能只有一次推进（提交串行化保证），防御性处理重复版本。
	if last := s.catalogVersions[len(s.catalogVersions)-1]; last.version == s.current.Load() {
		s.catalogVersions[len(s.catalogVersions)-1] = nc
	} else {
		s.catalogVersions = append(s.catalogVersions, nc)
	}
	s.catalogMu.Unlock()
	return nc
}

func (s *mvccStore) getInstance(ot, id string) *instanceData {
	if v, ok := s.instances.Load(instanceKey{ot, id}); ok {
		return v.(*instanceData)
	}
	return nil
}

func (s *mvccStore) loadOrCreateInstance(ot, id string) *instanceData {
	d := &instanceData{attrs: map[string]*attrHistory{}}
	actual, _ := s.instances.LoadOrStore(instanceKey{ot, id}, d)
	return actual.(*instanceData)
}

// attrVersionsForTest 返回某属性版本号序列的副本（供测试核对串行等价性）。
func (s *mvccStore) attrVersionsForTest(ot, id, attr string) []int64 {
	d := s.getInstance(ot, id)
	if d == nil {
		return nil
	}
	d.mu.RLock()
	h := d.attrs[attr]
	d.mu.RUnlock()
	if h == nil {
		return nil
	}
	h.mu.RLock()
	out := make([]int64, len(h.versions))
	for i, av := range h.versions {
		out[i] = av.version
	}
	h.mu.RUnlock()
	return out
}

// appendAttr 在版本 v 下为某属性追加一条新版本（调用方持提交锁）。
func (s *mvccStore) appendAttr(d *instanceData, attr string, v int64, val Value) {
	d.mu.Lock()
	h := d.attrs[attr]
	if h == nil {
		h = &attrHistory{}
		d.attrs[attr] = h
	}
	h.mu.Lock()
	h.versions = append(h.versions, attrVersion{version: v, value: val})
	h.mu.Unlock()
	d.mu.Unlock()
}

// pruneOld 在提交后裁剪超出保留窗口的属性版本（不推进任何时钟、不影响判定缓存）。
func (s *mvccStore) pruneOld() {
	floor := s.current.Load() - s.retainVersions
	if floor <= 0 {
		return
	}
	s.instances.Range(func(_, v any) bool {
		d := v.(*instanceData)
		d.mu.Lock()
		for attr, h := range d.attrs {
			h.mu.Lock()
			cut := 0
			for cut < len(h.versions) && h.versions[cut].version < floor {
				cut++
			}
			if cut > 0 {
				// 保留 floor 之前最近的一条，使恰好在裁剪边界的旧快照仍可用；
				// 更早版本的失效由平台层按保留窗口显式判定。
				if cut > 1 {
					h.versions = append([]attrVersion{h.versions[cut-1]}, h.versions[cut:]...)
				}
			}
			h.mu.Unlock()
			_ = attr
		}
		d.mu.Unlock()
		return true
	})
}

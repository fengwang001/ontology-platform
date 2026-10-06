package increcheck

// cache.go：检查结果缓存。
// 每个签名/实现结果都附带依据 Basis：该结果所读取的每个签名，
// 以及采纳结果瞬间该签名的版本号。沿用时逐一比对登记当前版本，
// 任何一个不一致就拒绝沿用；开销只与直接依赖数量成正比。

// Basis 是一次沿用所依据的签名版本集合。
type Basis map[string]int64

// SigEntry 是缓存的签名检查结果。
type SigEntry struct {
	Result SigResult
	Basis  Basis
}

// ImplEntry 是缓存的实现检查结果。
type ImplEntry struct {
	Result ImplResult
	Basis  Basis
}

// Cache 保存签名与实现两类检查结果。
type Cache struct {
	sig  map[string]SigEntry
	impl map[string]ImplEntry
}

func NewCache() *Cache {
	return &Cache{sig: map[string]SigEntry{}, impl: map[string]ImplEntry{}}
}

// makeBasis 在采纳结果的同一瞬间快照依赖签名的当前版本，
// 杜绝「结果来自旧版本、依据来自新版本」的拼接。
func makeBasis(reg *Registry, refs []string) Basis {
	b := Basis{}
	for _, ref := range refs {
		b[ref] = reg.sigVersion(ref)
	}
	return b
}

func (c *Cache) putSig(id string, e SigEntry) { c.sig[id] = e }
func (c *Cache) getSig(id string) (SigEntry, bool) {
	e, ok := c.sig[id]
	return e, ok
}

func (c *Cache) dropSig(id string) { delete(c.sig, id) }

// getSigResult 返回缓存中的签名结果（不判断依据是否有效）。
func (c *Cache) getSigResult(id string) SigResult {
	if e, ok := c.sig[id]; ok {
		return e.Result
	}
	return SigResult{Present: false}
}

func (c *Cache) putImpl(id string, e ImplEntry) { c.impl[id] = e }
func (c *Cache) getImpl(id string) (ImplEntry, bool) {
	e, ok := c.impl[id]
	return e, ok
}

func (c *Cache) dropImpl(id string) { delete(c.impl, id) }

// validSig 判断 id 的缓存签名结果是否可以沿用，
// 并返回不通过的第一个依赖标识（全部通过时 stale 为空）。
func (c *Cache) validSig(id string, reg *Registry) (bool, string) {
	e, ok := c.sig[id]
	if !ok {
		return false, id
	}
	return basisValid(e.Basis, reg), staleDep(e.Basis, reg)
}

// validImpl 判断 id 的缓存实现结果是否可以沿用。
func (c *Cache) validImpl(id string, reg *Registry) (bool, string) {
	e, ok := c.impl[id]
	if !ok {
		return false, id
	}
	return basisValid(e.Basis, reg), staleDep(e.Basis, reg)
}

// basisValid 只做逐一直等，不遍历程序其余声明。
func basisValid(b Basis, reg *Registry) bool {
	for dep, ver := range b {
		if reg.sigVersion(dep) != ver {
			return false
		}
	}
	return true
}

// staleDep 以确定次序找出第一个失配依据，供日志与测试引用。
func staleDep(b Basis, reg *Registry) string {
	for _, dep := range sortedBasis(b) {
		if reg.sigVersion(dep) != b[dep] {
			return dep
		}
	}
	return ""
}

// sortedBasis 返回依据标识的字典序，表示客观唯一。
func sortedBasis(b Basis) []string {
	out := make([]string, 0, len(b))
	for dep := range b {
		out = append(out, dep)
	}
	return sortedStrings(out)
}

// basisIDs 返回该结果所依据的签名标识（字典序），用于重登记依赖。
func (e SigEntry) basisIDs() []string { return sortedBasis(e.Basis) }

// basisIDs 返回实现结果所依据的签名标识（字典序）。
func (e ImplEntry) basisIDs() []string { return sortedBasis(e.Basis) }

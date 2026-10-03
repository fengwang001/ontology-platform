// Package region 保存单个区域内的版本集合、本地序号与当前版本判定。
package region

import "sync"

// VersionID 唯一标识一个版本：origin 为起源区域，Seq 为该区域本地序号。
type VersionID struct {
	Origin string
	Seq    int64
}

// Version 是一个数据版本或删除标记（Delete 标记 Size==0）。
type Version struct {
	ID      VersionID
	Key     string
	Size    int64
	TS      int64
	Delete  bool
	Replica bool
}

// Region 是单个区域的版本存储。
type Region struct {
	mu          sync.RWMutex
	name        string
	seq         int64
	versions    map[VersionID]Version
	keyIndex    map[string]map[VersionID]struct{}
	applyProbes int64
}

// New 创建一个命名区域的空存储。
func New(name string) *Region {
	return &Region{
		name:     name,
		versions: make(map[VersionID]Version),
		keyIndex: make(map[string]map[VersionID]struct{}),
	}
}

// Name 返回区域名。
func (g *Region) Name() string {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.name
}

// Create 在本地创建新版本，占用下一个本地序号。
func (g *Region) Create(key string, size, ts int64, del bool) Version {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.seq++
	v := Version{
		ID:     VersionID{Origin: g.name, Seq: g.seq},
		Key:    key,
		Size:   size,
		TS:     ts,
		Delete: del,
	}
	g.putLocked(v)
	return v
}

// Apply 把一个版本写入目的区域；已存在时不改状态。
// 重复判定只触碰按标识定位的 1 条记录，与区域内版本总数无关。
func (g *Region) Apply(v Version) (dup bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.applyProbes++
	if _, ok := g.versions[v.ID]; ok {
		return true
	}
	v.Replica = v.ID.Origin != g.name
	g.putLocked(v)
	return false
}

// Current 返回某键的当前版本及其是否存在。
// 决胜序：ts 大者胜，其次 origin 字节序大者，其次本地序号大者；与到达先后无关。
func (g *Region) Current(key string) (Version, bool) {
	g.mu.RLock()
	defer g.mu.RUnlock()
	var cur Version
	found := false
	for id := range g.keyIndex[key] {
		v := g.versions[id]
		if !found || newer(v, cur) {
			cur, found = v, true
		}
	}
	return cur, found
}

// Has 报告本区域是否拥有该键的任何版本。
func (g *Region) Has(key string) bool {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return len(g.keyIndex[key]) > 0
}

// Versions 返回本区域全部版本的副本（快照）。
func (g *Region) Versions() []Version {
	g.mu.RLock()
	defer g.mu.RUnlock()
	out := make([]Version, 0, len(g.versions))
	for _, v := range g.versions {
		out = append(out, v)
	}
	return out
}

// ApplyProbes 返回 Apply 重复判定累计触碰的记录数。
func (g *Region) ApplyProbes() int64 {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.applyProbes
}

// NextSeq 返回下一个将被分配的本地序号（测试快照用）。
func (g *Region) NextSeq() int64 {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.seq + 1
}

func (g *Region) putLocked(v Version) {
	g.versions[v.ID] = v
	set := g.keyIndex[v.Key]
	if set == nil {
		set = make(map[VersionID]struct{})
		g.keyIndex[v.Key] = set
	}
	set[v.ID] = struct{}{}
}

func newer(a, b Version) bool {
	if a.TS != b.TS {
		return a.TS > b.TS
	}
	if a.ID.Origin != b.ID.Origin {
		return a.ID.Origin > b.ID.Origin
	}
	return a.ID.Seq > b.ID.Seq
}

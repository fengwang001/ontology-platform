// Package region 实现单个区域内的版本存储、本地序号与当前版本收敛。
package region

import (
	"errors"
	"sync"
)

var (
	// ErrNotFound 键在本区域无任何版本。
	ErrNotFound = errors.New("不存在")
	// ErrDeleted 当前版本是删除标记。
	ErrDeleted = errors.New("被标记删除")
)

// VersionID 版本全局标识：origin 区域 + 该区域本地序号。
type VersionID struct {
	Origin string
	Seq    int64
}

// Version 区域内保存的一个版本（数据版本或删除标记）。
type Version struct {
	ID      VersionID
	Key     string
	Size    int64
	TS      int64
	Marker  bool
	Replica bool
}

// Region 单个区域。方法均为线程安全的。
type Region struct {
	mu sync.Mutex

	name string
	seq  int64

	// all 按 (origin,seq) 索引区域内全部版本，重复判定只触碰其中一条记录。
	all  map[VersionID]*Version
	keys map[string][]*Version

	duplicates  int64
	applyProbes int64
}

// New 创建一个具名区域。
func New(name string) *Region {
	return &Region{
		name: name,
		all:  make(map[VersionID]*Version),
		keys: make(map[string][]*Version),
	}
}

// Put 在本区域创建一个数据版本并占用一个本地序号。
func (r *Region) Put(key string, size, ts int64) Version {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.seq++
	v := Version{
		ID:  VersionID{Origin: r.name, Seq: r.seq},
		Key: key, Size: size, TS: ts,
	}
	r.store(&v)
	return v
}

// Delete 在本区域创建一个删除标记（size 为 0）。
func (r *Region) Delete(key string, ts int64) Version {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.seq++
	v := Version{
		ID:     VersionID{Origin: r.name, Seq: r.seq},
		Key:    key,
		TS:     ts,
		Marker: true,
	}
	r.store(&v)
	return v
}

// Apply 将一个外部版本幂等地写入本区域；重复（标识已存在）返回 true。
func (r *Region) Apply(v Version) (duplicate bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.applyProbes++ // 一次 map 探测，与区域内版本总数无关。
	if _, ok := r.all[v.ID]; ok {
		r.duplicates++
		return true
	}
	v.Replica = true
	r.store(&v)
	return false
}

// Get 返回当前版本；无版本报 ErrNotFound，当前为标记报 ErrDeleted。
func (r *Region) Get(key string) (Version, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	v, ok := r.currentLocked(key)
	if !ok {
		return Version{}, ErrNotFound
	}
	if v.Marker {
		return *v, ErrDeleted
	}
	return *v, nil
}

// Current 返回当前版本；无任何版本时 ok 为 false。
func (r *Region) Current(key string) (v Version, ok bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	p, have := r.currentLocked(key)
	if !have {
		return Version{}, false
	}
	return *p, true
}

// Has 判断标识是否已存在于本区域。
func (r *Region) Has(id VersionID) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	_, ok := r.all[id]
	return ok
}

// Seq 返回本区域已分配的本地序号最大值。
func (r *Region) Seq() int64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.seq
}

// Duplicates 返回 Apply 命中重复的累计次数。
func (r *Region) Duplicates() int64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.duplicates
}

// ApplyProbes 返回重复判定触碰记录的累计次数（每次 Apply 至多 1）。
func (r *Region) ApplyProbes() int64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.applyProbes
}

// Versions 返回区域内全部版本的快照（顺序不保证）。
func (r *Region) Versions() []Version {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]Version, 0, len(r.all))
	for _, p := range r.all {
		out = append(out, *p)
	}
	return out
}

// store 要求调用方持有 r.mu。
func (r *Region) store(v *Version) {
	r.all[v.ID] = v
	r.keys[v.Key] = append(r.keys[v.Key], v)
}

// currentLocked 选取规则：ts 大者胜；ts 相等 origin 字节序大者胜；
// 再相同则本地序号大者胜。与到达先后无关。要求调用方持有 r.mu。
func (r *Region) currentLocked(key string) (*Version, bool) {
	vs := r.keys[key]
	if len(vs) == 0 {
		return nil, false
	}
	best := vs[0]
	for _, v := range vs[1:] {
		if newer(v, best) {
			best = v
		}
	}
	return best, true
}

func newer(a, b *Version) bool {
	if a.TS != b.TS {
		return a.TS > b.TS
	}
	if a.ID.Origin != b.ID.Origin {
		return a.ID.Origin > b.ID.Origin
	}
	return a.ID.Seq > b.ID.Seq
}

// Package keyindex 提供按字节序排列的有序键索引，以及每个键的版本列表。
package keyindex

import (
	"sort"
	"sync"
	"sync/atomic"
)

// Version 描述键的一个版本。
type Version struct {
	ID       int64
	IsDelete bool
}

// KeyVersions 是一次测试装载的全部内容。
type KeyVersions struct {
	Key      string
	Versions []Version
}

type entry struct {
	key      string
	versions []Version // 版本号降序，最大者即当前版本
}

// Snapshot 是某一时刻的不可变索引视图。
type Snapshot struct {
	entries []entry
	idx     *Index
}

// Index 是并发安全的对象存储键索引。
type Index struct {
	mu    sync.Mutex
	cur   atomic.Pointer[Snapshot]
	seeks int64
}

// New 创建空索引。
func New() *Index {
	idx := &Index{}
	idx.cur.Store(&Snapshot{idx: idx})
	return idx
}

// Load 用测试数据整体替换索引内容。
func (i *Index) Load(data []KeyVersions) {
	built := make([]entry, 0, len(data))
	for _, kv := range data {
		vs := append([]Version(nil), kv.Versions...)
		sort.Slice(vs, func(a, b int) bool { return vs[a].ID > vs[b].ID })
		built = append(built, entry{key: kv.Key, versions: vs})
	}
	sort.Slice(built, func(a, b int) bool { return built[a].key < built[b].key })

	i.mu.Lock()
	defer i.mu.Unlock()
	i.cur.Store(&Snapshot{entries: built, idx: i})
}

// Put 合并写入一个版本；键不存在则新建。
func (i *Index) Put(key string, v Version) {
	i.mu.Lock()
	defer i.mu.Unlock()

	old := i.cur.Load()
	pos := sort.Search(len(old.entries), func(n int) bool {
		return old.entries[n].key >= key
	})

	var next []entry
	if pos < len(old.entries) && old.entries[pos].key == key {
		next = make([]entry, len(old.entries))
		copy(next, old.entries)
		merged := mergeVersion(append([]Version(nil), next[pos].versions...), v)
		next[pos].versions = merged
	} else {
		next = make([]entry, 0, len(old.entries)+1)
		next = append(next, old.entries[:pos]...)
		next = append(next, entry{key: key, versions: []Version{v}})
		next = append(next, old.entries[pos:]...)
	}
	i.cur.Store(&Snapshot{entries: next, idx: i})
}

func mergeVersion(vs []Version, v Version) []Version {
	pos := sort.Search(len(vs), func(n int) bool { return vs[n].ID <= v.ID })
	if pos < len(vs) && vs[pos].ID == v.ID {
		vs[pos] = v
		return vs
	}
	vs = append(vs, Version{})
	copy(vs[pos+1:], vs[pos:])
	vs[pos] = v
	return vs
}

// Snapshot 返回调用时刻的不可变视图。
func (i *Index) Snapshot() *Snapshot { return i.cur.Load() }

// Seeks 返回索引自创建以来的 SeekGE 调用次数。
func (i *Index) Seeks() int64 { return atomic.LoadInt64(&i.seeks) }

// ResetSeeks 将 SeekGE 计数清零，供测试精确复核。
func (i *Index) ResetSeeks() { atomic.StoreInt64(&i.seeks, 0) }

// SeekGE 返回不小于 k 的第一个键；ok 为 false 表示已到键空间末尾。
// 每次成功调用（无论是否命中）都让所属 Index 的 seeks 加 1。
func (s *Snapshot) SeekGE(k string) (key string, versions []Version, pos int, ok bool) {
	atomic.AddInt64(&s.idx.seeks, 1)
	pos = sort.Search(len(s.entries), func(n int) bool { return s.entries[n].key >= k })
	if pos == len(s.entries) {
		return "", nil, pos, false
	}
	return s.entries[pos].key, s.entries[pos].versions, pos, true
}

// At 返回位置 pos 上的键，不产生 seek。
func (s *Snapshot) At(pos int) (key string, versions []Version, ok bool) {
	if pos < 0 || pos >= len(s.entries) {
		return "", nil, false
	}
	return s.entries[pos].key, s.entries[pos].versions, true
}

func (s *Snapshot) Len() int { return len(s.entries) }

// Succ 返回 succ(P)；ok 为 false 表示键空间末尾、无后继。
func Succ(p string) (string, bool) {
	b := []byte(p)
	for len(b) > 0 && b[len(b)-1] == 0xFF {
		b = b[:len(b)-1]
	}
	if len(b) == 0 {
		return "", false
	}
	b[len(b)-1]++
	return string(b), true
}

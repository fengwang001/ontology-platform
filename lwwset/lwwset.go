package lwwset

import (
	"fmt"
	"reflect"
	"sort"
	"sync"
	"unsafe"
)

// Record 保存单个元素最新的添加时间与删除时间。零值表示该事件从未发生。
type Record struct {
	AddTime int64
	DelTime int64
}

// Entry 是变更日志中的一条记录，用于增量合并。
type Entry struct {
	Seq     int64
	Element string
	Record
}

// Replica 是一个 LWW 集合副本，可被多个 goroutine 并发使用。
type Replica struct {
	id       int64
	capacity int
	mu       sync.RWMutex
	elements map[string]Record
	log      []Entry
	pos      map[int64]int64
}

type pendingChange struct {
	element string
	rec     Record
}

func maxInt64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

// lockPair 以副本指针地址的全局稳定顺序同时加写锁，
// 互逆方向的合并并发进行也不会死锁。
func lockPair(a, b *Replica) (unlock func()) {
	pa := reflect.ValueOf(a).Pointer()
	pb := reflect.ValueOf(b).Pointer()
	if pa < pb {
		a.mu.Lock()
		b.mu.Lock()
		return func() { b.mu.Unlock(); a.mu.Unlock() }
	}
	b.mu.Lock()
	a.mu.Lock()
	return func() { a.mu.Unlock(); b.mu.Unlock() }
}

// appendLogLocked 追加一条变更日志，序号从 1 开始单调递增。
func (r *Replica) appendLogLocked(element string, rec Record) {
	var seq int64
	if n := len(r.log); n > 0 {
		seq = r.log[n-1].Seq
	}
	r.log = append(r.log, Entry{Seq: seq + 1, Element: element, Record: rec})
}

var _ = unsafe.Sizeof(0)

// NewReplica 创建副本；id 为副本编号，limit 为记录元素数上限。
func NewReplica(id int64, limit int) (*Replica, error) {
	return nil, nil
}

// ID 返回副本编号。
func (r *Replica) ID() int64 { return 0 }

// Add 在 ts 时刻添加元素。
func (r *Replica) Add(element string, ts int64) error { return nil }

// Remove 在 ts 时刻删除元素。
func (r *Replica) Remove(element string, ts int64) error { return nil }

// Contains 判定元素当前是否在集合中。
func (r *Replica) Contains(element string) bool { return false }

// Lookup 返回元素的添加/删除时间记录。
func (r *Replica) Lookup(element string) Record { return Record{} }

// Elements 返回集合中的全部元素（排序后）。
func (r *Replica) Elements() []string { return nil }

// Snapshot 返回全部元素记录的副本。
func (r *Replica) Snapshot() map[string]Record { return nil }

// Merge 将 other 整份合并进 r（只修改 r），返回新变更条数。
func (r *Replica) Merge(other *Replica) (int, error) { return 0, nil }

// MergeSince 返回 other 序号大于 since 的变更。
func MergeSince(other *Replica, since int64) ([]Entry, int64, error) { return nil, 0, nil }

// MergeIncremental 按保存的对端位置做增量合并，返回新变更条数。
func (r *Replica) MergeIncremental(other *Replica) (int, error) { return 0, nil }

// MergePosition 返回 r 记录的与对端 otherID 的合并位置。
func (r *Replica) MergePosition(otherID int64) (int64, error) { return 0, nil }

// SelfCheck 校验内部不变量。
func (r *Replica) SelfCheck() error { return nil }

// present 实现“并列偏删除”：有添加且删除严格早于添加时存在。
func present(rec Record) bool {
	return rec.AddTime > 0 && rec.AddTime > rec.DelTime
}

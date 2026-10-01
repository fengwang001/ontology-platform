// Package fdb 实现一个带注入时钟的以太网学习交换机转发表（FDB）。
//
// 处理模型与可复现性见同包 README.md。
package fdb

import (
	"errors"
	"sync"
)

// MAC 为 6 字节以太网地址。
type MAC = [6]byte

// Stats 为全部可观测计数的快照。
type Stats struct {
	Learned       int64
	Moves         int64
	Evictions     int64
	Expired       int64
	Flushed       int64
	Overridden    int64
	SecurityDrops int64
	Floods        int64
	Filtered      int64
}

// 可通过 errors.Is 区分的拒绝原因。
var (
	ErrPortRange       = errors.New("fdb: port out of range")
	ErrVLANRange       = errors.New("fdb: vlan out of range")
	ErrMulticastStatic = errors.New("fdb: static entry with multicast mac")
	ErrClockBackward   = errors.New("fdb: timestamp earlier than previous successful call")
	ErrInvalidConfig   = errors.New("fdb: invalid constructor parameters")
)

// entry 是一条转发表表项。
type entry struct {
	port   int
	seen   int64
	static bool
}

// FDB 是学习交换机转发表。零值不可用，须用 New 构造。
type FDB struct {
	n int
	a int64
	c int

	mu         sync.Mutex
	table      map[[8]byte]entry
	dynCount   int
	lastT      int64
	learned    int64
	moves      int64
	evictions  int64
	expired    int64
	flushed    int64
	overridden int64
	secDrops   int64
	floods     int64
	filtered   int64
}

// New 构造一个 N 端口（端口 0..N-1）、老化时长 A 纳秒、动态容量 C 的转发表。
// 静态表项不占动态容量。N∈[1,64]、A>0、C>0，否则返回 ErrInvalidConfig。
func New(n int, a int64, c int) (*FDB, error) {
	if n < 1 || n > 64 || a <= 0 || c <= 0 {
		return nil, ErrInvalidConfig
	}
	return &FDB{
		n:     n,
		a:     a,
		c:     c,
		table: make(map[[8]byte]entry),
	}, nil
}

func key(v int, mac MAC) [8]byte {
	return [8]byte{byte(v >> 8), byte(v), mac[0], mac[1], mac[2], mac[3], mac[4], mac[5]}
}

// IsMulticast 报告 mac 是否为组播（全 FF 广播亦属组播）：首字节最低位为 1。
func IsMulticast(mac MAC) bool { return mac[0]&1 == 1 }

func validVLAN(v int) bool { return v >= 1 && v <= 4094 }

func (f *FDB) validPort(p int) bool { return p >= 0 && p < f.n }

func keyLess(a, b [8]byte) bool {
	for i := 0; i < 8; i++ {
		if a[i] != b[i] {
			return a[i] < b[i]
		}
	}
	return false
}

// beginChange 校验变更类调用的通用前置条件，清除全部已过期动态表项，
// 并把注入时钟推进到 t。返回 nil 时调用方已持有 f.mu。
// 拒绝路径在任何状态变更之前返回，不改变表项、时钟与计数。
func (f *FDB) beginChange(p, v int, t int64) error {
	f.mu.Lock()
	if !f.validPort(p) {
		f.mu.Unlock()
		return ErrPortRange
	}
	if !validVLAN(v) {
		f.mu.Unlock()
		return ErrVLANRange
	}
	if t < f.lastT {
		f.mu.Unlock()
		return ErrClockBackward
	}
	f.sweep(t)
	f.lastT = t
	return nil
}

// sweep 删除所有满足 t-seen >= A 的动态表项（左闭）。调用时持锁。
func (f *FDB) sweep(t int64) {
	for k, e := range f.table {
		if !e.static && t-e.seen >= f.a {
			delete(f.table, k)
			f.dynCount--
			f.expired++
		}
	}
}

// evict 淘汰 seen 最早的动态表项；并列取键 (v,mac) 字节序最小者。调用时持锁。
func (f *FDB) evict() {
	var victim [8]byte
	bestSeen := int64(1<<63 - 1)
	found := false
	for k, e := range f.table {
		if e.static {
			continue
		}
		if !found || e.seen < bestSeen || (e.seen == bestSeen && keyLess(k, victim)) {
			victim, bestSeen, found = k, e.seen, true
		}
	}
	delete(f.table, victim)
	f.dynCount--
	f.evictions++
}

// flood 返回除入端口 p 外全部端口的升序列表（全新切片，不共享内部存储）。
func (f *FDB) flood(p int) []int {
	out := make([]int, 0, f.n-1)
	for q := 0; q < f.n; q++ {
		if q != p {
			out = append(out, q)
		}
	}
	return out
}

// Frame 按“校验 -> 过期清除 -> 学习 -> 转发”的固定顺序处理一帧，
// 返回升序出端口列表；返回切片每次新建，不与内部状态共享存储。
func (f *FDB) Frame(p int, s, d MAC, v int, t int64) ([]int, error) {
	if err := f.beginChange(p, v, t); err != nil {
		return nil, err
	}
	defer f.mu.Unlock()

	// 学习：组播源不学习，但帧仍继续转发。
	if !IsMulticast(s) {
		sk := key(v, s)
		if e, ok := f.table[sk]; ok {
			if e.static {
				// 静态表项受保护、不改动；从其他端口来帧直接丢弃。
				if e.port != p {
					f.secDrops++
					return []int{}, nil
				}
			} else {
				if e.port != p {
					f.moves++
				}
				e.port = p
				e.seen = t
				f.table[sk] = e
			}
		} else {
			if f.dynCount >= f.c {
				f.evict()
			}
			f.table[sk] = entry{port: p, seen: t}
			f.dynCount++
			f.learned++
		}
	}

	// 转发：组播目的或未知单播泛洪；命中同端口过滤；否则单播到该端口。
	if IsMulticast(d) {
		f.floods++
		return f.flood(p), nil
	}
	if e, ok := f.table[key(v, d)]; ok {
		if e.port == p {
			f.filtered++
			return []int{}, nil
		}
		return []int{e.port}, nil
	}
	f.floods++
	return f.flood(p), nil
}

// AddStatic 安装静态表项：同键动态表项被覆盖（Overridden），
// 同键静态表项则改端口。mac 为组播时返回 ErrMulticastStatic。
func (f *FDB) AddStatic(v int, mac MAC, p int, t int64) error {
	if IsMulticast(mac) {
		return ErrMulticastStatic
	}
	if err := f.beginChange(p, v, t); err != nil {
		return err
	}
	defer f.mu.Unlock()

	k := key(v, mac)
	if e, ok := f.table[k]; ok && !e.static {
		f.dynCount--
		f.overridden++
	}
	f.table[k] = entry{port: p, seen: t, static: true}
	return nil
}

// FlushPort 删除端口 p 上的全部动态表项，静态表项不受影响，返回删除条数。
func (f *FDB) FlushPort(p int, t int64) (int, error) {
	if err := f.beginChange(p, 1, t); err != nil {
		return 0, err
	}
	defer f.mu.Unlock()

	removed := 0
	for k, e := range f.table {
		if !e.static && e.port == p {
			delete(f.table, k)
			f.dynCount--
			removed++
		}
	}
	f.flushed += int64(removed)
	return removed, nil
}

// Lookup 只读查询：已过期的动态表项视为未命中，不改变任何表项、时钟与计数。
func (f *FDB) Lookup(v int, mac MAC, t int64) (port int, found bool, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !validVLAN(v) {
		return 0, false, ErrVLANRange
	}
	e, ok := f.table[key(v, mac)]
	if !ok {
		return 0, false, nil
	}
	if !e.static && t-e.seen >= f.a {
		return 0, false, nil
	}
	return e.port, true, nil
}

// Len 返回当前存放的动态表项数（含已过期但尚未被清除者）。
func (f *FDB) Len() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.dynCount
}

// Stats 返回全部计数的快照。
func (f *FDB) Stats() Stats {
	f.mu.Lock()
	defer f.mu.Unlock()
	return Stats{
		Learned:       f.learned,
		Moves:         f.moves,
		Evictions:     f.evictions,
		Expired:       f.expired,
		Flushed:       f.flushed,
		Overridden:    f.overridden,
		SecurityDrops: f.secDrops,
		Floods:        f.floods,
		Filtered:      f.filtered,
	}
}

// inspect 返回某表项的 seen 与是否静态，仅供包内测试观察使用。
func (f *FDB) inspect(v int, mac MAC) (seen int64, static bool, ok bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	e, exists := f.table[key(v, mac)]
	return e.seen, e.static, exists
}

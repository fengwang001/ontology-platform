package fat12

import (
	"errors"
	"sync"
)

// 拒绝原因，可用 errors.Is 区分。
var (
	ErrInvalid    = errors.New("fat12: invalid argument")
	ErrNotFound   = errors.New("fat12: file not found")
	ErrNoSpace    = errors.New("fat12: not enough free clusters")
	ErrOutOfRange = errors.New("fat12: truncate length beyond chain")
	ErrNotFree    = errors.New("fat12: cluster is not free")
)

// FAT12 是带连续优先分配与碎片整理的 FAT12 簇链表管理器。
//
// 12 位项字节布局
//
// 数据簇数 C 取值 1..4078，数据簇编号 2..C+1，FAT 共 C+2 项
// （项 0、项 1 保留），字节映像长度为 ⌈(C+2)×3/2⌉。项 n 的偏移为
// o = n + ⌊n/2⌋：
//
//	n 为偶数：值的低 8 位在字节 o，高 4 位在字节 o+1 的低半字节；
//	n 为奇数：值的低 4 位在字节 o 的高半字节，高 8 位在字节 o+1。
//
// 写任一项只改动该项所属的 12 位，不触碰相邻项的 4 位。
//
// 项值含义：0 空闲；0xFF7 坏簇；0xFF8 及以上为链尾（本实现统一写
// 0xFFF）；其余值为下一簇号。构造后项 0=0xFF8、项 1=0xFFF。
//
// 分配（Create/Extend 规则相同）
//
// 先在簇号不小于 rover 且不超过 C+1 的范围内（不环绕）找起点最小的、
// 由 n 个连续空闲簇组成的段；找不到时退回循环下一适应：从 rover 起按
// 簇号升序循环扫描（越过 C+1 回到 2，每簇至多扫一次）依次取空闲簇。
// 坏簇与已占用簇都不算空闲。成功后 rover = 最后取得簇的下一个簇号，
// 超过 C+1 则回到 2。
//
// 释放（Truncate/Delete）与碎片整理（Defrag）
//
// Truncate/Delete 释放簇后，rover 取 min(rover, 被释放簇中的最小簇号)，
// 无簇释放时不变。Defrag 取「全体空闲簇 ∪ 本文件链簇」中最小的 L 个
// （L 为链长，坏簇不在集合内）升序串成新链；新旧序列相同则不改，
// 否则释放旧链中不再使用的簇、写入新链，句柄改为新链首簇（旧句柄失效），
// 有簇释放时 rover 按释放规则回退，仅顺序变化时 rover 不变。
//
// 拒绝顺序（仅报第一个，被拒绝操作不改变任何状态）：参数非法 →
// 文件不存在 → 操作自有原因（空间不足 / Truncate 越界 / 簇非空闲）。
type FAT12 struct {
	mu    sync.Mutex
	c     int    // 数据簇数 C
	max   int    // 最大数据簇号 C+1
	fat   []byte // 12 位打包的 FAT 字节映像
	files map[int]struct{}
	rover int
}

// New 创建管理 C 个数据簇（簇号 2..C+1）的管理器。
func New(c int) (*FAT12, error) {
	if c < 1 || c > 4078 {
		return nil, ErrInvalid
	}
	n := c + 2
	f := &FAT12{
		c:     c,
		max:   c + 1,
		fat:   make([]byte, (n*3+1)/2),
		files: make(map[int]struct{}),
		rover: 2,
	}
	f.setEntry(0, 0xFF8)
	f.setEntry(1, 0xFFF)
	return f, nil
}

// getEntry 读取 12 位项 n。
// n 为偶数：低 8 位在字节 o，高 4 位在字节 o+1 的低半字节；
// n 为奇数：低 4 位在字节 o 的高半字节，高 8 位在字节 o+1。
func (f *FAT12) getEntry(n int) uint16 {
	o := n + n/2
	if n&1 == 0 {
		return uint16(f.fat[o]) | uint16(f.fat[o+1]&0x0F)<<8
	}
	return uint16(f.fat[o]>>4) | uint16(f.fat[o+1])<<4
}

// setEntry 写入 12 位项 n，不改动相邻项的位。
func (f *FAT12) setEntry(n int, v uint16) {
	v &= 0x0FFF
	o := n + n/2
	if n&1 == 0 {
		f.fat[o] = byte(v)
		f.fat[o+1] = f.fat[o+1]&0xF0 | byte(v>>8)&0x0F
		return
	}
	f.fat[o] = f.fat[o]&0x0F | byte(v<<4)&0xF0
	f.fat[o+1] = byte(v >> 4)
}

// Create 新建含 n 个簇的文件，返回句柄（首簇号）。
func (f *FAT12) Create(n int) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if n < 1 {
		return 0, ErrInvalid
	}
	if f.freeCount() < n {
		return 0, ErrNoSpace
	}
	clusters := f.allocate(n)
	f.linkChain(clusters, true)
	h := clusters[0]
	f.files[h] = struct{}{}
	f.advanceRover(clusters[len(clusters)-1])
	return h, nil
}

// Extend 在文件 h 的链尾之后追加 n 个簇。
func (f *FAT12) Extend(h, n int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if n < 1 {
		return ErrInvalid
	}
	if !f.fileExists(h) {
		return ErrNotFound
	}
	if f.freeCount() < n {
		return ErrNoSpace
	}
	clusters := f.allocate(n)
	f.linkChain(clusters, true)
	oldChain := f.chainOf(h)
	oldTail := oldChain[len(oldChain)-1]
	f.setEntry(oldTail, uint16(clusters[0]))
	f.advanceRover(clusters[len(clusters)-1])
	return nil
}

// allocate 返回 n 个空闲簇，取簇顺序：先找 rover 起不环绕的最小连续
// 空闲段；找不到时退回从 rover 起的循环下一适应扫描。
func (f *FAT12) allocate(n int) []int {
	if run := f.findContiguous(n); run != nil {
		return run
	}
	return f.scanCyclic(n)
}

// findContiguous 在簇号不小于 rover 且不超过 C+1 的范围内（不环绕）
// 找起点最小的、由 n 个连续空闲簇组成的段。
func (f *FAT12) findContiguous(n int) []int {
	runStart := -1
	runLen := 0
	for c := f.rover; c <= f.max; c++ {
		if f.getEntry(c) == 0 {
			if runLen == 0 {
				runStart = c
			}
			runLen++
			if runLen == n {
				out := make([]int, n)
				for i := range out {
					out[i] = runStart + i
				}
				return out
			}
			continue
		}
		runStart = -1
		runLen = 0
	}
	return nil
}

// scanCyclic 从 rover 起按簇号升序循环扫描（每个簇至多扫一次），
// 依次收集空闲簇。
func (f *FAT12) scanCyclic(n int) []int {
	out := make([]int, 0, n)
	for c, seen := f.rover, 0; seen < f.c; seen++ {
		if f.getEntry(c) == 0 {
			out = append(out, c)
			if len(out) == n {
				return out
			}
		}
		c++
		if c > f.max {
			c = 2
		}
	}
	return out
}

// linkChain 按给定次序把簇串成链并以 0xFFF 收尾。
func (f *FAT12) linkChain(clusters []int, eoc bool) {
	for i := 0; i+1 < len(clusters); i++ {
		f.setEntry(clusters[i], uint16(clusters[i+1]))
	}
	if eoc {
		f.setEntry(clusters[len(clusters)-1], 0xFFF)
	}
}

// advanceRover 成功分配后把 rover 置为最后取得簇的下一个簇号。
func (f *FAT12) advanceRover(last int) {
	next := last + 1
	if next > f.max {
		next = 2
	}
	f.rover = next
}

func (f *FAT12) fileExists(h int) bool {
	_, ok := f.files[h]
	return ok
}

func (f *FAT12) freeCount() int {
	n := 0
	for c := 2; c <= f.max; c++ {
		if f.getEntry(c) == 0 {
			n++
		}
	}
	return n
}

// chainOf 返回文件首簇为 h 的链簇号序列。调用方须保证 h 是现存句柄。
func (f *FAT12) chainOf(h int) []int {
	out := []int{h}
	for {
		v := f.getEntry(h)
		if v >= 0xFF8 {
			return out
		}
		h = int(v)
		out = append(out, h)
	}
}

// Truncate 保留文件 h 链的前 k 个簇。
func (f *FAT12) Truncate(h, k int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if k < 1 {
		return ErrInvalid
	}
	if !f.fileExists(h) {
		return ErrNotFound
	}
	chain := f.chainOf(h)
	if k > len(chain) {
		return ErrOutOfRange
	}
	released := chain[k:]
	f.setEntry(chain[k-1], 0xFFF)
	for _, c := range released {
		f.setEntry(c, 0)
	}
	f.retreatRover(released)
	return nil
}

// Delete 删除文件 h 并释放整条链。
func (f *FAT12) Delete(h int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.fileExists(h) {
		return ErrNotFound
	}
	chain := f.chainOf(h)
	for _, c := range chain {
		f.setEntry(c, 0)
	}
	delete(f.files, h)
	f.retreatRover(chain)
	return nil
}

// MarkBad 把一个空闲簇标记为坏簇（0xFF7）。
func (f *FAT12) MarkBad(c int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if c < 2 || c > f.max {
		return ErrInvalid
	}
	if f.getEntry(c) != 0 {
		return ErrNotFree
	}
	f.setEntry(c, 0xFF7)
	return nil
}

// Defrag 把文件 h 的链整理到最小簇号，返回（可能变化的）句柄。
func (f *FAT12) Defrag(h int) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.fileExists(h) {
		return 0, ErrNotFound
	}
	old := f.chainOf(h)
	l := len(old)

	// S = 全体空闲簇 ∪ 旧链簇（坏簇不在其中），取最小的 L 个。
	newChain := make([]int, 0, l)
	for c := 2; c <= f.max && len(newChain) < l; c++ {
		v := f.getEntry(c)
		if v == 0 || belongs(c, old) {
			newChain = append(newChain, c)
		}
	}

	if equalInts(old, newChain) {
		return h, nil
	}

	oldSet := make(map[int]struct{}, l)
	for _, c := range old {
		oldSet[c] = struct{}{}
	}
	newSet := make(map[int]struct{}, l)
	for _, c := range newChain {
		newSet[c] = struct{}{}
	}

	released := make([]int, 0)
	for _, c := range old {
		if _, ok := newSet[c]; !ok {
			released = append(released, c)
		}
	}

	// 先释放旧链中不再使用的簇，再写入新链。
	for _, c := range released {
		f.setEntry(c, 0)
	}
	f.linkChain(newChain, true)

	delete(f.files, h)
	f.files[newChain[0]] = struct{}{}
	f.retreatRover(released)
	return newChain[0], nil
}

// retreatRover 在释放了簇时把 rover 回退到被释放的最小簇号。
func (f *FAT12) retreatRover(released []int) {
	if len(released) == 0 {
		return
	}
	min := released[0]
	for _, c := range released[1:] {
		if c < min {
			min = c
		}
	}
	if min < f.rover {
		f.rover = min
	}
}

func belongs(x int, list []int) bool {
	for _, v := range list {
		if v == x {
			return true
		}
	}
	return false
}

func equalInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// Image 返回 FAT 字节映像副本。
func (f *FAT12) Image() []byte {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]byte, len(f.fat))
	copy(out, f.fat)
	return out
}

// Chain 返回文件 h 的链簇号序列副本。
func (f *FAT12) Chain(h int) ([]int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.fileExists(h) {
		return nil, ErrNotFound
	}
	return f.chainOf(h), nil
}

// Free 返回空闲簇数（不含坏簇）。
func (f *FAT12) Free() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.freeCount()
}

// Rover 返回分配游标。
func (f *FAT12) Rover() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.rover
}

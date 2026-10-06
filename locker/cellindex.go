package locker

import "math/bits"

// CellID 格口编号（正整数）。
type CellID int

// Cell 描述一个格口。
type Cell struct {
	ID   CellID
	Size Size
}

// 单档格口数上限 = 64^3 = 262144（叶 64 位 × L1 64 位 × L2 64 位）。
const maxCellsPerTier = 64 * 64 * 64

// freeBitset 是单档格口的三级层次位图：
//
//	leaf[b] 的第 k 位   = 格口 64b+k 是否空闲
//	l1[a]   的第 b 位   = leaf[a*64+b] 是否非零
//	l2      的第 a 位   = l1[a] 是否非零
//
// 找到最小空闲位固定只访问 1 个 l2 字、1 个 l1 字、1 个 leaf 字，
// 共 3 次字探测，与该档格口总数无关。
type freeBitset struct {
	n      int
	leaf   []uint64
	l1     []uint64
	l2     uint64
	ids    []CellID // 位序号 -> 格口编号（ids 已按编号升序）
	probes int
}

func newFreeBitset(ids []CellID) *freeBitset {
	n := len(ids)
	nl := (n + 63) / 64
	fs := &freeBitset{
		n:    n,
		leaf: make([]uint64, nl),
		l1:   make([]uint64, (nl+63)/64),
		ids:  ids,
	}
	// 初始全部空闲。
	for i := 0; i < n; i++ {
		fs.leaf[i>>6] |= 1 << uint(i&63)
	}
	for a, w := range fs.leaf {
		if w != 0 {
			fs.l1[a>>6] |= 1 << uint(a&63)
		}
	}
	for a, w := range fs.l1 {
		if w != 0 {
			fs.l2 |= 1 << uint(a)
		}
	}
	return fs
}

// findMin 返回最小的空闲位序号；每次访问一个机器字记一次探测。
func (fs *freeBitset) findMin() (int, bool) {
	fs.probes++ // l2
	if fs.l2 == 0 {
		return 0, false
	}
	a := bits.TrailingZeros64(fs.l2)
	fs.probes++ // l1[a]
	b := bits.TrailingZeros64(fs.l1[a])
	leafIdx := a*64 + b
	fs.probes++ // leaf[leafIdx]
	k := bits.TrailingZeros64(fs.leaf[leafIdx])
	return leafIdx*64 + k, true
}

func (fs *freeBitset) markBusy(bit int) {
	fs.leaf[bit>>6] &^= 1 << uint(bit&63)
	if fs.leaf[bit>>6] == 0 {
		a := bit >> 12
		b := (bit >> 6) & 63
		fs.l1[a] &^= 1 << uint(b)
		if fs.l1[a] == 0 {
			fs.l2 &^= 1 << uint(a)
		}
	}
}

func (fs *freeBitset) markFree(bit int) {
	wasZero := fs.leaf[bit>>6] == 0
	fs.leaf[bit>>6] |= 1 << uint(bit&63)
	if wasZero {
		a := bit >> 12
		b := (bit >> 6) & 63
		l1Zero := fs.l1[a] == 0
		fs.l1[a] |= 1 << uint(b)
		if l1Zero {
			fs.l2 |= 1 << uint(a)
		}
	}
}

// CellIndex 是三档格口的分层位图索引。
//
// 存件时从「不小于快件规格的最低档」起逐档探测，每档最多 3 次字探测
// （空档仅 1 次），总探测次数上限为 3*3=9，不随格口总数与历史快件数增长。
// ProbeStats 以可观测方式暴露探测计数，供开销证明使用（见测试与设计说明）。
type CellIndex struct {
	tiers [numSizes]*freeBitset
	pos   map[CellID]pos // 格口编号 -> (档, 位序号)

	allocations int
	releases    int
	wordProbes  int
}

type pos struct {
	tier Size
	bit  int
}

// ProbeStats 汇总自最近一次 ResetStats 以来的索引活动计数。
type ProbeStats struct {
	Allocations int // 分配调用次数
	Releases    int // 释放调用次数
	WordProbes  int // 访问的机器字总数
}

// NewCellIndex 构建索引；格口编号必须为正且互不相同，单档数量有上限。
func NewCellIndex(cells []Cell) (*CellIndex, bool) {
	var perTier [numSizes][]CellID
	seen := make(map[CellID]bool, len(cells))
	for _, cell := range cells {
		if cell.ID <= 0 || !cell.Size.valid() || seen[cell.ID] {
			return nil, false
		}
		seen[cell.ID] = true
		perTier[cell.Size] = append(perTier[cell.Size], cell.ID)
	}
	ci := &CellIndex{pos: make(map[CellID]pos, len(cells))}
	for tier := Size(0); tier < numSizes; tier++ {
		ids := perTier[tier]
		if len(ids) > maxCellsPerTier {
			return nil, false
		}
		// 调用方（NewCabinet）已按编号排序；防御性地就地排序保证不变量。
		sortCellIDs(ids)
		fs := newFreeBitset(ids)
		ci.tiers[tier] = fs
		for bit, id := range ids {
			ci.pos[id] = pos{tier: tier, bit: bit}
		}
	}
	return ci, true
}

// Allocate 选择规格不小于 minSize 的、规格最小且编号最小的空闲格口。
func (ci *CellIndex) Allocate(minSize Size) (CellID, bool) {
	if !minSize.valid() {
		return 0, false
	}
	ci.allocations++
	before := ci.probeSnapshot()
	for tier := minSize; tier < numSizes; tier++ {
		fs := ci.tiers[tier]
		if bit, ok := fs.findMin(); ok {
			fs.markBusy(bit)
			ci.wordProbes += ci.probeSnapshot() - before
			return fs.ids[bit], true
		}
	}
	ci.wordProbes += ci.probeSnapshot() - before
	return 0, false
}

// Release 令格口重新空闲，立即可被分配。
func (ci *CellIndex) Release(id CellID) bool {
	p, ok := ci.pos[id]
	if !ok {
		return false
	}
	ci.releases++
	ci.tiers[p.tier].markFree(p.bit)
	return true
}

// HasFitting 返回柜内是否存在规格不小于 minSize 的格口（不论忙闲）。
func (ci *CellIndex) HasFitting(minSize Size) bool {
	for tier := minSize; tier < numSizes; tier++ {
		if ci.tiers[tier].n > 0 {
			return true
		}
	}
	return false
}

func (ci *CellIndex) probeSnapshot() int {
	sum := 0
	for tier := Size(0); tier < numSizes; tier++ {
		sum += ci.tiers[tier].probes
	}
	return sum
}

func (ci *CellIndex) Stats() ProbeStats {
	return ProbeStats{
		Allocations: ci.allocations,
		Releases:    ci.releases,
		WordProbes:  ci.wordProbes,
	}
}

func (ci *CellIndex) ResetStats() {
	ci.allocations, ci.releases, ci.wordProbes = 0, 0, 0
	for tier := Size(0); tier < numSizes; tier++ {
		ci.tiers[tier].probes = 0
	}
}

// sortCellIDs 为避免新增依赖而手写的插入排序；格口清单通常一次性导入。
func sortCellIDs(ids []CellID) {
	for i := 1; i < len(ids); i++ {
		for j := i; j > 0 && ids[j-1] > ids[j]; j-- {
			ids[j-1], ids[j] = ids[j], ids[j-1]
		}
	}
}

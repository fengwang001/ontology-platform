package ftl

import (
	"fmt"
	"math/rand"
	"reflect"
	"testing"
)

// naive 是独立编写的朴素逐页模型：用线性扫描实现同一套语义，
// 用于与基于堆的 FTL 实现逐操作对照。它的可信来自简单直观，
// 而不是性能。
type naive struct {
	cfg     Config
	pages   [][]naivePage
	next    []int
	valid   []int
	erases  []int
	retired []bool
	active  int
	l2p     map[uint64]pa
	mapped  int
	free    int
	nret    int
	logical uint64
	phys    uint64
}

type naivePage struct {
	st   pageState
	lpn  uint64
	data []byte
}

func newNaive(cfg Config) *naive {
	n := &naive{
		cfg:     cfg,
		pages:   make([][]naivePage, cfg.NumBlocks),
		next:    make([]int, cfg.NumBlocks),
		valid:   make([]int, cfg.NumBlocks),
		erases:  make([]int, cfg.NumBlocks),
		retired: make([]bool, cfg.NumBlocks),
		active:  -1,
		l2p:     make(map[uint64]pa),
		free:    cfg.NumBlocks,
	}
	for i := range n.pages {
		n.pages[i] = make([]naivePage, cfg.PagesPerBlock)
	}
	return n
}

func (n *naive) full(b int) bool { return n.next[b] == n.cfg.PagesPerBlock }

// ensureActive 线性扫描空闲块：擦除次数最少，并列取块号较小者。
func (n *naive) ensureActive() bool {
	if n.active >= 0 {
		return true
	}
	best := -1
	for i := 0; i < n.cfg.NumBlocks; i++ {
		if n.retired[i] || n.next[i] != 0 || i == n.active {
			continue
		}
		if best < 0 || n.erases[i] < n.erases[best] ||
			(n.erases[i] == n.erases[best] && i < best) {
			best = i
		}
	}
	if best < 0 {
		return false
	}
	n.free--
	n.active = best
	return true
}

// program 在块 b 的下一个空闲页编程，返回物理地址。
func (n *naive) program(b int, lpn uint64, data []byte) pa {
	p := n.next[b]
	n.pages[b][p] = naivePage{st: pageValid, lpn: lpn, data: append([]byte(nil), data...)}
	n.next[b]++
	n.valid[b]++
	n.phys++
	if n.full(b) && n.active == b {
		n.active = -1
	}
	return pa{block: b, page: p}
}

func (n *naive) invalidate(b, p int) {
	n.pages[b][p].st = pageInvalid
	n.valid[b]--
}

func (n *naive) write(lpn uint64, data []byte) error {
	if lpn >= uint64(n.cfg.LogicalPages) {
		return ErrInvalidArgument
	}
	old, mapped := n.l2p[lpn]
	if !mapped {
		if limit := (n.cfg.NumBlocks - n.nret - n.cfg.LowWatermark) * n.cfg.PagesPerBlock; n.mapped+1 > limit {
			return ErrNoSpace
		}
	}
	if !n.ensureActive() {
		n.collect()
		if !n.ensureActive() {
			return ErrNoSpace
		}
	}
	if mapped {
		n.invalidate(old.block, old.page)
	}
	n.l2p[lpn] = n.program(n.active, lpn, data)
	if !mapped {
		n.mapped++
	}
	n.logical++
	if n.free < n.cfg.LowWatermark {
		n.collect()
	}
	return nil
}

func (n *naive) read(lpn uint64) ([]byte, error) {
	if lpn >= uint64(n.cfg.LogicalPages) {
		return nil, ErrInvalidArgument
	}
	addr, ok := n.l2p[lpn]
	if !ok {
		return nil, ErrNotWritten
	}
	return append([]byte(nil), n.pages[addr.block][addr.page].data...), nil
}

func (n *naive) discard(lpn uint64) error {
	if lpn >= uint64(n.cfg.LogicalPages) {
		return ErrInvalidArgument
	}
	addr, ok := n.l2p[lpn]
	if !ok {
		return nil
	}
	n.invalidate(addr.block, addr.page)
	delete(n.l2p, lpn)
	n.mapped--
	return nil
}

// pickVictim 线性扫描：写满、非活动、未退役且有回收收益（有效页 <
// 每块页数）的块中，有效页最少、擦除次数较少、块号较小者。
func (n *naive) pickVictim() (int, bool) {
	best := -1
	for i := 0; i < n.cfg.NumBlocks; i++ {
		if n.retired[i] || i == n.active || !n.full(i) || n.valid[i] == n.cfg.PagesPerBlock {
			continue
		}
		if best < 0 ||
			n.valid[i] < n.valid[best] ||
			(n.valid[i] == n.valid[best] && n.erases[i] < n.erases[best]) ||
			(n.valid[i] == n.valid[best] && n.erases[i] == n.erases[best] && i < best) {
			best = i
		}
	}
	if best < 0 {
		return -1, false
	}
	return best, true
}

func (n *naive) reclaim(v int) bool {
	for p := 0; p < n.cfg.PagesPerBlock; p++ {
		if n.pages[v][p].st != pageValid {
			continue
		}
		if !n.ensureActive() {
			return false
		}
		lpn := n.pages[v][p].lpn
		data := n.pages[v][p].data
		n.invalidate(v, p)
		n.l2p[lpn] = n.program(n.active, lpn, data)
	}
	n.erase(v)
	return true
}

func (n *naive) erase(b int) {
	for i := range n.pages[b] {
		n.pages[b][i] = naivePage{}
	}
	n.next[b] = 0
	n.valid[b] = 0
	n.erases[b]++
	if n.erases[b] >= n.cfg.EraseLimit {
		n.retired[b] = true
		n.nret++
		return
	}
	n.free++
}

func (n *naive) collect() {
	ran := false
	for n.free < n.cfg.HighWatermark {
		v, ok := n.pickVictim()
		if !ok {
			break
		}
		if !n.reclaim(v) {
			break
		}
		ran = true
	}
	if ran {
		n.wearLevel()
	}
}

func (n *naive) wearLevel() {
	lo, hi := -1, -1
	for i := 0; i < n.cfg.NumBlocks; i++ {
		if n.retired[i] {
			continue
		}
		if lo < 0 || n.erases[i] < n.erases[lo] {
			lo = i
		}
		if hi < 0 || n.erases[i] > n.erases[hi] {
			hi = i
		}
	}
	if lo < 0 || n.erases[hi]-n.erases[lo] <= n.cfg.WearThreshold {
		return
	}
	best := -1
	for i := 0; i < n.cfg.NumBlocks; i++ {
		if n.retired[i] || i == n.active || !n.full(i) {
			continue
		}
		if best < 0 || n.erases[i] < n.erases[best] ||
			(n.erases[i] == n.erases[best] && i < best) {
			best = i
		}
	}
	if best >= 0 {
		n.reclaim(best)
	}
}

// ---- 布局快照：FTL 与朴素模型逐页对比 ----

type dumpPage struct {
	st  pageState
	lpn int64 // 仅有效页有意义，其余为 -1
}

type dumpBlock struct {
	next    int
	valid   int
	erases  int
	retired bool
	active  bool
	pages   []dumpPage
}

type dump struct {
	blocks  []dumpBlock
	l2p     map[uint64]pa
	mapped  int
	free    int
	nret    int
	logical uint64
	phys    uint64
}

func dumpFTL(f *FTL) dump {
	d := dump{
		l2p:     make(map[uint64]pa, len(f.l2p)),
		mapped:  f.mapped,
		free:    f.freeCount,
		nret:    f.retired,
		logical: f.logicalWrites,
		phys:    f.physicalProgrammed,
	}
	for lpn, addr := range f.l2p {
		d.l2p[lpn] = addr
	}
	for i := range f.blocks {
		blk := &f.blocks[i]
		db := dumpBlock{
			next:    blk.nextFree,
			valid:   blk.valid,
			erases:  blk.erases,
			retired: blk.retired,
			active:  f.active == i,
			pages:   make([]dumpPage, len(blk.pages)),
		}
		for p := range blk.pages {
			db.pages[p] = dumpPage{st: blk.pages[p], lpn: -1}
			if blk.pages[p] == pageValid {
				db.pages[p].lpn = int64(blk.lpn[p])
			}
		}
		d.blocks = append(d.blocks, db)
	}
	return d
}

func dumpNaive(n *naive) dump {
	d := dump{
		l2p:     make(map[uint64]pa, len(n.l2p)),
		mapped:  n.mapped,
		free:    n.free,
		nret:    n.nret,
		logical: n.logical,
		phys:    n.phys,
	}
	for lpn, addr := range n.l2p {
		d.l2p[lpn] = addr
	}
	for i := 0; i < n.cfg.NumBlocks; i++ {
		db := dumpBlock{
			next:    n.next[i],
			valid:   n.valid[i],
			erases:  n.erases[i],
			retired: n.retired[i],
			active:  n.active == i,
			pages:   make([]dumpPage, n.cfg.PagesPerBlock),
		}
		for p := range n.pages[i] {
			db.pages[p] = dumpPage{st: n.pages[i][p].st, lpn: -1}
			if n.pages[i][p].st == pageValid {
				db.pages[p].lpn = int64(n.pages[i][p].lpn)
			}
		}
		d.blocks = append(d.blocks, db)
	}
	return d
}

// checkInvariants 统计量守恒不变式（对 FTL 与模型都检查）。
func checkInvariants(t *testing.T, d dump, cfg Config, who string) {
	t.Helper()
	validSum, freeBlocks, retiredBlocks, activeBlocks := 0, 0, 0, 0
	for _, b := range d.blocks {
		validSum += b.valid
		switch {
		case b.retired:
			retiredBlocks++
		case b.active:
			activeBlocks++
		case b.next == 0:
			freeBlocks++
		}
		cnt := 0
		for _, p := range b.pages {
			if p.st == pageValid {
				cnt++
			}
		}
		if cnt != b.valid {
			t.Fatalf("%s: block valid count %d != actual valid pages %d", who, b.valid, cnt)
		}
	}
	if validSum != d.mapped || len(d.l2p) != d.mapped {
		t.Fatalf("%s: valid=%d mapped=%d l2p=%d must be equal", who, validSum, d.mapped, len(d.l2p))
	}
	if freeBlocks != d.free {
		t.Fatalf("%s: free blocks = %d, stats say %d", who, freeBlocks, d.free)
	}
	if retiredBlocks != d.nret {
		t.Fatalf("%s: retired blocks = %d, stats say %d", who, retiredBlocks, d.nret)
	}
	if activeBlocks > 1 {
		t.Fatalf("%s: at most one active block, got %d", who, activeBlocks)
	}
	if freeBlocks+retiredBlocks+activeBlocks > cfg.NumBlocks {
		t.Fatalf("%s: block classification overflows total", who)
	}
	if d.phys < d.logical {
		t.Fatalf("%s: physical programmed %d < logical writes %d", who, d.phys, d.logical)
	}
}

// TestModelRandomOps 随机写入/丢弃/读取序列下，FTL 与朴素逐页模型
// 的每一次操作结果、物理布局与统计量都必须完全一致；
// 日志打印每条操作的输入、输出与判定依据。
func TestModelRandomOps(t *testing.T) {
	configs := []Config{
		{NumBlocks: 8, PagesPerBlock: 4, LogicalPages: 24, LowWatermark: 2, HighWatermark: 3, EraseLimit: 6, WearThreshold: 1},
		{NumBlocks: 12, PagesPerBlock: 3, LogicalPages: 40, LowWatermark: 2, HighWatermark: 4, EraseLimit: 1000, WearThreshold: 2},
		{NumBlocks: 6, PagesPerBlock: 2, LogicalPages: 10, LowWatermark: 2, HighWatermark: 3, EraseLimit: 3, WearThreshold: 0},
	}
	for ci, cfg := range configs {
		for seed := int64(1); seed <= 3; seed++ {
			t.Run(fmt.Sprintf("cfg%d/seed%d", ci, seed), func(t *testing.T) {
				runModelComparison(t, cfg, seed, 2000)
			})
		}
	}
}

func runModelComparison(t *testing.T, cfg Config, seed int64, ops int) {
	t.Helper()
	f := mustNew(t, cfg)
	n := newNaive(cfg)
	rng := rand.New(rand.NewSource(seed))

	for i := 0; i < ops; i++ {
		kind := rng.Intn(100)
		lpn := uint64(rng.Intn(cfg.LogicalPages + 2)) // 含越界参数
		data := []byte{byte(rng.Intn(256)), byte(i)}

		var fErr, nErr error
		var fGot, nGot []byte
		var op, reason string
		switch {
		case kind < 55:
			op = "WRITE"
			_, mapped := n.l2p[lpn]
			limit := (cfg.NumBlocks - n.nret - cfg.LowWatermark) * cfg.PagesPerBlock
			reason = fmt.Sprintf("mapped=%v mapped+1=%d limit=%d free=%d low=%d",
				mapped, n.mapped+1, limit, n.free, cfg.LowWatermark)
			fErr = f.Write(lpn, data)
			nErr = n.write(lpn, data)
		case kind < 80:
			op = "DISCARD"
			_, mapped := n.l2p[lpn]
			reason = fmt.Sprintf("mapped=%v (未映射则无操作)", mapped)
			fErr = f.Discard(lpn)
			nErr = n.discard(lpn)
		default:
			op = "READ"
			_, mapped := n.l2p[lpn]
			reason = fmt.Sprintf("mapped=%v", mapped)
			fGot, fErr = f.Read(lpn)
			nGot, nErr = n.read(lpn)
		}

		match := (fErr == nil && nErr == nil) ||
			(fErr != nil && nErr != nil && fErr.Error() == nErr.Error())
		if !match || !reflect.DeepEqual(fGot, nGot) {
			t.Fatalf("op=%d %s lpn=%d: FTL=(%v,%v) model=(%v,%v) | %s",
				i, op, lpn, fGot, fErr, nGot, nErr, reason)
		}
		t.Logf("op=%d %s lpn=%d data=%v -> err=%v | 判定依据: %s", i, op, lpn, data, fErr, reason)

		fd, nd := dumpFTL(f), dumpNaive(n)
		if !reflect.DeepEqual(fd, nd) {
			t.Fatalf("op=%d %s lpn=%d: layout diverged\nFTL:   %+v\nmodel: %+v", i, op, lpn, fd, nd)
		}
		checkInvariants(t, fd, cfg, "FTL")
		checkInvariants(t, nd, cfg, "model")
	}
}

// TestDeterministicReplay 相同操作序列重放得到完全相同的物理布局与统计量。
func TestDeterministicReplay(t *testing.T) {
	cfg := Config{NumBlocks: 8, PagesPerBlock: 4, LogicalPages: 24,
		LowWatermark: 2, HighWatermark: 3, EraseLimit: 5, WearThreshold: 1}

	type op struct {
		kind int
		lpn  uint64
		data []byte
	}
	rng := rand.New(rand.NewSource(42))
	ops := make([]op, 1500)
	for i := range ops {
		ops[i] = op{kind: rng.Intn(3), lpn: uint64(rng.Intn(cfg.LogicalPages)), data: []byte{byte(i)}}
	}

	run := func() dump {
		f := mustNew(t, cfg)
		for _, o := range ops {
			switch o.kind {
			case 0:
				f.Write(o.lpn, o.data)
			case 1:
				f.Discard(o.lpn)
			default:
				f.Read(o.lpn)
			}
		}
		return dumpFTL(f)
	}
	first, second := run(), run()
	if !reflect.DeepEqual(first, second) {
		t.Fatal("replay of identical op sequence produced different layout/stats")
	}
}

package ontology

import (
	"errors"
	"fmt"
	"math/rand"
	"reflect"
	"strings"
	"sync"
	"testing"
)

// ---------------- 朴素参考模型 ----------------

// naiveRA 是严格按题目规则逐步写就的独立参考实现，用于与 ReadAhead 对照。
// 它不复用生产代码中的任何结构与辅助函数。
type naiveRA struct {
	n, i, m, cp int
	cache       []int        // LRU 次序：最旧在前，最新在后
	pf          map[int]bool // 预读未读标记
	prev        int
	ws, wsz, mk int
}

type naiveResult struct {
	demand    []int
	readahead []int
	evicted   []int
	branch    string // 判定依据
}

func newNaive(n, i, m, cp int) *naiveRA {
	return &naiveRA{n: n, i: i, m: m, cp: cp, pf: map[int]bool{}, prev: -1}
}

func (s *naiveRA) has(p int) bool { _, ok := s.pf[p]; return ok }

func (s *naiveRA) touch(p int) {
	for idx, q := range s.cache {
		if q == p {
			s.cache = append(append(append([]int{}, s.cache[:idx]...), s.cache[idx+1:]...), p)
			return
		}
	}
}

func imin(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func imax(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func (s *naiveRA) read(p, n int) *naiveResult {
	res := &naiveResult{demand: []int{}, readahead: []int{}, evicted: []int{}}
	end := p + n
	seq := p == s.prev+1

	missingSet := map[int]bool{}
	for page := p; page < end; page++ {
		if !s.has(page) {
			missingSet[page] = true
		}
	}

	// (1)(2) 窗口与预读计算。
	makeWindow, newWS, newSize := false, 0, 0
	if len(missingSet) > 0 {
		for page := p; page < end; page++ {
			if missingSet[page] {
				res.demand = append(res.demand, page)
			}
		}
		if seq {
			size := imin(s.m, imax(s.i, imax(2*s.wsz, n)))
			rs := end
			if rs < s.n {
				makeWindow, newWS, newSize = true, rs, size
				res.branch = fmt.Sprintf("miss+seq: s=min(%d,max(%d,2*%d,%d))=%d rs=%d", s.m, s.i, s.wsz, n, size, rs)
			} else {
				res.branch = "miss+seq: rs>=N, no readahead, window cleared"
			}
		} else {
			res.branch = "miss+non-sequential: no readahead, window cleared"
		}
	} else {
		if s.wsz > 0 && p <= s.mk && s.mk < end {
			size := imin(s.m, imax(s.i, 2*s.wsz))
			rs := s.ws + s.wsz
			if rs < s.n {
				makeWindow, newWS, newSize = true, rs, size
				res.branch = fmt.Sprintf("all-hit: mk=%d in [%d,%d), async s=min(%d,max(%d,2*%d))=%d rs=%d", s.mk, p, end, s.m, s.i, s.wsz, size, rs)
			} else {
				s.wsz = 0
				res.branch = "all-hit: async rs>=N, window cleared"
			}
		} else {
			res.branch = fmt.Sprintf("all-hit: no action (wsz=%d mk=%d range=[%d,%d))", s.wsz, s.mk, p, end)
		}
	}

	if makeWindow {
		limit := imin(newWS+newSize, s.n)
		for page := newWS; page < limit; page++ {
			if !s.has(page) {
				res.readahead = append(res.readahead, page)
			}
		}
		s.ws, s.wsz, s.mk = newWS, newSize, newWS+newSize/2
	} else if len(missingSet) > 0 {
		s.wsz = 0
	}

	// (3) 缓存更新。
	for page := p; page < end; page++ {
		if missingSet[page] {
			s.pf[page] = false
			s.cache = append(s.cache, page)
		} else {
			s.touch(page)
			s.pf[page] = false
		}
	}
	for _, page := range res.readahead {
		s.pf[page] = true
		s.cache = append(s.cache, page)
	}

	evictedPF := false
	for len(s.cache) > s.cp {
		page := s.cache[0]
		s.cache = s.cache[1:]
		if s.pf[page] {
			evictedPF = true
		}
		delete(s.pf, page)
		res.evicted = append(res.evicted, page)
	}
	if evictedPF && s.wsz > 0 {
		before := s.wsz
		s.wsz = imax(1, s.wsz/2)
		res.branch += fmt.Sprintf("; thrash: evicted pf page, wsz %d->%d", before, s.wsz)
	}

	s.prev = end - 1
	return res
}

func (s *naiveRA) dropCache() {
	s.cache = nil
	s.pf = map[int]bool{}
}

func naiveState(s *naiveRA) StateSnapshot {
	snap := StateSnapshot{Prev: s.prev, Window: Window{WS: s.ws, WSz: s.wsz}, HasWindow: s.wsz > 0}
	if s.wsz > 0 {
		snap.Mk = s.mk
	}
	return snap
}

func naiveCacheOrder(s *naiveRA) []int {
	out := make([]int, len(s.cache))
	copy(out, s.cache)
	return out
}

// ---------------- 辅助 ----------------

func eqInts(a, b []int) bool {
	if len(a) == 0 && len(b) == 0 {
		return true
	}
	return reflect.DeepEqual(a, b)
}

func intsStr(a []int) string {
	parts := make([]string, len(a))
	for idx, v := range a {
		parts[idx] = fmt.Sprintf("%d", v)
	}
	return "[" + strings.Join(parts, ",") + "]"
}

func cacheOrder(r *ReadAhead) []int {
	out := make([]int, len(r.cache))
	copy(out, r.cache)
	return out
}

// ---------------- 确定性用例 ----------------

// 题目给出的完整示例。
func TestSpecExample(t *testing.T) {
	r, err := NewReadAhead(100, 2, 8, 4)
	if err != nil {
		t.Fatal(err)
	}

	d, a, e, err := r.Read(0, 1)
	if err != nil || !eqInts(d, []int{0}) || !eqInts(a, []int{1, 2}) || len(e) != 0 {
		t.Fatalf("Read(0,1) = %v %v %v %v", d, a, e, err)
	}
	st := r.State()
	if st.Prev != 0 || !st.HasWindow || st.Window != (Window{1, 2}) || st.Mk != 2 {
		t.Fatalf("state after Read(0,1): %+v", st)
	}

	d, a, e, _ = r.Read(1, 1)
	if !(len(d) == 0 && len(a) == 0 && len(e) == 0) {
		t.Fatalf("Read(1,1) expected all hit no action, got %v %v %v", d, a, e)
	}

	d, a, e, _ = r.Read(2, 1)
	if !(len(d) == 0) || !eqInts(a, []int{3, 4, 5, 6}) || !eqInts(e, []int{0, 1, 2}) {
		t.Fatalf("Read(2,1) = %v %v %v", d, a, e)
	}
	st = r.State()
	if st.Window != (Window{3, 4}) || st.Mk != 5 || st.Prev != 2 {
		t.Fatalf("state after Read(2,1): %+v", st)
	}

	if d, a, e, _ = r.Read(3, 1); !(len(d) == 0 && len(a) == 0 && len(e) == 0) {
		t.Fatalf("Read(3,1) = %v %v %v", d, a, e)
	}
	if d, a, e, _ = r.Read(4, 1); !(len(d) == 0 && len(a) == 0 && len(e) == 0) {
		t.Fatalf("Read(4,1) = %v %v %v", d, a, e)
	}

	d, a, e, _ = r.Read(5, 1)
	if len(d) != 0 || !eqInts(a, []int{7, 8, 9, 10, 11, 12, 13, 14}) ||
		!eqInts(e, []int{6, 3, 4, 5, 7, 8, 9, 10}) {
		t.Fatalf("Read(5,1) = %v %v %v", d, a, e)
	}
	st = r.State()
	if st.Prev != 5 || st.Window != (Window{7, 4}) || st.Mk != 11 {
		t.Fatalf("final state: %+v", st)
	}
}

// 首次 p=0 视为顺序访问（prev 初始 -1，0 == -1+1）。
func TestFirstReadAtZeroIsSequential(t *testing.T) {
	r, _ := NewReadAhead(50, 2, 8, 10)
	d, a, e, err := r.Read(0, 1)
	if err != nil || !eqInts(d, []int{0}) || !eqInts(a, []int{1, 2}) || len(e) != 0 {
		t.Fatalf("sequential first read got %v %v %v %v", d, a, e, err)
	}

	// 首次读非 0：p != prev+1，属随机访问，不预读且窗口清空。
	r2, _ := NewReadAhead(50, 2, 8, 10)
	d, a, e, _ = r2.Read(3, 1)
	if !eqInts(d, []int{3}) || len(a) != 0 || len(e) != 0 {
		t.Fatalf("non-sequential first read: %v %v %v", d, a, e)
	}
	if st := r2.State(); st.HasWindow {
		t.Fatalf("random first read must leave no window, got %+v", st)
	}
}

// I=4、M=16 时逐页顺序读下窗口按 4、8、16 倍增并封顶（Cp 足够大）。
func TestWindowDoublingAndCap(t *testing.T) {
	r, _ := NewReadAhead(400, 4, 16, 400)
	read := func(p int) {
		t.Helper()
		if _, _, _, err := r.Read(p, 1); err != nil {
			t.Fatal(err)
		}
	}

	read(0)
	if st := r.State(); st.Window != (Window{1, 4}) || st.Mk != 3 {
		t.Fatalf("window 4: %+v", st)
	}
	read(1)
	read(2)
	if st := r.State(); st.Window != (Window{1, 4}) {
		t.Fatalf("window must not move before mark page: %+v", st)
	}
	read(3)
	if st := r.State(); st.Window != (Window{5, 8}) || st.Mk != 9 {
		t.Fatalf("window 8: %+v", st)
	}
	for p := 5; p <= 8; p++ {
		read(p)
		if st := r.State(); st.Window != (Window{5, 8}) {
			t.Fatalf("page %d must not trigger async readahead, state %+v", p, st)
		}
	}
	read(9)
	if st := r.State(); st.Window != (Window{13, 16}) || st.Mk != 21 {
		t.Fatalf("window 16: %+v", st)
	}
	for p := 13; p <= 20; p++ {
		read(p)
		if st := r.State(); st.Window != (Window{13, 16}) {
			t.Fatalf("page %d inside new window must not trigger, state %+v", p, st)
		}
	}
	read(21)
	if st := r.State(); st.Window != (Window{29, 16}) || st.Mk != 37 {
		t.Fatalf("window must be capped at 16, state %+v", st)
	}
}

// 标记页恰为 ws+floor(s/2)：读它触发异步预读，读它前一页不触发。
func TestMarkPageBoundary(t *testing.T) {
	r, _ := NewReadAhead(100, 4, 16, 100)
	if _, _, _, _ = r.Read(0, 1); true {
	}
	// 窗口 (1,4)，mk=3。页 1、2 命中不触发。
	for _, p := range []int{1, 2} {
		d, a, e, _ := r.Read(p, 1)
		if len(d) != 0 || len(a) != 0 || len(e) != 0 {
			t.Fatalf("page %d (before mark) must not trigger: %v %v %v", p, d, a, e)
		}
	}
	if st := r.State(); st.Window != (Window{1, 4}) || st.Mk != 3 {
		t.Fatalf("state before mark: %+v", st)
	}
	d, a, e, _ := r.Read(3, 1)
	if len(d) != 0 || !eqInts(a, []int{5, 6, 7, 8, 9, 10, 11, 12}) || len(e) != 0 {
		t.Fatalf("reading mark page 3 must trigger async readahead: %v %v %v", d, a, e)
	}
	if st := r.State(); st.Window != (Window{5, 8}) || st.Mk != 9 {
		t.Fatalf("state after mark: %+v", st)
	}
}

// 一次请求跨过标记页（n > 1）同样触发异步预读。
func TestRequestSpansMarkPage(t *testing.T) {
	r, _ := NewReadAhead(100, 4, 16, 100)
	// Read(0,1)：窗口 (1,4)，mk=3，缓存 {0,1,2,3,4}。
	if _, _, _, _ = r.Read(0, 1); true {
	}
	// Read(1,3) 全命中 [1,4)，mk=3 落在其中，触发异步预读。
	d, a, e, _ := r.Read(1, 3)
	if len(d) != 0 || !eqInts(a, []int{5, 6, 7, 8, 9, 10, 11, 12}) || len(e) != 0 {
		t.Fatalf("spanning request: %v %v %v", d, a, e)
	}
	if st := r.State(); st.Window != (Window{5, 8}) || st.Mk != 9 {
		t.Fatalf("state: %+v", st)
	}
	// 区间右端不含：读 [mk, mk) 风格，例如窗口 (5,8) mk=9，读 [5,9) 不触发。
	d, a, e, _ = r.Read(5, 4)
	if len(d) != 0 || len(a) != 0 || len(e) != 0 {
		t.Fatalf("request ending exactly at mark must not trigger: %v %v %v", d, a, e)
	}
	if st := r.State(); st.Window != (Window{5, 8}) {
		t.Fatalf("state: %+v", st)
	}
}

// n 大于 I 使同步窗口 s 取 n。
func TestNGreaterThanI(t *testing.T) {
	r, _ := NewReadAhead(100, 2, 32, 100)
	d, a, e, _ := r.Read(0, 5)
	if !eqInts(d, []int{0, 1, 2, 3, 4}) || !eqInts(a, []int{5, 6, 7, 8, 9}) || len(e) != 0 {
		t.Fatalf("Read(0,5): %v %v %v", d, a, e)
	}
	if st := r.State(); st.Window != (Window{5, 5}) || st.Mk != 7 {
		t.Fatalf("s must be n=5: %+v", st)
	}

	// 已有 wsz=5 时，更大的 n 仍可抬高 s：读 [5,20)，s=max(I,2*5,20)=20。
	// 页 5..9 已缓存，10..24 尚未缓存 => 走同步未命中分支：需求 10..24，rs=25。
	d, a, e, _ = r.Read(5, 20)
	wantDemand := []int{10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20, 21, 22, 23, 24}
	if !eqInts(d, wantDemand) || !eqInts(a, []int{25, 26, 27, 28, 29, 30, 31, 32, 33, 34, 35, 36, 37, 38, 39, 40, 41, 42, 43, 44}) || len(e) != 0 {
		t.Fatalf("Read(5,20): %v %v %v", d, a, e)
	}
	if st := r.State(); st.Window != (Window{25, 20}) || st.Mk != 35 {
		t.Fatalf("s must be n=20: %+v", st)
	}
}

// 预读起点不小于 N 时窗口清空且不预读（同步分支）。
func TestSyncRSAtNClearsWindow(t *testing.T) {
	r, _ := NewReadAhead(10, 2, 8, 10)
	// DropCache + 顺序读到文件尾：Read(8,2) 时 rs=10 >= N。
	if _, _, _, _ = r.Read(0, 2); true {
	}
	r.DropCache()
	// prev=1；Read(2,6) 顺序，s=min(8,max(2,0,6))=6，需求 {2..7}，rs=8 窗口 (8,6) mk=11。
	d, a, e, _ := r.Read(2, 6)
	if !eqInts(d, []int{2, 3, 4, 5, 6, 7}) || !eqInts(a, []int{8, 9}) || len(e) != 0 {
		t.Fatalf("Read(2,6): %v %v %v", d, a, e)
	}
	if st := r.State(); st.Window != (Window{8, 6}) || st.Mk != 11 {
		t.Fatalf("truncated window keeps untruncated s: %+v", st)
	}
	// 读最后两页：全命中、mk=12 不在内，无动作。
	d, a, e, _ = r.Read(8, 2)
	if len(d) != 0 || len(a) != 0 || len(e) != 0 {
		t.Fatalf("Read(8,2): %v %v %v", d, a, e)
	}
	r.DropCache()
	// 现在 [8,10) 未缓存，顺序未命中 Read(8,2)：需求 {8,9}，rs=10>=N，窗口清空。
	d, a, e, _ = r.Read(8, 2)
	if !eqInts(d, []int{8, 9}) || len(a) != 0 || len(e) != 0 {
		t.Fatalf("Read(8,2) miss at end: %v %v %v", d, a, e)
	}
	if st := r.State(); st.HasWindow {
		t.Fatalf("window must be cleared when rs >= N, got %+v", st)
	}
}

// 窗口被文件尾截断时 s 仍为未截断值（mk 也可能 >= N）。
func TestTruncatedWindowKeepsSize(t *testing.T) {
	r, _ := NewReadAhead(11, 4, 16, 11)
	// Read(0,1)：窗口 (1,4)，预读 1..4。
	if _, _, _, _ = r.Read(0, 1); true {
	}
	// 读到 mk=3 触发异步：s=8, rs=5，预读区间 [5,13) 被 N=11 截断为 5..10，
	// 但窗口仍记 (5,8)，mk=9。
	for _, p := range []int{1, 2} {
		d, a, e, _ := r.Read(p, 1)
		if len(d) != 0 || len(e) != 0 {
			t.Fatalf("Read(%d,1): %v %v %v", p, d, a, e)
		}
	}
	d, a, e, _ := r.Read(3, 1)
	if !eqInts(d, []int{}) || !eqInts(a, []int{5, 6, 7, 8, 9, 10}) || len(e) != 0 {
		t.Fatalf("async readahead at truncation: %v %v %v", d, a, e)
	}
	st := r.State()
	if st.Window != (Window{5, 8}) || st.Mk != 9 {
		t.Fatalf("truncated window must keep s=8: %+v", st)
	}
	// 全量缓存下 mk=9 落在 [5,11) 内：异步 rs=13>=N，窗口清空、不预读。
	d, a, e, _ = r.Read(5, 6)
	if len(d) != 0 || len(a) != 0 || len(e) != 0 {
		t.Fatalf("async rs>=N must not issue pages: %v %v %v", d, a, e)
	}
	if st := r.State(); st.HasWindow {
		t.Fatalf("async rs>=N must clear window: %+v", st)
	}
	// DropCache 保留 prev=10 与（已清空的）窗口；顺序未命中 Read 起点必须紧跟 prev。
	r.DropCache()
	// 另用新实例验证同步窗口的未截断 s=16：I=4，先养大到 wsz=8，
	// DropCache 后从 mk 页顺序读，新 s=16 被 N 截断但 wsz 保持 16。
	r2, _ := NewReadAhead(11, 4, 16, 11)
	if _, _, _, _ = r2.Read(0, 1); true { // (1,4) mk=3
	}
	for _, p := range []int{1, 2} {
		if _, _, _, _ = r2.Read(p, 1); true {
		}
	}
	if _, _, _, _ = r2.Read(3, 1); true { // (5,8) mk=9
	}
	r2.DropCache()
	// prev=3；Read(4,1) 顺序未命中，s=16，rs=5，预读 5..10（截断），wsz 仍为 16。
	d2, a2, e2, _ := r2.Read(4, 1)
	if !eqInts(d2, []int{4}) || !eqInts(a2, []int{5, 6, 7, 8, 9, 10}) || len(e2) != 0 {
		t.Fatalf("sync window truncated but s=16: %v %v %v", d2, a2, e2)
	}
	if st := r2.State(); st.Window != (Window{5, 16}) || st.Mk != 13 {
		t.Fatalf("window keeps untruncated s=16: %+v", st)
	}
	if st := r.State(); st.Prev != 10 {
		t.Fatalf("prev: %+v", st)
	}
}

// 随机访问清空窗口，其后的顺序未命中重新从 max(I, n) 起算。
func TestRandomAccessResetsWindow(t *testing.T) {
	r, _ := NewReadAhead(100, 4, 16, 100)
	// 先把窗口养大：Read(0,1) -> (1,4) mk=3，读 3 -> (5,8)。
	if _, _, _, _ = r.Read(0, 1); true {
	}
	if _, _, _, _ = r.Read(1, 1); true {
	}
	if _, _, _, _ = r.Read(2, 1); true {
	}
	if _, _, _, _ = r.Read(3, 1); true {
	}
	if st := r.State(); st.Window != (Window{5, 8}) {
		t.Fatalf("setup state: %+v", st)
	}
	// 跳到 50：非顺序未命中，窗口清空。
	d, a, e, _ := r.Read(50, 1)
	if !eqInts(d, []int{50}) || len(a) != 0 || len(e) != 0 {
		t.Fatalf("random read: %v %v %v", d, a, e)
	}
	if st := r.State(); st.HasWindow {
		t.Fatalf("random read must clear window: %+v", st)
	}
	// 紧随其后的顺序未命中从 I 重新起算（而不是沿用 2*wsz=16）。
	d, a, e, _ = r.Read(51, 1)
	if !eqInts(d, []int{51}) || !eqInts(a, []int{52, 53, 54, 55}) || len(e) != 0 {
		t.Fatalf("sequential miss after random: %v %v %v", d, a, e)
	}
	if st := r.State(); st.Window != (Window{52, 4}) || st.Mk != 54 {
		t.Fatalf("window must restart from I: %+v", st)
	}
}

// DropCache 后标记页未缓存导致走未命中分支。
func TestDropCacheMarkMiss(t *testing.T) {
	r, _ := NewReadAhead(100, 4, 16, 100)
	if _, _, _, _ = r.Read(0, 1); true {
	}
	// 顺序命中 1、2，使 prev=2（同时不触发异步：mk=3 尚未读到）。
	if _, _, _, _ = r.Read(1, 1); true {
	}
	if _, _, _, _ = r.Read(2, 1); true {
	}
	st := r.State()
	if st.Window != (Window{1, 4}) || st.Mk != 3 {
		t.Fatalf("setup: %+v", st)
	}
	r.DropCache()
	if len(cacheOrder(r)) != 0 {
		t.Fatalf("DropCache must empty cache")
	}
	// prev 与窗口保留：State 不变。
	if st2 := r.State(); st2 != st {
		t.Fatalf("DropCache must preserve prev/window/mk: before %+v after %+v", st, st2)
	}
	// 读标记页 3（p=3 == prev+1=3，顺序），但它已不在缓存：走未命中分支，同步预读。
	d, a, e, _ := r.Read(3, 1)
	// DropCache 保留窗口 wsz=4，故同步 s=min(16,max(4,8,1))=8。
	if !eqInts(d, []int{3}) || !eqInts(a, []int{4, 5, 6, 7, 8, 9, 10, 11}) || len(e) != 0 {
		t.Fatalf("dropped mark page must take miss branch: %v %v %v", d, a, e)
	}
	if st := r.State(); st.Window != (Window{4, 8}) || st.Mk != 8 {
		t.Fatalf("new sync window: %+v", st)
	}
}

// 命中更新最近使用次序，从而影响淘汰对象。
func TestLRUTouchChangesEviction(t *testing.T) {
	r, _ := NewReadAhead(100, 2, 8, 2)
	// 缓存 LRU->MRU：0(f),1(t),2(t)（Cp=2 但此刻为初始读后的 3 页？—— Cp=2
	// 时 Read(0,1) 已淘汰 0；改用 Cp=3 建立后再压入随机读）。
	r, _ = NewReadAhead(100, 2, 8, 3)
	if _, _, _, _ = r.Read(0, 1); true {
	}
	if !reflect.DeepEqual(cacheOrder(r), []int{0, 1, 2}) {
		t.Fatalf("setup order: %v", cacheOrder(r))
	}
	// 命中页 1：pf 清除并移到 MRU，次序变为 [0,2,1]。
	d, a, e, _ := r.Read(1, 1)
	if len(d) != 0 || len(a) != 0 || len(e) != 0 {
		t.Fatalf("hit page1: %v %v %v", d, a, e)
	}
	if !reflect.DeepEqual(cacheOrder(r), []int{0, 2, 1}) {
		t.Fatalf("order after hit: %v", cacheOrder(r))
	}
	if r.pf[1] {
		t.Fatalf("hit must clear pf")
	}
	// 一次随机未命中插入页 50：淘汰最旧的 0；再来随机未命中 60：淘汰次旧的 2。
	// 页 1 因命中被更新为最近使用而存活。
	if d, a, e, _ = r.Read(50, 1); !eqInts(d, []int{50}) || len(a) != 0 || !eqInts(e, []int{0}) {
		t.Fatalf("first random read: %v %v %v", d, a, e)
	}
	if d, a, e, _ = r.Read(60, 1); !eqInts(d, []int{60}) || len(a) != 0 || !eqInts(e, []int{2}) {
		t.Fatalf("second random read must evict page 2 (not 1): %v %v %v", d, a, e)
	}
	if !reflect.DeepEqual(cacheOrder(r), []int{1, 50, 60}) {
		t.Fatalf("survivors (1 must survive due to LRU touch): %v", cacheOrder(r))
	}

	// 对照：不命中页 1 时，页 1 会先于页 2 被淘汰。
	r2, _ := NewReadAhead(100, 2, 8, 3)
	if _, _, _, _ = r2.Read(0, 1); true { // [0,1,2]
	}
	if _, _, _, _ = r2.Read(50, 1); true { // 淘汰 0 -> [1,2,50]
	}
	d, a, e, _ = r2.Read(60, 1)
	if !eqInts(e, []int{1}) {
		t.Fatalf("without touch, page 1 must be oldest and evicted: %v", e)
	}
	if !reflect.DeepEqual(cacheOrder(r2), []int{2, 50, 60}) {
		t.Fatalf("control survivors: %v", cacheOrder(r2))
	}
}

// 被淘汰页可以包含本次刚读的页（极小缓存 + 大预读）。
func TestEvictJustReadPage(t *testing.T) {
	r, _ := NewReadAhead(100, 2, 8, 2)
	// Read(0,1)：需求 0；预读 1,2；缓存超限淘汰最旧的 0（本次刚读）。
	d, a, e, _ := r.Read(0, 1)
	if !eqInts(d, []int{0}) || !eqInts(a, []int{1, 2}) || !eqInts(e, []int{0}) {
		t.Fatalf("Read(0,1): %v %v %v", d, a, e)
	}
	if !reflect.DeepEqual(cacheOrder(r), []int{1, 2}) {
		t.Fatalf("survivors: %v", cacheOrder(r))
	}
}

// pf 页被读命中后再被淘汰不触发收缩；未读即被淘汰触发收缩。
func TestPFClearedOnHitNoShrink(t *testing.T) {
	// 未读即被淘汰：wsz 8 -> 4。
	r, _ := NewReadAhead(100, 2, 8, 4)
	if _, _, _, _ = r.Read(0, 1); true {
	}
	if _, _, _, _ = r.Read(2, 1); true {
	} // 窗口 (3,4)，淘汰 0,1,2（1,2 是未读 pf 页）
	if st := r.State(); st.Window != (Window{3, 2}) {
		t.Fatalf("unread pf eviction must shrink 4->2: %+v", st)
	}

	// 命中清除 pf 后淘汰不收缩：构造窗口 (3,4)，先命中清 pf，再触发预读。
	r2, _ := NewReadAhead(100, 2, 8, 6)
	if _, _, _, _ = r2.Read(0, 1); true {
	}
	// Cp=6：缓存 0,1,2（pf 1,2）。命中 1、2 清 pf 并更新次序。
	if _, _, _, _ = r2.Read(1, 1); true {
	}
	if _, _, _, _ = r2.Read(2, 1); true {
	} // 触发异步预读 3..6
	// 缓存：旧 pf 页均已被读命中，新预读页尚未淘汰，无 pf 淘汰 -> 不收缩。
	if st := r2.State(); st.Window != (Window{3, 4}) {
		t.Fatalf("cleared pf must not shrink: %+v", st)
	}
}

// wsz 为 1 时收缩仍为 1（通过白盒置状态构造，因为公开操作中 wsz=1 的窗口
// 在同一轮同步预读内不会因自身新预读页被淘汰而触发；这里直接验证收缩规则）。
func TestShrinkFloorAtOne(t *testing.T) {
	r, _ := NewReadAhead(100, 2, 8, 5)

	// 先公开验证 2 -> 1：窗口 (1,2)，制造一次「淘汰含未读 pf 页且窗口保留」。
	// 白盒构造：wsz=2、缓存 [7(pf 未读), 9]，顺序读未命中页 10 触发同步预读
	// s=max(2,4,1)=4，窗口变 (11,4)，插入 10,11,12,13,14 后最旧页 7 被淘汰 -> 4->2。
	r.mu.Lock()
	r.wsz, r.ws, r.mk, r.prev = 2, 9, 10, 9
	r.cache = []int{7, 9}
	r.pf = map[int]bool{7: true, 9: false}
	r.mu.Unlock()
	d, a, e, _ := r.Read(10, 1)
	if !eqInts(d, []int{10}) || !eqInts(a, []int{11, 12, 13, 14}) || !eqInts(e, []int{7, 9}) {
		t.Fatalf("4->2 case: demand=%v ra=%v ev=%v", d, a, e)
	}
	if st := r.State(); st.Window != (Window{11, 2}) {
		t.Fatalf("shrink 4->2: %+v", st)
	}

	// 同样路径把 wsz 预置为 1：新窗口 s=2，pf 页被淘汰 -> 2->1。
	r.mu.Lock()
	r.wsz, r.ws, r.mk, r.prev = 1, 19, 19, 19
	r.cache = []int{13, 14, 19}
	r.pf = map[int]bool{13: true, 14: true, 19: false}
	r.mu.Unlock()
	d, a, e, _ = r.Read(20, 1)
	if !eqInts(d, []int{20}) || !eqInts(a, []int{21, 22}) || !eqInts(e, []int{13}) {
		t.Fatalf("2->1 case: demand=%v ra=%v ev=%v", d, a, e)
	}
	if st := r.State(); st.Window != (Window{21, 1}) {
		t.Fatalf("shrink 2->1: %+v", st)
	}

	// wsz 已经是 1 时再发生同样淘汰：新 s=2，收缩 floor(2/2)=1，不再下降。
	r.mu.Lock()
	r.wsz, r.ws, r.mk, r.prev = 1, 29, 29, 29
	r.cache = []int{23, 24, 29}
	r.pf = map[int]bool{23: true, 24: true, 29: false}
	r.mu.Unlock()
	d, a, e, _ = r.Read(30, 1)
	if !eqInts(d, []int{30}) || !eqInts(a, []int{31, 32}) || !eqInts(e, []int{23}) {
		t.Fatalf("floor case: demand=%v ra=%v ev=%v", d, a, e)
	}
	if st := r.State(); st.Window != (Window{31, 1}) {
		t.Fatalf("wsz must stay at floor 1: %+v", st)
	}
}

// 窗口已清空时淘汰 pf 页不改窗口（wsz 保持 0）。
func TestEvictPFWithoutWindowNoChange(t *testing.T) {
	r, _ := NewReadAhead(100, 2, 8, 3)
	// 顺序首次读建立缓存 0(f),1(t),2(t)，窗口 (1,2)。
	if _, _, _, _ = r.Read(0, 1); true {
	}
	// 再命中一次页 0（全命中、mk=2 不在 [0,1)）：次序变为 [1,2,0]。
	if d, a, e, _ := r.Read(0, 1); len(d) != 0 || len(a) != 0 || len(e) != 0 {
		t.Fatalf("re-hit page 0: %v %v %v", d, a, e)
	}
	if !reflect.DeepEqual(cacheOrder(r), []int{1, 2, 0}) {
		t.Fatalf("order: %v", cacheOrder(r))
	}
	// 随机未命中：窗口清空；新读页 50(f) 插入后淘汰最旧页 1（pf 未读）。
	d, a, e, _ := r.Read(50, 1)
	if !eqInts(d, []int{50}) || len(a) != 0 || !eqInts(e, []int{1}) {
		t.Fatalf("random miss eviction: demand=%v ra=%v ev=%v", d, a, e)
	}
	st := r.State()
	if st.HasWindow || st.Window.WSz != 0 {
		t.Fatalf("window must remain cleared: %+v", st)
	}
}

// 构造参数非法时拒绝。
func TestConstructorRejectsBadArgs(t *testing.T) {
	cases := []struct{ n, i, m, cp int }{
		{0, 1, 1, 1},
		{10, 0, 1, 1},
		{10, 5, 4, 1},
		{10, 1, 1, 0},
		{-1, 1, 1, 1},
	}
	for idx, c := range cases {
		if r, err := NewReadAhead(c.n, c.i, c.m, c.cp); err == nil || r != nil {
			t.Fatalf("case %d %+v must be rejected, got %v %v", idx, c, r, err)
		} else if !errors.Is(err, ErrInvalidArgument) {
			t.Fatalf("case %d: errors.Is mismatch: %v", idx, err)
		}
	}
}

// 被拒绝的 Read 不改变缓存、LRU 次序、pf、prev、窗口与标记页；
// 错误必须可用 errors.Is 区分，且先报非法参数再报越界。
func TestRejectedReadDoesNotMutate(t *testing.T) {
	r, _ := NewReadAhead(10, 2, 8, 4)
	if _, _, _, _ = r.Read(0, 1); true {
	} // 建立状态
	beforeCache := cacheOrder(r)
	beforePF := map[int]bool{}
	for k, v := range r.pf {
		beforePF[k] = v
	}
	beforeState := r.State()

	type call struct {
		p, n   int
		target error
	}
	calls := []call{
		{-1, 1, ErrInvalidArgument},
		{0, 0, ErrInvalidArgument},
		{5, -2, ErrInvalidArgument},
		{9, 2, ErrOutOfRange}, // p+n=11 > N=10
		{10, 1, ErrOutOfRange},
		{-1, 100, ErrInvalidArgument}, // 同时非法且越界：先报非法
	}
	for idx, c := range calls {
		d, a, e, err := r.Read(c.p, c.n)
		if err == nil || !errors.Is(err, c.target) {
			t.Fatalf("call %d Read(%d,%d): want %v, got %v", idx, c.p, c.n, c.target, err)
		}
		if d != nil || a != nil || e != nil {
			t.Fatalf("call %d rejected read must return nil lists", idx)
		}
		if !reflect.DeepEqual(cacheOrder(r), beforeCache) {
			t.Fatalf("call %d cache order changed: %v vs %v", idx, cacheOrder(r), beforeCache)
		}
		if !reflect.DeepEqual(r.pf, beforePF) {
			t.Fatalf("call %d pf changed: %v vs %v", idx, r.pf, beforePF)
		}
		if st := r.State(); st != beforeState {
			t.Fatalf("call %d state changed: %+v vs %+v", idx, st, beforeState)
		}
	}
}

// DropCache 保留 prev、窗口与标记页；连续调用安全。
func TestDropCachePreservesWindowState(t *testing.T) {
	r, _ := NewReadAhead(100, 2, 8, 4)
	if _, _, _, _ = r.Read(0, 1); true {
	}
	before := r.State()
	r.DropCache()
	r.DropCache()
	if after := r.State(); after != before {
		t.Fatalf("state changed: %+v vs %+v", after, before)
	}
	if len(cacheOrder(r)) != 0 || len(r.pf) != 0 {
		t.Fatalf("cache not empty: %v pf=%v", cacheOrder(r), r.pf)
	}
}

// 不变量：任何时刻缓存页数不超过 Cp；需求读与预读互不相交且都在 [0,N)；
// 两次 DropCache 之间同一页被再次发起读之前必已离开缓存（被淘汰）；wsz 不超过 M。
func TestInvariantsDuringSequence(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	for iter := 0; iter < 200; iter++ {
		n := 1 + rng.Intn(60)
		i := 1 + rng.Intn(6)
		m := i + rng.Intn(20)
		cp := 1 + rng.Intn(8)
		r, err := NewReadAhead(n, i, m, cp)
		if err != nil {
			t.Fatal(err)
		}
		// inCache 独立记录自上次 DropCache 以来缓存成员关系事件。
		inCache := map[int]bool{}
		for step := 0; step < 60; step++ {
			if rng.Intn(8) == 0 {
				r.DropCache()
				inCache = map[int]bool{}
				continue
			}
			p := rng.Intn(n)
			nlen := 1 + rng.Intn(n-p)
			d, a, e, err := r.Read(p, nlen)
			if err != nil {
				t.Fatalf("iter %d step %d Read(%d,%d) n=%d: %v", iter, step, p, nlen, n, err)
			}
			if len(cacheOrder(r)) > cp {
				t.Fatalf("cache exceeds capacity: %d > %d", len(cacheOrder(r)), cp)
			}
			issued := map[int]bool{}
			checkPage := func(pg int, kind string) {
				t.Helper()
				if pg < 0 || pg >= n {
					t.Fatalf("%s page out of range: %d", kind, pg)
				}
				if issued[pg] {
					t.Fatalf("demand/readahead intersect or duplicate at %d", pg)
				}
				issued[pg] = true
				if inCache[pg] {
					t.Fatalf("page %d re-issued (%s) before eviction", pg, kind)
				}
			}
			for _, pg := range d {
				checkPage(pg, "demand")
			}
			for _, pg := range a {
				checkPage(pg, "readahead")
			}
			for _, pg := range d {
				inCache[pg] = true
			}
			for _, pg := range a {
				inCache[pg] = true
			}
			for _, pg := range e {
				if !inCache[pg] {
					t.Fatalf("page %d evicted but was not cached", pg)
				}
				delete(inCache, pg)
			}
			if st := r.State(); st.Window.WSz > m {
				t.Fatalf("wsz %d exceeds M %d", st.Window.WSz, m)
			}
		}
	}
}

// 并发调用：结果等价于某个串行顺序（数据竞争由 -race 检查）。
func TestConcurrentAccess(t *testing.T) {
	r, _ := NewReadAhead(300, 4, 32, 16)
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(seed int64) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(seed))
			for k := 0; k < 300; k++ {
				switch rng.Intn(10) {
				case 0:
					r.DropCache()
				case 1:
					_ = r.State()
				default:
					p := rng.Intn(300)
					nlen := 1 + rng.Intn(300-p)
					_, _, _, _ = r.Read(p, nlen)
				}
			}
		}(int64(g + 1))
	}
	wg.Wait()
	if len(cacheOrder(r)) > r.cp {
		t.Fatalf("final cache exceeds capacity")
	}
}

// 与朴素模拟器对照的小段序列（含日志，打印输入、输出与判定依据）。
func TestCompareNaiveLogged(t *testing.T) {
	type op struct {
		p, n int
		drop bool
	}
	ops := []op{
		{0, 1, false}, {1, 1, false}, {2, 1, false}, {3, 1, false},
		{4, 1, false}, {5, 1, false},
		{0, 0, true},
		{5, 1, false}, {6, 2, false},
		{40, 3, false}, {43, 1, false},
	}
	n, i, m, cp := 100, 2, 8, 4
	r, _ := NewReadAhead(n, i, m, cp)
	s := newNaive(n, i, m, cp)
	var log strings.Builder
	fmt.Fprintf(&log, "config N=%d I=%d M=%d Cp=%d\n", n, i, m, cp)
	for idx, o := range ops {
		if o.drop {
			r.DropCache()
			s.dropCache()
			fmt.Fprintf(&log, "step %d: DropCache\n", idx)
			continue
		}
		d, a, e, err := r.Read(o.p, o.n)
		if err != nil {
			t.Fatalf("step %d: %v", idx, err)
		}
		nr := s.read(o.p, o.n)
		fmt.Fprintf(&log, "step %d: Read(%d,%d) [%s]\n         demand=%s readahead=%s evicted=%s\n",
			idx, o.p, o.n, nr.branch, intsStr(d), intsStr(a), intsStr(e))
		if !eqInts(d, nr.demand) || !eqInts(a, nr.readahead) || !eqInts(e, nr.evicted) {
			t.Fatalf("step %d output mismatch:\n%s", idx, log.String())
		}
		if r.State() != naiveState(s) {
			t.Fatalf("step %d state mismatch: real=%+v naive=%+v\n%s", idx, r.State(), naiveState(s), log.String())
		}
		if !reflect.DeepEqual(cacheOrder(r), naiveCacheOrder(s)) {
			t.Fatalf("step %d cache order mismatch: %v vs %v", idx, cacheOrder(r), naiveCacheOrder(s))
		}
	}
	t.Log("\n" + log.String())
}

// 2000 组随机读序列与朴素模拟器逐步对照，Cp 取多种小值；
// 每组日志含输入、输出与判定依据（失败时随断言输出，成功时抽样 t.Log）。
func TestRandomCompareNaive2000(t *testing.T) {
	rng := rand.New(rand.NewSource(20261001))
	cpChoices := []int{1, 2, 3, 4, 5, 8}
	sampleLogs := make([]string, 0, 5)
	for iter := 0; iter < 2000; iter++ {
		n := 1 + rng.Intn(40)
		i := 1 + rng.Intn(5)
		m := i + rng.Intn(16)
		cp := cpChoices[rng.Intn(len(cpChoices))]
		r, err := NewReadAhead(n, i, m, cp)
		if err != nil {
			t.Fatal(err)
		}
		s := newNaive(n, i, m, cp)
		var log strings.Builder
		fmt.Fprintf(&log, "iter %d config N=%d I=%d M=%d Cp=%d\n", iter, n, i, m, cp)
		steps := 1 + rng.Intn(40)
		for step := 0; step < steps; step++ {
			if rng.Intn(6) == 0 {
				r.DropCache()
				s.dropCache()
				fmt.Fprintf(&log, "  step %d: DropCache\n", step)
				continue
			}
			p := rng.Intn(n)
			nlen := 1 + rng.Intn(n-p)
			d, a, e, err := r.Read(p, nlen)
			if err != nil {
				t.Fatalf("iter %d step %d Read(%d,%d): %v", iter, step, p, nlen, err)
			}
			nr := s.read(p, nlen)
			fmt.Fprintf(&log, "  step %d: Read(%d,%d) [%s] demand=%s ra=%s ev=%s\n",
				step, p, nlen, nr.branch, intsStr(nr.demand), intsStr(nr.readahead), intsStr(nr.evicted))
			if !eqInts(d, nr.demand) || !eqInts(a, nr.readahead) || !eqInts(e, nr.evicted) {
				t.Fatalf("iter %d step %d output mismatch:\n%sreal=%s %s %s",
					iter, step, log.String(), intsStr(d), intsStr(a), intsStr(e))
			}
			if r.State() != naiveState(s) {
				t.Fatalf("iter %d step %d state mismatch: real=%+v naive=%+v\n%s",
					iter, step, r.State(), naiveState(s), log.String())
			}
			if !reflect.DeepEqual(cacheOrder(r), naiveCacheOrder(s)) {
				t.Fatalf("iter %d step %d cache order mismatch: %v vs %v\n%s",
					iter, step, cacheOrder(r), naiveCacheOrder(s), log.String())
			}
		}
		if iter%400 == 0 {
			sampleLogs = append(sampleLogs, log.String())
		}
	}
	for idx, sample := range sampleLogs {
		t.Logf("sample %d:\n%s", idx, sample)
	}
}

// 相同读序列重放得到完全相同的需求读、预读、淘汰页列表与状态。
func TestDeterministicReplay(t *testing.T) {
	rng := rand.New(rand.NewSource(77))
	type op struct {
		p, n int
		drop bool
	}
	for iter := 0; iter < 50; iter++ {
		n := 1 + rng.Intn(50)
		i := 1 + rng.Intn(4)
		m := i + rng.Intn(12)
		cp := 1 + rng.Intn(6)
		ops := make([]op, 30)
		for idx := range ops {
			if rng.Intn(7) == 0 {
				ops[idx] = op{drop: true}
				continue
			}
			p := rng.Intn(n)
			ops[idx] = op{p: p, n: 1 + rng.Intn(n-p)}
		}

		run := func() ([][3][]int, StateSnapshot, []int) {
			r, _ := NewReadAhead(n, i, m, cp)
			out := [][3][]int{}
			for _, o := range ops {
				if o.drop {
					r.DropCache()
					continue
				}
				d, a, e, err := r.Read(o.p, o.n)
				if err != nil {
					t.Fatal(err)
				}
				out = append(out, [3][]int{d, a, e})
			}
			return out, r.State(), cacheOrder(r)
		}

		out1, st1, c1 := run()
		out2, st2, c2 := run()
		if !reflect.DeepEqual(out1, out2) || st1 != st2 || !reflect.DeepEqual(c1, c2) {
			t.Fatalf("iter %d replay mismatch:\nout1=%v\nout2=%v\n%+v vs %+v\n%v vs %v",
				iter, out1, out2, st1, st2, c1, c2)
		}
	}
}

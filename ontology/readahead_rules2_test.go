package ontology

import (
	"errors"
	"fmt"
	"sync"
	"testing"
)

// 命中更新最近使用次序，从而改变淘汰对象。
func TestRuleHitUpdatesLRU(t *testing.T) {
	// Cp=1：Read(0,1) 同步预读 1，淘汰最久未用的 0。
	r := mustNew(t, 100, 1, 8, 1)
	_, _, e, _ := r.Read(0, 1)
	if !eqSlices(e, []int{0}) {
		t.Fatalf("缓存 0,1 后应淘汰最久未用的 0, got %v", e)
	}

	// 用互不相邻的随机读建立 LRU=[10,30,50]（随机读不预读、不干扰次序）。
	r = mustNew(t, 100, 4, 16, 3)
	r.Read(10, 1)              // LRU=[10]
	r.Read(30, 1)              // 随机：LRU=[10,30]
	r.Read(50, 1)              // 随机：LRU=[10,30,50]
	r.Read(10, 1)              // 命中 10（仍相对 prev=50 为随机）：LRU=[30,50,10]，并清 pf
	_, _, e, _ = r.Read(70, 1) // 插入 70：LRU=[30,50,10,70]，淘汰最久未用的 [30]
	if !eqSlices(e, []int{30}) {
		t.Fatalf("命中提升 10 后，30 才是最久未用, got %v", e)
	}
	// 对照：不去命中 10，则 10 仍是最久未用。
	r = mustNew(t, 100, 4, 16, 3)
	r.Read(10, 1)
	r.Read(30, 1)
	r.Read(50, 1)
	_, _, e, _ = r.Read(70, 1) // LRU=[10,30,50,70]，淘汰 [10]
	if !eqSlices(e, []int{10}) {
		t.Fatalf("未命中提升时 10 最久未用, got %v", e)
	}
}

// 淘汰可能淘汰本次刚读的页。
func TestRuleEvictsJustReadPage(t *testing.T) {
	r := mustNew(t, 100, 1, 8, 1)
	// Read(0,1): demand{0} ahead{1} -> LRU[0,1] -> 淘汰本次刚读的 0。
	_, _, e, _ := r.Read(0, 1)
	if !eqSlices(e, []int{0}) {
		t.Fatalf("应淘汰本次刚读的需求页 0, got %v", e)
	}
}

// pf 页被读命中后再被淘汰不触发收缩；未读即被淘汰触发收缩。
func TestRulePFFlagShrink(t *testing.T) {
	// 未读 pf 被淘汰 -> 收缩。
	r := mustNew(t, 100, 1, 8, 2)
	// Read(0,1)：I=1 -> 窗口 (1,1) mk=1，ahead{1}，缓存 [0,1pf]。
	_, a, _, _ := r.Read(0, 1)
	if !eqSlices(a, []int{1}) {
		t.Fatalf("ahead={1}, got %v", a)
	}
	// 读 1：全命中 mk=1 -> 异步 s=2 rs=2 ahead{2,3}，窗口 (2,2) mk=3；
	// 命中 1 清 pf；插入后 LRU=[0,1,2,3]，Cp=2 淘汰 [0,1]（均非 pf）不缩。
	_, a, e, _ := r.Read(1, 1)
	if !eqSlices(a, []int{2, 3}) || !eqSlices(e, []int{0, 1}) {
		t.Fatalf("Read(1,1): a=%v e=%v", a, e)
	}
	if st := r.State(); st.Window != (Window{2, 2}) {
		t.Fatalf("pf 已被命中清除，淘汰不应收缩, got %+v", st)
	}
	// 读 mk=3（全命中）：异步 s=4 rs=4 ahead{4,5,6,7}，
	// LRU=[2,3,4,5,6,7] -> 淘汰 [2,3,4,5]，4、5 未读 pf -> 4 收缩为 2，
	// ws=4、mk=6 不变。
	_, a, e, _ = r.Read(3, 1)
	if !eqSlices(a, []int{4, 5, 6, 7}) || !eqSlices(e, []int{2, 3, 4, 5}) {
		t.Fatalf("Read(3,1): a=%v e=%v", a, e)
	}
	if st := r.State(); st.Window != (Window{4, 2}) || st.Mk != 6 {
		t.Fatalf("未读 pf 淘汰后 wsz 应由 4 收缩为 2（ws、mk 不变）, got %+v", st)
	}

	// pf 页先被读命中（清除 pf）再被淘汰 -> 不收缩。
	// Cp=2,I=1：Read(0,1) 后缓存 [0,1pf]，窗口 (1,1) mk=1。
	r = mustNew(t, 100, 1, 8, 2)
	r.Read(0, 1)
	// 读 1：全命中且 mk=1 在请求内 -> 异步 s=2 rs=2 ahead={2,3}；
	// 命中 1 清 pf；淘汰 [0,1]（0 是需求页、1 刚被命中，pf 均为 false）-> 不缩。
	_, _, e, _ = r.Read(1, 1)
	if !eqSlices(e, []int{0, 1}) {
		t.Fatalf("淘汰 [0,1], got %v", e)
	}
	if st := r.State(); st.Window != (Window{2, 2}) {
		t.Fatalf("被读命中的 pf 页淘汰不应触发收缩, got %+v", st)
	}
}

// wsz 为 1 时发生收缩仍保持 1：max(1, floor(1/2))=1。
func TestRuleShrinkFloorIsOne(t *testing.T) {
	if got := shrinkSize(1); got != 1 {
		t.Fatalf("shrinkSize(1)=%d, 期望 1", got)
	}
	if got := shrinkSize(2); got != 1 {
		t.Fatalf("shrinkSize(2)=%d, 期望 1", got)
	}
	if got := shrinkSize(3); got != 1 {
		t.Fatalf("shrinkSize(3)=%d, 期望 1", got)
	}
	if got := shrinkSize(8); got != 4 {
		t.Fatalf("shrinkSize(8)=%d, 期望 4", got)
	}

	// 端到端验证抖动收缩 2->1 且 ws/mk 不变。Cp=1,I=1：
	// Read(0,1)：demand{0} ahead{1}，窗口(1,1)mk=1，淘汰 0，缓存 {1pf}。
	r := mustNew(t, 100, 1, 8, 1)
	d, a, e, _ := r.Read(0, 1)
	if !eqSlices(d, []int{0}) || !eqSlices(a, []int{1}) || !eqSlices(e, []int{0}) {
		t.Fatalf("Read(0,1) 判定不符: d=%v a=%v e=%v", d, a, e)
	}
	// Read(1,1)：全命中 mk=1 -> 异步 s=2 rs=2 ahead{2,3}，窗口(2,2)mk=3；
	// 命中 1 清 pf；淘汰 [1(pf=false),2(pf=true)]，2 是未读 pf -> 收缩为 1，
	// ws=2、mk=3 不变。
	_, a, e, _ = r.Read(1, 1)
	if !eqSlices(a, []int{2, 3}) || !eqSlices(e, []int{1, 2}) {
		t.Fatalf("Read(1,1) 判定不符: a=%v e=%v", a, e)
	}
	if st := r.State(); st.Window != (Window{2, 1}) || st.Mk != 3 {
		t.Fatalf("未读 pf 淘汰后 wsz 应由 2 收缩为 1 且 ws/mk 不变, got %+v", st)
	}
}

// 窗口已清空时淘汰 pf 页不修改窗口。
func TestRuleEvictPFWithoutWindow(t *testing.T) {
	r := mustNew(t, 100, 4, 16, 2)
	r.Read(0, 1) // 窗口 (1,4)，缓存淘汰至 [3,4]（均为未读 pf）
	// 随机读 50：未命中、不顺序 -> 不预读，窗口清空；插入需求页 50，
	// 淘汰最久未用的 3（pf=true），但窗口已空 -> 不改窗口。
	_, _, e, _ := r.Read(50, 1)
	if !eqSlices(e, []int{3}) {
		t.Fatalf("随机读应淘汰最久未用页（含 pf）, got %v", e)
	}
	if st := r.State(); st.Window.Wsz != 0 || st.HasMk {
		t.Fatalf("窗口已清空时淘汰 pf 页不得重建/修改窗口, got %+v", st)
	}
}

// 被拒绝的 Read 不改变缓存、LRU、pf、prev、窗口与标记页。
func TestRuleRejectionKeepsState(t *testing.T) {
	for _, tc := range []struct {
		name    string
		p, n    int
		wantErr error
	}{
		{"p 为负", -1, 1, ErrInvalidArg},
		{"n 为 0", 0, 0, ErrInvalidArg},
		{"n 为负", 0, -2, ErrInvalidArg},
		{"负 p 优先于越界", -1, 1000, ErrInvalidArg},
		{"越界", 9, 2, ErrOutOfRange},
		{"p 等于 N", 10, 1, ErrOutOfRange},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := mustNew(t, 10, 2, 8, 3)
			r.Read(0, 1)
			before := r.State()

			d, a, e, err := r.Read(tc.p, tc.n)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("errors.Is(err,%v)=false, err=%v", tc.wantErr, err)
			}
			if d != nil || a != nil || e != nil {
				t.Fatalf("被拒绝调用不得返回页集合, got d=%v a=%v e=%v", d, a, e)
			}
			if got := r.State(); got != before {
				t.Fatalf("状态被改变: before=%+v after=%+v", before, got)
			}
			// 缓存内容也应不变：再读 1（已缓存）应全命中且不触发新预读。
			d, a, e, _ = r.Read(1, 1)
			if len(d) != 0 || len(a) != 0 || len(e) != 0 {
				t.Fatalf("拒绝后缓存被污染: d=%v a=%v e=%v", d, a, e)
			}
		})
	}
}

// 构造参数非法时整体拒绝（errors.Is 可区分）。
func TestRuleInvalidConfig(t *testing.T) {
	for _, c := range [][4]int{
		{0, 1, 1, 1},
		{10, 0, 1, 1},
		{10, 5, 4, 1}, // M < I
		{10, 1, 2, 0},
	} {
		if _, err := NewReadahead(c[0], c[1], c[2], c[3]); !errors.Is(err, ErrInvalidConfig) {
			t.Fatalf("配置 %v 应被拒绝, got %v", c, err)
		}
	}
}

// Read、DropCache、State 可被并发调用，结果等价于某个串行顺序。
func TestRuleConcurrentSafe(t *testing.T) {
	r := mustNew(t, 500, 2, 16, 8)
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(off int) {
			defer wg.Done()
			for k := 0; k < 300; k++ {
				p := (off + k) % 500
				n := 1 + (k % 4)
				if p+n > 500 {
					n = 500 - p
				}
				if n < 1 {
					_, _, _, _ = r.Read(0, 1)
					continue
				}
				_, _, _, _ = r.Read(p, n)
				_ = r.State()
				if k%20 == 0 {
					r.DropCache()
				}
			}
		}(g * 7)
	}
	wg.Wait()
	// 任何时刻缓存页数不大于 Cp 的不变量在实现内保证；这里仅做状态健全性检查。
	st := r.State()
	if st.Window.Wsz < 0 || st.Window.Wsz > 16 {
		t.Fatalf("并发后窗口非法: %+v", st)
	}
}

// 两次 DropCache 之间同一页被再次发起读之前必已被淘汰。
func TestRuleRereadAfterDropCacheIsDemand(t *testing.T) {
	r := mustNew(t, 100, 4, 16, 50)
	r.Read(0, 1) // 0 为需求页
	r.DropCache()
	d, a, _, _ := r.Read(0, 1)
	// 相对 prev=0，p=0 非顺序 -> 不预读；0 已被 DropCache 淘汰，故为需求读。
	if !eqSlices(d, []int{0}) {
		t.Fatalf("DropCache 后再次读 0 必须重新发起需求读, got d=%v a=%v", d, a)
	}
	fmt.Println("判定依据：DropCache 清空 pages/lru 但保留 prev，故同页再读落入 missing。")
}

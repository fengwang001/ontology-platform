package btree

import (
	"errors"
	"fmt"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
)

func mustNew(t *testing.T, capacity, pageLimit int) *Tree {
	t.Helper()
	tr, err := New(capacity, pageLimit)
	if err != nil {
		t.Fatalf("New(%d, %d): %v", capacity, pageLimit, err)
	}
	return tr
}

func mustInsert(t *testing.T, tr *Tree, key string, size int) {
	t.Helper()
	if err := tr.Insert(key, size); err != nil {
		t.Fatalf("Insert(%q, %d): %v", key, size, err)
	}
}

func mustDelete(t *testing.T, tr *Tree, key string) {
	t.Helper()
	if err := tr.Delete(key); err != nil {
		t.Fatalf("Delete(%q): %v", key, err)
	}
}

func pv(id, occ int, keys ...string) PageView {
	return PageView{ID: id, Keys: keys, Occupancy: occ}
}

func assertPages(t *testing.T, tr *Tree, want ...PageView) {
	t.Helper()
	got := tr.Pages()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Pages() = %+v, want %+v", got, want)
	}
}

// 题目例一：五条 20 恰好占满不切分；第六条触发切分 j=3 差为 0；
// Delete d 后左借会使 L=40<M 不行，无右邻，并左 40+60=100<=C 成立。
func TestPromptExample1(t *testing.T) {
	tr := mustNew(t, 100, 10)
	for _, k := range []string{"a", "b", "c", "d", "e"} {
		mustInsert(t, tr, k, 20)
	}
	assertPages(t, tr, pv(1, 100, "a", "b", "c", "d", "e"))
	mustInsert(t, tr, "f", 20)
	assertPages(t, tr,
		pv(1, 60, "a", "b", "c"),
		pv(2, 60, "d", "e", "f"))
	mustDelete(t, tr, "d")
	assertPages(t, tr, pv(1, 100, "a", "b", "c", "e", "f"))
}

// 题目例二：最左页下溢只试右侧；右借 d 后页 2 分隔键变为 e。
func TestPromptExample2(t *testing.T) {
	tr := mustNew(t, 100, 10)
	for _, k := range []string{"a", "b", "c", "d", "e", "f", "g", "h"} {
		mustInsert(t, tr, k, 20)
	}
	assertPages(t, tr,
		pv(1, 60, "a", "b", "c"),
		pv(2, 100, "d", "e", "f", "g", "h"))
	mustDelete(t, tr, "a")
	assertPages(t, tr,
		pv(1, 60, "b", "c", "d"),
		pv(2, 80, "e", "f", "g", "h"))
	// 分隔键已重算为 e：d1 < e 应进入页 1 而不是页 2。
	mustInsert(t, tr, "d1", 20)
	assertPages(t, tr,
		pv(1, 80, "b", "c", "d", "d1"),
		pv(2, 80, "e", "f", "g", "h"))
}

// 切分时差相等取较小的 j：j=2 与 j=3 差均为 15，取 j=2。
func TestSplitTiePicksSmallerJ(t *testing.T) {
	tr := mustNew(t, 60, 10) // M=30, Emax=15
	for _, k := range []string{"a", "b", "c", "d"} {
		mustInsert(t, tr, k, 15)
	}
	assertPages(t, tr, pv(1, 60, "a", "b", "c", "d"))
	mustInsert(t, tr, "e", 15) // 75>60，j=2 与 j=3 差均为 15
	assertPages(t, tr,
		pv(1, 30, "a", "b"),
		pv(2, 45, "c", "d", "e"))
}

// 左借取最少条数：U=30 借 1 条 25 即达 55>=M=50，不多借；
// 同时验证借出方取走后恰等于 M（75-25=50）是允许的。
func TestLeftBorrowMinimalCountOvershoots(t *testing.T) {
	tr := mustNew(t, 100, 10)
	for _, k := range []string{"a", "b", "c", "d"} {
		mustInsert(t, tr, k, 25)
	}
	mustInsert(t, tr, "e", 25) // 125>100，j=2 与 j=3 差均为 25，取 j=2
	assertPages(t, tr,
		pv(1, 50, "a", "b"),
		pv(2, 75, "c", "d", "e"))
	mustInsert(t, tr, "a1", 25) // 入页 1：[a,a1,b]=75
	mustDelete(t, tr, "e")      // 页 2 余 50，不下溢
	mustInsert(t, tr, "c1", 5)  // 入页 2：[c,c1,d]=55
	mustDelete(t, tr, "d")      // U=[c,c1]=30 下溢，左借 b(25) 一条即够
	assertPages(t, tr,
		pv(1, 50, "a", "a1"),
		pv(2, 55, "b", "c", "c1"))
}

// 借出方取走后为 M-1 不允许：左借失败后并左 74+30=104>C 也不行，
// 四步都不满足，保持下溢不算错误。
func TestBorrowDonorBelowMRejectedAndUnderflowKept(t *testing.T) {
	tr := mustNew(t, 100, 10)
	mustInsert(t, tr, "a", 24)
	for _, k := range []string{"b", "c", "d", "e"} {
		mustInsert(t, tr, k, 25)
	}
	// 124>100，j=3 差 0：L=[a,b,c]=74，R=[d,e]=50
	assertPages(t, tr,
		pv(1, 74, "a", "b", "c"),
		pv(2, 50, "d", "e"))
	mustInsert(t, tr, "f", 5) // 入页 2
	mustDelete(t, tr, "d")    // U=[e,f]=30 下溢
	// 左借 c(25)：U=55 够，但 L=49<M 不行；无右邻；
	// 并左 74+30=104>100 不行；无右邻可并。保持下溢。
	assertPages(t, tr,
		pv(1, 74, "a", "b", "c"),
		pv(2, 30, "e", "f"))
}

// 并合恰等于 C 允许（例一已覆盖 40+60=100）；这里覆盖 C+1 不允许。
func TestMergeExactlyCAllowedAndCPlusOneRejected(t *testing.T) {
	// 60+40=100<=C：并左成立。
	tr := mustNew(t, 100, 10)
	for _, k := range []string{"a", "b", "c", "d", "e", "f"} {
		mustInsert(t, tr, k, 20)
	}
	assertPages(t, tr,
		pv(1, 60, "a", "b", "c"),
		pv(2, 60, "d", "e", "f"))
	mustDelete(t, tr, "f") // U=[d,e]=40，左借不行，并左 100<=100 成立
	assertPages(t, tr, pv(1, 100, "a", "b", "c", "d", "e"))

	// 60+41=101>C：并左不行，保持下溢。
	tr2 := mustNew(t, 100, 10)
	for _, kv := range [][2]int{{'a', 20}, {'b', 20}, {'c', 20}, {'d', 25}, {'e', 16}} {
		mustInsert(t, tr2, string(rune(kv[0])), kv[1])
	}
	// 101>100，j=3 差 19 最小：L=[a,b,c]=60，R=[d,e]=41
	assertPages(t, tr2,
		pv(1, 60, "a", "b", "c"),
		pv(2, 41, "d", "e"))
	mustInsert(t, tr2, "f", 1) // 入页 2，[d,e,f]=42
	mustDelete(t, tr2, "f")    // U=[d,e]=41 下溢
	// 左借 c(20)：U=61 够，但 L=40<M 不行；并左 101>100 不行。保持下溢。
	assertPages(t, tr2,
		pv(1, 60, "a", "b", "c"),
		pv(2, 41, "d", "e"))
}

// 左借优先于右借：两侧都能借时执行左借。
func TestLeftBorrowBeatsRightBorrow(t *testing.T) {
	tr := mustNew(t, 100, 10)
	for _, k := range []string{"a", "b", "c", "d", "e", "f", "g"} {
		mustInsert(t, tr, k, 25)
	}
	// 两次切分后：[a,b]50 [c,d]50 [e,f,g]75
	assertPages(t, tr,
		pv(1, 50, "a", "b"),
		pv(2, 50, "c", "d"),
		pv(3, 75, "e", "f", "g"))
	mustInsert(t, tr, "a1", 25) // 入页 1：[a,a1,b]=75
	mustInsert(t, tr, "c1", 5)  // 入页 2：[c,c1,d]=55
	mustDelete(t, tr, "c")      // U=[c1,d]=30 下溢，左右均可借，左借优先
	assertPages(t, tr,
		pv(1, 50, "a", "a1"),
		pv(2, 55, "b", "c1", "d"),
		pv(3, 75, "e", "f", "g"))
}

// 右借优先于并左：左借不行、并左可行，但右借可行时先右借。
func TestRightBorrowBeatsMergeLeft(t *testing.T) {
	tr := mustNew(t, 100, 10)
	for _, k := range []string{"a", "b", "c", "d", "e", "f", "g"} {
		mustInsert(t, tr, k, 25)
	}
	// [a,b]50 [c,d]50 [e,f,g]75
	mustInsert(t, tr, "c1", 5) // 入页 2：[c,c1,d]=55
	mustDelete(t, tr, "d")     // U=[c,c1]=30 下溢
	// 左借 b(25)：L=25<M 不行；并左 50+30=80<=100 可行，
	// 但右借 e(25) 可行（U=55，R=50），右借优先。
	assertPages(t, tr,
		pv(1, 50, "a", "b"),
		pv(2, 55, "c", "c1", "e"),
		pv(3, 50, "f", "g"))
}

// 并左优先于并右：两侧都借不动、两侧都可并时执行并左。
func TestMergeLeftBeatsMergeRight(t *testing.T) {
	tr := mustNew(t, 80, 10) // M=40, Emax=20
	for _, k := range []string{"a", "b", "c", "d", "e", "f", "g"} {
		mustInsert(t, tr, k, 20)
	}
	// [a,b]40 [c,d]40 [e,f,g]60
	assertPages(t, tr,
		pv(1, 40, "a", "b"),
		pv(2, 40, "c", "d"),
		pv(3, 60, "e", "f", "g"))
	mustDelete(t, tr, "g")     // 页 3 余 40=M，不下溢
	mustInsert(t, tr, "c1", 4) // 入页 2：[c,c1,d]=44
	mustDelete(t, tr, "d")     // U=[c,c1]=24 下溢
	// 左借 b(20)：L=20<M 不行；右借 e(20)：R=20<M 不行；
	// 并左 40+24=64<=80 成立（并右 24+40=64 也可行，但并左优先）。
	assertPages(t, tr,
		pv(1, 64, "a", "b", "c", "c1"),
		pv(3, 40, "e", "f"))
}

// 被删页为最左页时只试右侧：右借不行则并右，存活的是最左页（编号 1，无分隔键）。
func TestLeftmostPageMergeRight(t *testing.T) {
	tr := mustNew(t, 100, 10)
	for _, k := range []string{"a", "b", "c", "d", "e", "f"} {
		mustInsert(t, tr, k, 20)
	}
	assertPages(t, tr,
		pv(1, 60, "a", "b", "c"),
		pv(2, 60, "d", "e", "f"))
	mustDelete(t, tr, "a") // 最左页 U=[b,c]=40 下溢
	// 无左邻；右借 d(20)：R=40<M 不行；无左可并；并右 40+60=100<=100 成立。
	assertPages(t, tr, pv(1, 100, "b", "c", "d", "e", "f"))
}

// 删除页首键（不触发再平衡）后分隔键同步重算。
func TestDeleteFirstKeySeparatorRecomputed(t *testing.T) {
	tr := mustNew(t, 100, 10)
	for _, k := range []string{"a", "b", "c", "d", "e", "f", "g", "h"} {
		mustInsert(t, tr, k, 20)
	}
	// [a,b,c]60 [d,e,f,g,h]100，页 2 分隔键为 d
	mustDelete(t, tr, "d") // 页 2 余 80>=M，不再平衡；分隔键重算为 e
	assertPages(t, tr,
		pv(1, 60, "a", "b", "c"),
		pv(2, 80, "e", "f", "g", "h"))
	// d1 < e：若分隔键仍是 d 会误入页 2，重算后应入页 1。
	mustInsert(t, tr, "d1", 20)
	assertPages(t, tr,
		pv(1, 80, "a", "b", "c", "d1"),
		pv(2, 80, "e", "f", "g", "h"))
}

// 页编号不复用：释放过的编号不再出现，新页取下一个编号。
func TestPageIDNotReused(t *testing.T) {
	tr := mustNew(t, 100, 10)
	for _, k := range []string{"a", "b", "c", "d", "e", "f"} {
		mustInsert(t, tr, k, 20)
	}
	mustDelete(t, tr, "d") // 并左，页 2 释放
	assertPages(t, tr, pv(1, 100, "a", "b", "c", "e", "f"))
	mustInsert(t, tr, "g", 20) // 120>100 切分，新页编号为 3 而非复用 2
	assertPages(t, tr,
		pv(1, 60, "a", "b", "c"),
		pv(3, 60, "e", "f", "g"))
}

// 页数不足时拒绝且不消耗页编号。
func TestPageLimitRejectionKeepsStateAndID(t *testing.T) {
	tr := mustNew(t, 100, 2)
	for _, k := range []string{"a", "b", "c", "d", "e", "f", "g", "h"} {
		mustInsert(t, tr, k, 20)
	}
	assertPages(t, tr,
		pv(1, 60, "a", "b", "c"),
		pv(2, 100, "d", "e", "f", "g", "h"))
	before := tr.Pages()
	nextID := tr.nextID
	if err := tr.Insert("i", 20); !errors.Is(err, ErrPageLimit) {
		t.Fatalf("Insert(i) err = %v, want ErrPageLimit", err)
	}
	assertPages(t, tr, before...) // 状态不变
	if tr.nextID != nextID {
		t.Fatalf("nextID = %d, want %d（拒绝不得消耗页编号）", tr.nextID, nextID)
	}
	// 并回单页后再切分，新页编号仍为 3。
	mustDelete(t, tr, "h")
	mustDelete(t, tr, "g")
	mustDelete(t, tr, "f") // U=[d,e]=40，左借不行，并左 60+40=100 成立
	assertPages(t, tr, pv(1, 100, "a", "b", "c", "d", "e"))
	mustInsert(t, tr, "f", 20)
	assertPages(t, tr,
		pv(1, 60, "a", "b", "c"),
		pv(3, 60, "d", "e", "f"))
}

// 只剩一页时下溢不处理，不算错误。
func TestSinglePageUnderflowUntouched(t *testing.T) {
	tr := mustNew(t, 100, 10)
	mustInsert(t, tr, "a", 20)
	mustInsert(t, tr, "b", 20)
	mustDelete(t, tr, "a") // 余 20<M，但只剩一页
	assertPages(t, tr, pv(1, 20, "b"))
	mustDelete(t, tr, "b") // 唯一一页允许为空
	assertPages(t, tr, pv(1, 0))
}

// 参数非法统一拒绝，且先于键存在/不存在、页数不足判定；被拒绝的操作不改变状态。
func TestInvalidArgumentsAndRejectionOrder(t *testing.T) {
	for _, args := range [][2]int{{7, 1}, {1_000_001, 1}, {8, 0}, {8, 1_000_001}, {-1, -1}} {
		if _, err := New(args[0], args[1]); !errors.Is(err, ErrInvalidArgument) {
			t.Fatalf("New(%d, %d) err = %v, want ErrInvalidArgument", args[0], args[1], err)
		}
	}
	tr := mustNew(t, 100, 10) // Emax=25
	if err := tr.Insert("", 5); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("Insert empty key err = %v", err)
	}
	if err := tr.Insert("k", 0); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("Insert size 0 err = %v", err)
	}
	if err := tr.Insert("k", 26); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("Insert size>Emax err = %v", err)
	}
	if err := tr.Delete(""); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("Delete empty key err = %v", err)
	}
	mustInsert(t, tr, "k", 25)
	if err := tr.Insert("k", 25); !errors.Is(err, ErrKeyExists) {
		t.Fatalf("Insert dup err = %v, want ErrKeyExists", err)
	}
	// 参数非法优先于键已存在。
	if err := tr.Insert("k", 99); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("Insert dup+bad size err = %v, want ErrInvalidArgument", err)
	}
	if err := tr.Delete("k2"); !errors.Is(err, ErrKeyNotFound) {
		t.Fatalf("Delete missing err = %v, want ErrKeyNotFound", err)
	}
	// 参数非法优先于键不存在。
	if err := tr.Delete(""); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("Delete empty err = %v, want ErrInvalidArgument", err)
	}
	assertPages(t, tr, pv(1, 25, "k")) // 所有拒绝均未改变状态
}

// 定位页的分隔键比较次数不超过 ceil(log2 m)（m=1 时为 0）。
func TestLocateComparisonBound(t *testing.T) {
	tr := mustNew(t, 40, 300) // M=20, Emax=10
	for i := 0; i < 200; i++ {
		mustInsert(t, tr, fmt.Sprintf("k%03d", i), 10)
	}
	checkBound := func() {
		m := len(tr.pages)
		bound := 0
		for (1 << bound) < m {
			bound++
		}
		if tr.lastCmp > bound {
			t.Fatalf("lastCmp = %d > ceil(log2 %d) = %d", tr.lastCmp, m, bound)
		}
	}
	checkBound()
	for i := 0; i < 200; i += 7 {
		mustDelete(t, tr, fmt.Sprintf("k%03d", i))
		checkBound()
	}
	for i := 0; i < 50; i++ {
		mustInsert(t, tr, fmt.Sprintf("x%03d", i), 10)
		checkBound()
	}
}

// Pages() 返回拷贝，调用方修改不影响树。
func TestPagesReturnsCopy(t *testing.T) {
	tr := mustNew(t, 100, 10)
	mustInsert(t, tr, "a", 20)
	mustInsert(t, tr, "b", 20)
	got := tr.Pages()
	got[0].Keys[0] = "zzz"
	got[0].Occupancy = 999
	assertPages(t, tr, pv(1, 40, "a", "b"))
}

// 并发调用等价于某个串行顺序：尺寸总量守恒、键序与占用不变式保持。
// 需配合 -race 运行。
func TestConcurrentOps(t *testing.T) {
	tr := mustNew(t, 100, 1000)
	var wg sync.WaitGroup
	var inserted, deleted atomic.Int64
	done := make(chan struct{})
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 500; i++ {
				key := fmt.Sprintf("g%02d-%04d", g, i)
				if err := tr.Insert(key, 20); err == nil {
					inserted.Add(20)
				}
				if i%2 == 0 {
					if err := tr.Delete(key); err == nil {
						deleted.Add(20)
					}
				}
			}
		}(g)
	}
	var rwg sync.WaitGroup
	rwg.Add(1)
	go func() {
		defer rwg.Done()
		for {
			select {
			case <-done:
				return
			default:
				_ = tr.Pages()
			}
		}
	}()
	wg.Wait()
	close(done)
	rwg.Wait()

	pages := tr.Pages()
	total := 0
	prev := ""
	for _, p := range pages {
		if p.Occupancy > 100 {
			t.Fatalf("页 %d 占用 %d 超过 C", p.ID, p.Occupancy)
		}
		if len(p.Keys) == 0 && len(pages) > 1 {
			t.Fatalf("页 %d 为空但树中不止一页", p.ID)
		}
		for _, k := range p.Keys {
			if prev != "" && k <= prev {
				t.Fatalf("键未严格递增: %q 后 %q", prev, k)
			}
			prev = k
		}
		total += p.Occupancy
	}
	if want := inserted.Load() - deleted.Load(); int64(total) != want {
		t.Fatalf("尺寸总量 %d != 成功插入-删除 %d", total, want)
	}
}

package ontology

import (
	"reflect"
	"testing"
)

func mustNew(t *testing.T, n, i, m, cp int) *Readahead {
	t.Helper()
	r, err := NewReadahead(n, i, m, cp)
	if err != nil {
		t.Fatalf("NewReadahead(%d,%d,%d,%d): %v", n, i, m, cp, err)
	}
	return r
}

func eqSlices(a, b []int) bool {
	if len(a) == 0 && len(b) == 0 {
		return true
	}
	return reflect.DeepEqual(a, b)
}

// 首次 p=0 视为顺序访问（即便 prev=-1）。
func TestRuleFirstPageZeroIsSequential(t *testing.T) {
	r := mustNew(t, 100, 2, 8, 10)
	d, a, e, err := r.Read(0, 1)
	if err != nil || !eqSlices(d, []int{0}) || !eqSlices(a, []int{1, 2}) || len(e) != 0 {
		t.Fatalf("判定：seq(p==prev+1, prev=-1) 应成立；got d=%v a=%v e=%v err=%v", d, a, e, err)
	}

	// 首次从非 0 开始不是顺序访问：不预读。
	r = mustNew(t, 100, 2, 8, 10)
	d, a, _, err = r.Read(3, 1)
	if err != nil || !eqSlices(d, []int{3}) || len(a) != 0 {
		t.Fatalf("判定：首次 p=3 非顺序，不应预读；got d=%v a=%v", d, a)
	}
}

// I=4、M=16，逐页顺序读窗口按 4、8、16 倍增并封顶。
func TestRuleWindowDoublesAndCaps(t *testing.T) {
	r := mustNew(t, 10000, 4, 16, 10000)
	steps := []struct {
		p, expWsz, expMk int
	}{
		{0, 4, 1 + 4/2},    // s=4, ws=1, mk=3
		{3, 8, 5 + 8/2},    // s=8, ws=5, mk=9
		{9, 16, 13 + 16/2}, // s=16, ws=13, mk=21
		{21, 16, 29 + 8},   // s=min(16,32)=16, ws=29, mk=37
	}
	for _, st := range steps {
		if _, _, _, err := r.Read(st.p, 1); err != nil {
			t.Fatal(err)
		}
		got := r.State()
		if got.Window.Wsz != st.expWsz || got.Mk != st.expMk {
			t.Fatalf("Read(%d) 后窗口=%+v, 期望 wsz=%d mk=%d", st.p, got, st.expWsz, st.expMk)
		}
	}
}

// 标记页恰好 ws+floor(s/2)：读 mk 触发异步预读，读前一页不触发。
func TestRuleMarkPageTriggersAsync(t *testing.T) {
	r := mustNew(t, 10000, 4, 16, 10000)
	r.Read(0, 1) // ws=1,s=4,mk=3
	if st := r.State(); st.Mk != 3 {
		t.Fatalf("mk 应为 ws+floor(s/2)=1+2=3, got %d", st.Mk)
	}
	// 逐页读到 mk 前一页（2），均不触发异步预读。
	for _, p := range []int{1, 2} {
		_, a, _, _ := r.Read(p, 1)
		if len(a) != 0 {
			t.Fatalf("Read(%d) 在 mk=3 之前不应触发异步预读, got ahead=%v", p, a)
		}
	}
	if st := r.State(); st.Window.Wsz != 4 {
		t.Fatalf("未触发时窗口不变, got %+v", st)
	}
	// 读 mk=3 本身触发异步预读（页4此前已被同步预读）。
	_, a, _, _ := r.Read(3, 1)
	if !eqSlices(a, []int{5, 6, 7, 8, 9, 10, 11, 12}) {
		t.Fatalf("读 mk 应异步预读 8 页, got %v", a)
	}
	st := r.State()
	if st.Window != (Window{5, 8}) || st.Mk != 9 {
		t.Fatalf("异步窗口应为 (5,8) mk=9, got %+v", st)
	}
}

// 一次请求跨过标记页（n>1）同样触发异步预读。
func TestRuleRequestSpansMark(t *testing.T) {
	r := mustNew(t, 10000, 4, 16, 10000)
	r.Read(0, 4) // demand 0..3, s=4 ws=4 mk=6, ahead 4..7
	// 请求 [5,7) 跨过 mk=6，且全部命中 -> 异步预读。
	d, a, _, _ := r.Read(5, 2)
	if len(d) != 0 || !eqSlices(a, []int{8, 9, 10, 11, 12, 13, 14, 15}) {
		t.Fatalf("跨标记页全命中应触发异步预读, got d=%v a=%v", d, a)
	}
}

// n 大于 I 时同步窗口 s 取 n。
func TestRuleNGreaterThanI(t *testing.T) {
	r := mustNew(t, 10000, 2, 32, 10000)
	_, a, _, _ := r.Read(0, 6)
	if !eqSlices(a, []int{6, 7, 8, 9, 10, 11}) {
		t.Fatalf("s 应取 n=6, got ahead=%v", a)
	}
	if st := r.State(); st.Window != (Window{6, 6}) || st.Mk != 9 {
		t.Fatalf("窗口应为 (6,6) mk=9, got %+v", st)
	}
}

// 同步预读起点不小于 N：窗口清空且不预读。
func TestRuleSyncStartAtN(t *testing.T) {
	r := mustNew(t, 10, 2, 8, 20)
	r.Read(0, 4) // demand 0..3, rs=4, ahead 4..9, ws=4 wsz=2 mk=5
	// DropCache 后顺序读 [4,10)：missing 非空、顺序，rs=10>=N，不预读，窗口清空。
	r.DropCache()
	d, a, e, _ := r.Read(4, 6)
	if !eqSlices(d, []int{4, 5, 6, 7, 8, 9}) || len(a) != 0 || len(e) != 0 {
		t.Fatalf("文件尾不应预读, got d=%v a=%v e=%v", d, a, e)
	}
	if st := r.State(); st.Window.Wsz != 0 || st.HasMk {
		t.Fatalf("窗口应清空, got %+v", st)
	}

	// 异步预读起点不小于 N 时同样窗口清空：
	// I=2，N=10。Read(0,6)：s=max(2,0,6)=6 rs=6，预读 6..9，窗口 (6,6) mk=9。
	r2 := mustNew(t, 10, 2, 8, 20)
	r2.Read(0, 6)
	// 全命中读 mk=9：异步 rs=ws+wsz=12>=N，窗口清空，不预读。
	d, a, e, _ = r2.Read(9, 1)
	if len(d) != 0 || len(a) != 0 || len(e) != 0 {
		t.Fatalf("异步越界不应预读, got d=%v a=%v e=%v", d, a, e)
	}
	if st := r2.State(); st.Window.Wsz != 0 || st.HasMk {
		t.Fatalf("异步越界窗口应清空, got %+v", st)
	}
}

// 随机访问清空窗口，其后顺序未命中重新从 max(I,n) 起算。
func TestRuleRandomAccessResetsWindow(t *testing.T) {
	r := mustNew(t, 10000, 4, 16, 10000)
	r.Read(0, 1)  // 窗口 (1,4)
	r.Read(50, 1) // 随机未命中：不预读，窗口清空
	if st := r.State(); st.Window.Wsz != 0 || st.HasMk {
		t.Fatalf("随机访问应清空窗口, got %+v", st)
	}
	// 紧接顺序读 51（prev=50），旧 wsz=0，s=max(4,0,n)=4。
	_, a, _, _ := r.Read(51, 1)
	if !eqSlices(a, []int{52, 53, 54, 55}) {
		t.Fatalf("重置后应从 I=4 起算, got %v", a)
	}

	// n 较大时从 n 起算。
	r = mustNew(t, 10000, 4, 16, 10000)
	r.Read(80, 3)              // 随机（首次非0），窗口清空
	_, a, _, _ = r.Read(83, 7) // 顺序，s=max(4,2*0,7)=7
	if !eqSlices(a, []int{90, 91, 92, 93, 94, 95, 96}) {
		t.Fatalf("重置后 n=7 应使 s=7, got %v", a)
	}
}

// 窗口被 N 截断时 wsz 仍记录未截断值。
func TestRuleTruncatedWindowKeepsSize(t *testing.T) {
	r := mustNew(t, 10, 4, 16, 50)
	_, a, _, _ := r.Read(0, 1) // rs=1,s=4, ahead 1..4（截断但 wsz=4）
	if !eqSlices(a, []int{1, 2, 3, 4}) {
		t.Fatalf("预读只到 N, got %v", a)
	}
	if st := r.State(); st.Window != (Window{1, 4}) || st.Mk != 3 {
		t.Fatalf("截断不改变 s, got %+v", st)
	}

	// 尾部截断到只剩 2 页，wsz 仍为 8，mk=12 可越过 N。
	r2 := mustNew(t, 10, 4, 16, 50)
	r2.Read(0, 4) // rs=4 s=4 mk=6
	r2.Read(4, 4) // 顺序未命中：s=8 rs=8，预读仅 {8,9}，wsz=8
	if st := r2.State(); st.Window != (Window{8, 8}) || st.Mk != 12 {
		t.Fatalf("尾部截断 wsz 仍为 8, got %+v", st)
	}
}

// DropCache 后标记页未缓存导致走未命中分支。
func TestRuleDropCacheMarkMisses(t *testing.T) {
	r := mustNew(t, 10000, 4, 16, 50)
	r.Read(0, 1) // mk=3, 缓存 0..4
	r.DropCache()
	st := r.State()
	if st.Window.Wsz != 4 || st.Mk != 3 || !st.HasMk || st.Prev != 0 {
		t.Fatalf("DropCache 保留 prev/窗口/mk, got %+v", st)
	}
	// 读 mk=3：缓存为空 -> 未命中，且相对 prev=0 不顺序，窗口清空、不预读。
	d, a, _, _ := r.Read(3, 1)
	if !eqSlices(d, []int{3}) || len(a) != 0 {
		t.Fatalf("DropCache 后应走未命中分支, got d=%v a=%v", d, a)
	}
	if st := r.State(); st.Window.Wsz != 0 || st.HasMk {
		t.Fatalf("非顺序未命中应清空窗口, got %+v", st)
	}
}

package fib

import (
	"errors"
	"reflect"
	"testing"
)

func pfx(a, b, c, d, l int) Prefix {
	return Prefix{Addr: uint32(a)<<24 | uint32(b)<<16 | uint32(c)<<8 | uint32(d), Len: l}
}

func addr(a, b, c, d int) uint32 {
	return uint32(a)<<24 | uint32(b)<<16 | uint32(c)<<8 | uint32(d)
}

func nhResult(s string) Result { return Result{Kind: NexthopRoute, Nexthop: s} }

var bhResult = Result{Kind: BlackholeRoute}
var nrResult = Result{Kind: NoRoute}

func expectQuery(t *testing.T, m *Manager, a uint32, wantCtl, wantData Result) {
	t.Helper()
	ctl, data := m.Query(a)
	if ctl != wantCtl || data != wantData {
		t.Fatalf("Query(%d.%d.%d.%d) = (%v, %v), 期望 (%v, %v)",
			byte(a>>24), byte(a>>16), byte(a>>8), byte(a), ctl, data, wantCtl, wantData)
	}
}

func expectCount(t *testing.T, m *Manager, want int) {
	t.Helper()
	if got := m.Count(); got != want {
		t.Fatalf("Count() = %d, 期望 %d", got, want)
	}
}

func expectList(t *testing.T, m *Manager, want []Entry) {
	t.Helper()
	got := m.List()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("List() = %v, 期望 %v", got, want)
	}
}

// 参数非法:前缀低位非零、长度越界、下一跳为空、黑洞带值。
func TestInvalidArguments(t *testing.T) {
	m := NewManager(8)
	for _, p := range []Prefix{
		{Addr: addr(10, 0, 0, 1), Len: 8},  // 低位非零
		{Addr: addr(10, 0, 0, 0), Len: 33}, // 长度越界
		{Addr: addr(10, 0, 0, 0), Len: -1}, // 长度为负
		{Addr: addr(1, 0, 0, 0), Len: 0},   // 零长度但地址非零
	} {
		if err := m.Put(p, NH("a")); !errors.Is(err, ErrInvalidArgument) {
			t.Fatalf("Put(%v) = %v, 期望参数非法", p, err)
		}
		if err := m.Delete(p); !errors.Is(err, ErrInvalidArgument) {
			t.Fatalf("Delete(%v) = %v, 期望参数非法", p, err)
		}
	}
	if err := m.Put(pfx(10, 0, 0, 0, 8), NH("")); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("空下一跳应报参数非法, 得到 %v", err)
	}
	badBH := Nexthop{Blackhole: true, Value: "x"}
	if err := m.Put(pfx(10, 0, 0, 0, 8), badBH); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("黑洞带值应报参数非法, 得到 %v", err)
	}
	if err := m.SetCapacity(-1); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("负容量应报参数非法, 得到 %v", err)
	}
	// 非法参数不得改变状态。
	expectCount(t, m, 0)
	if got := m.List(); len(got) != 0 {
		t.Fatalf("非法操作后 List 应为空, 得到 %v", got)
	}
}

// 默认路由的有无:有默认路由时全空间有路由;无默认路由时未覆盖
// 地址为无路由(隐含无路由,不占条目数)。
func TestDefaultRoutePresence(t *testing.T) {
	m := NewManager(16)
	if err := m.Put(pfx(10, 0, 0, 0, 8), NH("a")); err != nil {
		t.Fatal(err)
	}
	expectCount(t, m, 1)
	expectQuery(t, m, addr(1, 2, 3, 4), nrResult, nrResult)
	expectQuery(t, m, addr(10, 1, 2, 3), nhResult("a"), nhResult("a"))
	// 写入默认路由后全空间有路由。
	if err := m.Put(pfx(0, 0, 0, 0, 0), NH("b")); err != nil {
		t.Fatal(err)
	}
	expectCount(t, m, 2)
	expectQuery(t, m, addr(1, 2, 3, 4), nhResult("b"), nhResult("b"))
	expectQuery(t, m, addr(10, 1, 2, 3), nhResult("a"), nhResult("a"))
	// 撤销默认路由恢复无路由。
	if err := m.Delete(pfx(0, 0, 0, 0, 0)); err != nil {
		t.Fatal(err)
	}
	expectCount(t, m, 1)
	expectQuery(t, m, addr(1, 2, 3, 4), nrResult, nrResult)
	if err := m.Delete(pfx(10, 0, 0, 0, 8)); err != nil {
		t.Fatal(err)
	}
	expectCount(t, m, 0)
	expectQuery(t, m, addr(10, 1, 2, 3), nrResult, nrResult)
}

// 黑洞夹在两段相同下一跳之间:黑洞是有效下一跳,不能合并掉,
// 且与无路由可区分。
func TestBlackholeSandwich(t *testing.T) {
	m := NewManager(16)
	for _, op := range []Op{
		PutOp(pfx(10, 0, 0, 0, 16), NH("a")),
		PutOp(pfx(10, 1, 0, 0, 16), Blackhole),
		PutOp(pfx(10, 2, 0, 0, 16), NH("a")),
	} {
		if err := m.Put(op.Prefix, op.Nexthop); err != nil {
			t.Fatal(err)
		}
	}
	// 两段 a 无法越过黑洞合并:需要 3 个条目。
	expectCount(t, m, 3)
	expectQuery(t, m, addr(10, 0, 1, 1), nhResult("a"), nhResult("a"))
	expectQuery(t, m, addr(10, 1, 1, 1), bhResult, bhResult)
	expectQuery(t, m, addr(10, 2, 1, 1), nhResult("a"), nhResult("a"))
	expectQuery(t, m, addr(10, 3, 1, 1), nrResult, nrResult)
	// 黑洞与无路由可区分。
	ctl, _ := m.Query(addr(10, 1, 1, 1))
	if ctl.Kind == NoRoute {
		t.Fatal("黑洞被误判为无路由")
	}
}

// 下一跳相同的相邻兄弟前缀合并,以及改下一跳后的再拆分。
func TestSiblingMergeAndSplit(t *testing.T) {
	m := NewManager(16)
	if err := m.Put(pfx(10, 0, 0, 0, 9), NH("a")); err != nil {
		t.Fatal(err)
	}
	if err := m.Put(pfx(10, 128, 0, 0, 9), NH("a")); err != nil {
		t.Fatal(err)
	}
	// 兄弟合并为一个 /8 条目。
	expectCount(t, m, 1)
	expectList(t, m, []Entry{{Prefix: pfx(10, 0, 0, 0, 8), Result: nhResult("a")}})
	// 改掉一半:拆分为两个条目。
	if err := m.Put(pfx(10, 128, 0, 0, 9), NH("b")); err != nil {
		t.Fatal(err)
	}
	expectCount(t, m, 2)
	expectQuery(t, m, addr(10, 0, 0, 1), nhResult("a"), nhResult("a"))
	expectQuery(t, m, addr(10, 128, 0, 1), nhResult("b"), nhResult("b"))
	// 改回来:重新合并。
	if err := m.Put(pfx(10, 128, 0, 0, 9), NH("a")); err != nil {
		t.Fatal(err)
	}
	expectCount(t, m, 1)
}

// 撤销导致的聚合退化:删除一条"粘合"路由后,剩余路由无法继续
// 合并,条目数上升。
func TestDeleteDeaggregation(t *testing.T) {
	m := NewManager(16)
	// 10/8、11/8 与 10.0/9 组合后可合并为 10/7 一个条目。
	for _, p := range []Prefix{pfx(10, 0, 0, 0, 8), pfx(11, 0, 0, 0, 8), pfx(10, 0, 0, 0, 9)} {
		if err := m.Put(p, NH("a")); err != nil {
			t.Fatal(err)
		}
	}
	expectCount(t, m, 1)
	// 撤销 10/8 后,10.0/9 与 11/8 不再相邻,退化为两个条目。
	if err := m.Delete(pfx(10, 0, 0, 0, 8)); err != nil {
		t.Fatal(err)
	}
	expectCount(t, m, 2)
	expectQuery(t, m, addr(10, 0, 0, 1), nhResult("a"), nhResult("a"))
	expectQuery(t, m, addr(10, 128, 0, 1), nrResult, nrResult)
	expectQuery(t, m, addr(11, 0, 0, 1), nhResult("a"), nhResult("a"))
}

// 无路由条目:当"一条默认条目 + 一条无路由条目"比逐段枚举更省
// 时,数据面应出现无路由条目(占条目数)。
func TestNoRouteEntry(t *testing.T) {
	m := NewManager(16)
	for _, p := range []Prefix{pfx(0, 0, 0, 0, 1), pfx(128, 0, 0, 0, 2), pfx(192, 0, 0, 0, 3)} {
		if err := m.Put(p, NH("a")); err != nil {
			t.Fatal(err)
		}
	}
	expectCount(t, m, 2)
	expectList(t, m, []Entry{
		{Prefix: pfx(0, 0, 0, 0, 0), Result: nhResult("a")},
		{Prefix: pfx(224, 0, 0, 0, 3), Result: nrResult},
	})
	expectQuery(t, m, addr(200, 0, 0, 1), nhResult("a"), nhResult("a"))
	expectQuery(t, m, addr(224, 0, 0, 1), nrResult, nrResult)
	expectQuery(t, m, addr(255, 255, 255, 255), nrResult, nrResult)
}

// 容量恰等于最少条目数时成功,差一时拒绝且状态不变。
func TestCapacityExactAndOneShort(t *testing.T) {
	m := NewManager(16)
	if err := m.Put(pfx(10, 0, 0, 0, 8), NH("a")); err != nil {
		t.Fatal(err)
	}
	if err := m.Put(pfx(12, 0, 0, 0, 8), NH("a")); err != nil {
		t.Fatal(err)
	}
	expectCount(t, m, 2)
	// 容量下调到恰等于最少条目数:允许。
	if err := m.SetCapacity(2); err != nil {
		t.Fatalf("容量等于最少条目数应允许, 得到 %v", err)
	}
	// 再写入一条使最少条目数变为 3,超出容量:拒绝。
	if err := m.Put(pfx(14, 0, 0, 0, 8), NH("a")); !errors.Is(err, ErrCapacityExceeded) {
		t.Fatalf("超容量应报容量不足, 得到 %v", err)
	}
	expectCount(t, m, 2)
	expectQuery(t, m, addr(14, 0, 0, 1), nrResult, nrResult)
	// 容量差一:新上限小于当前最少条目数,拒绝且容量不变。
	if err := m.SetCapacity(1); !errors.Is(err, ErrCapacityExceeded) {
		t.Fatalf("容量差一应报容量不足, 得到 %v", err)
	}
	if got := m.Capacity(); got != 2 {
		t.Fatalf("拒绝后容量应保持 2, 得到 %d", got)
	}
	// 放宽容量后写入成功。
	if err := m.SetCapacity(3); err != nil {
		t.Fatal(err)
	}
	if err := m.Put(pfx(14, 0, 0, 0, 8), NH("a")); err != nil {
		t.Fatal(err)
	}
	expectCount(t, m, 3)
}

// 批量内同一前缀先添加后撤销的净效果为零;同批重复操作按顺序生效。
func TestBatchNetEffect(t *testing.T) {
	m := NewManager(16)
	if err := m.Put(pfx(10, 0, 0, 0, 8), NH("a")); err != nil {
		t.Fatal(err)
	}
	// 先添加后撤销:净效果为零。
	if err := m.Batch(PutOp(pfx(11, 0, 0, 0, 8), NH("b")), DeleteOp(pfx(11, 0, 0, 0, 8))); err != nil {
		t.Fatal(err)
	}
	expectCount(t, m, 1)
	expectQuery(t, m, addr(11, 0, 0, 1), nrResult, nrResult)
	// 同批内对同一前缀重复操作:以最后一步为准。
	if err := m.Batch(
		PutOp(pfx(9, 0, 0, 0, 8), NH("x")),
		PutOp(pfx(9, 0, 0, 0, 8), NH("y")),
		DeleteOp(pfx(9, 0, 0, 0, 8)),
		PutOp(pfx(9, 0, 0, 0, 8), NH("z")),
	); err != nil {
		t.Fatal(err)
	}
	expectQuery(t, m, addr(9, 0, 0, 1), nhResult("z"), nhResult("z"))
	// 批内先撤销后添加也合法。
	if err := m.Batch(DeleteOp(pfx(9, 0, 0, 0, 8)), PutOp(pfx(9, 0, 0, 0, 8), NH("w"))); err != nil {
		t.Fatal(err)
	}
	expectQuery(t, m, addr(9, 0, 0, 1), nhResult("w"), nhResult("w"))
}

// 错误优先级:参数非法 > 撤销目标不存在 > 容量不足。
func TestBatchErrorPriority(t *testing.T) {
	m := NewManager(1)
	if err := m.Put(pfx(10, 0, 0, 0, 8), NH("a")); err != nil {
		t.Fatal(err)
	}
	// 批内第 0 步撤销不存在,第 1 步参数非法:参数非法优先。
	err := m.Batch(DeleteOp(pfx(11, 0, 0, 0, 8)), PutOp(Prefix{Addr: 1, Len: 8}, NH("x")))
	if !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("参数非法应优先于不存在, 得到 %v", err)
	}
	// 批内两个撤销目标都不存在:按生效顺序取第一个。
	err = m.Batch(DeleteOp(pfx(11, 0, 0, 0, 8)), DeleteOp(pfx(12, 0, 0, 0, 8)))
	if !errors.Is(err, ErrRouteNotFound) {
		t.Fatalf("应报撤销目标不存在, 得到 %v", err)
	}
	// 批内先写入(成功)再撤销不存在:整批回滚,写入不生效。
	err = m.Batch(PutOp(pfx(11, 0, 0, 0, 8), NH("b")), DeleteOp(pfx(12, 0, 0, 0, 8)))
	if !errors.Is(err, ErrRouteNotFound) {
		t.Fatalf("应报撤销目标不存在, 得到 %v", err)
	}
	expectQuery(t, m, addr(11, 0, 0, 1), nrResult, nrResult)
	// 批量最终超容量:整批不生效。
	err = m.Batch(PutOp(pfx(11, 0, 0, 0, 8), NH("b")), PutOp(pfx(12, 0, 0, 0, 8), NH("c")))
	if !errors.Is(err, ErrCapacityExceeded) {
		t.Fatalf("应报容量不足, 得到 %v", err)
	}
	expectCount(t, m, 1)
	expectQuery(t, m, addr(11, 0, 0, 1), nrResult, nrResult)
	expectQuery(t, m, addr(12, 0, 0, 1), nrResult, nrResult)
}

// 拒绝后状态不变:控制面、数据面与条目数都不得有任何变化。
func TestRejectionStateUnchanged(t *testing.T) {
	m := NewManager(1)
	// 构造撤销会导致条目数上升的形态:当前合并为 1 条。
	for _, p := range []Prefix{pfx(10, 0, 0, 0, 8), pfx(11, 0, 0, 0, 8), pfx(10, 0, 0, 0, 9)} {
		if err := m.Put(p, NH("a")); err != nil {
			t.Fatal(err)
		}
	}
	expectCount(t, m, 1)
	snapshot := func() (int, []Entry, Result, Result) {
		ctl1, data1 := m.Query(addr(10, 0, 0, 1))
		ctl2, data2 := m.Query(addr(11, 0, 0, 1))
		if ctl1 != data1 || ctl2 != data2 {
			t.Fatal("两个面结果不一致")
		}
		return m.Count(), m.List(), ctl1, ctl2
	}
	c0, l0, q10, q11 := snapshot()
	rejected := []error{
		m.Put(pfx(12, 0, 0, 0, 8), NH("b")),              // 写入超容量
		m.Delete(pfx(10, 0, 0, 0, 8)),                    // 撤销导致退化超容量
		m.Delete(pfx(99, 0, 0, 0, 8)),                    // 撤销不存在
		m.Batch(PutOp(pfx(12, 0, 0, 0, 8), NH("b"))),     // 批量超容量
		m.Batch(PutOp(Prefix{Addr: 1, Len: 8}, NH("x"))), // 批量参数非法
		m.SetCapacity(0),                                 // 容量差一
	}
	for i, err := range rejected {
		if err == nil {
			t.Fatalf("第 %d 个操作应被拒绝", i)
		}
		c, l, a, b := snapshot()
		if c != c0 || !reflect.DeepEqual(l, l0) || a != q10 || b != q11 {
			t.Fatalf("第 %d 个拒绝操作改变了状态", i)
		}
	}
}

// 写入与原值完全相同视为空操作:成功且不改任何状态。
func TestSameValueNoop(t *testing.T) {
	m := NewManager(1)
	if err := m.Put(pfx(10, 0, 0, 0, 8), NH("a")); err != nil {
		t.Fatal(err)
	}
	expectCount(t, m, 1)
	before := m.List()
	// 容量已满,但写入相同值是空操作,必须成功。
	if err := m.Put(pfx(10, 0, 0, 0, 8), NH("a")); err != nil {
		t.Fatalf("相同值写入应为成功的空操作, 得到 %v", err)
	}
	if got := m.List(); !reflect.DeepEqual(got, before) {
		t.Fatal("空操作改变了数据面")
	}
	// 容量为 0 时任何新增都被拒绝。
	m2 := NewManager(0)
	if err := m2.Put(pfx(10, 0, 0, 0, 8), NH("a")); !errors.Is(err, ErrCapacityExceeded) {
		t.Fatalf("容量 0 应拒绝新增, 得到 %v", err)
	}
}

// 被完全遮蔽的路由:控制面保留,数据面按最少条目聚合;
// 撤销遮蔽者后被遮蔽的路由重新生效。
func TestShadowedRoute(t *testing.T) {
	m := NewManager(16)
	if err := m.Put(pfx(10, 0, 0, 0, 8), NH("a")); err != nil {
		t.Fatal(err)
	}
	if err := m.Put(pfx(10, 0, 0, 0, 9), NH("b")); err != nil {
		t.Fatal(err)
	}
	if err := m.Put(pfx(10, 128, 0, 0, 9), NH("b")); err != nil {
		t.Fatal(err)
	}
	// 10/8->a 被两条 /9->b 完全遮蔽,数据面只需一个条目。
	expectCount(t, m, 1)
	expectList(t, m, []Entry{{Prefix: pfx(10, 0, 0, 0, 8), Result: nhResult("b")}})
	expectQuery(t, m, addr(10, 0, 0, 1), nhResult("b"), nhResult("b"))
	// 撤销一条 /9 后,10/8->a 重新显露。
	if err := m.Delete(pfx(10, 0, 0, 0, 9)); err != nil {
		t.Fatal(err)
	}
	expectCount(t, m, 2)
	expectQuery(t, m, addr(10, 0, 0, 1), nhResult("a"), nhResult("a"))
	expectQuery(t, m, addr(10, 128, 0, 1), nhResult("b"), nhResult("b"))
}

// 主机路由(/32)与黑洞默认路由。
func TestHostRouteAndBlackholeDefault(t *testing.T) {
	m := NewManager(16)
	if err := m.Put(Prefix{Addr: addr(10, 1, 2, 3), Len: 32}, NH("a")); err != nil {
		t.Fatal(err)
	}
	expectCount(t, m, 1)
	expectQuery(t, m, addr(10, 1, 2, 3), nhResult("a"), nhResult("a"))
	expectQuery(t, m, addr(10, 1, 2, 4), nrResult, nrResult)
	// 黑洞默认路由:全空间被丢弃,但不同于无路由。
	if err := m.Put(pfx(0, 0, 0, 0, 0), Blackhole); err != nil {
		t.Fatal(err)
	}
	expectCount(t, m, 2)
	expectQuery(t, m, addr(1, 2, 3, 4), bhResult, bhResult)
	expectQuery(t, m, addr(10, 1, 2, 3), nhResult("a"), nhResult("a"))
}

// List 的排序与条目数一致性。
func TestListOrderAndCount(t *testing.T) {
	m := NewManager(64)
	routes := []struct {
		p  Prefix
		nh Nexthop
	}{
		{pfx(10, 0, 0, 0, 8), NH("a")},
		{pfx(10, 1, 0, 0, 16), NH("b")},
		{pfx(192, 168, 0, 0, 16), Blackhole},
		{pfx(172, 16, 0, 0, 12), NH("c")},
		{pfx(10, 1, 2, 0, 24), NH("a")},
	}
	for _, r := range routes {
		if err := m.Put(r.p, r.nh); err != nil {
			t.Fatal(err)
		}
	}
	entries := m.List()
	if len(entries) != m.Count() {
		t.Fatalf("List 长度 %d 与 Count %d 不一致", len(entries), m.Count())
	}
	for i := 1; i < len(entries); i++ {
		a, b := entries[i-1].Prefix, entries[i].Prefix
		if a.Addr > b.Addr || (a.Addr == b.Addr && a.Len > b.Len) {
			t.Fatalf("List 未按(起始地址, 前缀长度)升序: %v 在 %v 前", a, b)
		}
	}
	// 数据面查询与列表查询一致。
	for _, a := range []uint32{addr(10, 1, 2, 3), addr(192, 168, 1, 1), addr(172, 20, 0, 1), addr(8, 8, 8, 8)} {
		_, data := m.Query(a)
		if got := entriesLookup(entries, a); got != data {
			t.Fatalf("地址 %d: Query 数据面 %v 与 List 查询 %v 不一致", a, data, got)
		}
	}
}

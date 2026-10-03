package hekaton

import (
	"errors"
	"reflect"
	"sync"
	"testing"
)

func mustNew(t *testing.T, k int) *Engine {
	t.Helper()
	e, err := New(k)
	if err != nil {
		t.Fatalf("New(%d): %v", k, err)
	}
	return e
}

func mustRead(t *testing.T, e *Engine, tr, key int) int {
	t.Helper()
	v, err := e.Read(tr, key)
	if err != nil {
		t.Fatalf("Read(t%d,k%d): %v", tr, key, err)
	}
	return v
}

func mustPrecommit(t *testing.T, e *Engine, tr int) int {
	t.Helper()
	et, err := e.Precommit(tr)
	if err != nil {
		t.Fatalf("Precommit(t%d): %v", tr, err)
	}
	return et
}

func mustFinish(t *testing.T, e *Engine, tr int) []int {
	t.Helper()
	o, err := e.Finish(tr)
	if err != nil {
		t.Fatalf("Finish(t%d): %v", tr, err)
	}
	return o
}

func mustAbort(t *testing.T, e *Engine, tr int) []int {
	t.Helper()
	a, err := e.Abort(tr)
	if err != nil {
		t.Fatalf("Abort(t%d): %v", tr, err)
	}
	return a
}

func hasDep(e *Engine, who, whom int) bool {
	_, a := e.txns[who].deps[whom]
	_, b := e.txns[whom].dependents[who]
	return a && b
}

func TestNewRejectsBadK(t *testing.T) {
	for _, k := range []int{0, -1, 65, 1000} {
		if _, err := New(k); !errors.Is(err, ErrInvalidK) {
			t.Fatalf("New(%d) err=%v, want ErrInvalidK", k, err)
		}
	}
}

func TestRejectionOrderAndNoStateChange(t *testing.T) {
	e := mustNew(t, 1)
	t1 := e.Begin()

	// 1) 事务号不存在最先报。
	if _, err := e.Read(999, 0); !errors.Is(err, ErrUnknownTxn) {
		t.Fatalf("Read unknown: %v", err)
	}
	if _, err := e.Write(999, 0, 1); !errors.Is(err, ErrUnknownTxn) {
		t.Fatalf("Write unknown: %v", err)
	}
	if _, err := e.Precommit(999); !errors.Is(err, ErrUnknownTxn) {
		t.Fatalf("Precommit unknown: %v", err)
	}
	if _, err := e.Finish(999); !errors.Is(err, ErrUnknownTxn) {
		t.Fatalf("Finish unknown: %v", err)
	}
	if _, err := e.Abort(999); !errors.Is(err, ErrUnknownTxn) {
		t.Fatalf("Abort unknown: %v", err)
	}

	// 2) 状态不符优先于键越界。
	et := mustPrecommit(t, e, t1)
	if _, err := e.Read(t1, 9); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("Read prepared w/ bad key: %v", err)
	}
	if _, err := e.Write(t1, 9, 1); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("Write prepared w/ bad key: %v", err)
	}
	if _, err := e.Precommit(t1); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("double precommit: %v", err)
	}
	if _, err := e.Abort(t1); err != nil {
		t.Fatalf("Abort prepared: %v", err)
	}
	if _, err := e.Finish(t1); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("Finish aborted: %v", err)
	}
	if _, err := e.Abort(t1); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("Abort aborted: %v", err)
	}

	// 3) 活跃事务键越界最后报，且拒绝不改变状态。
	e2 := mustNew(t, 1)
	a := e2.Begin()
	beforeClock := e2.clock
	if _, err := e2.Read(a, 1); !errors.Is(err, ErrKeyOutOfRange) {
		t.Fatalf("Read bad key: %v", err)
	}
	if _, err := e2.Read(a, -1); !errors.Is(err, ErrKeyOutOfRange) {
		t.Fatalf("Read neg key: %v", err)
	}
	if _, err := e2.Write(a, 1, 1); !errors.Is(err, ErrKeyOutOfRange) {
		t.Fatalf("Write bad key: %v", err)
	}
	if e2.clock != beforeClock || e2.txns[a].state != Active {
		t.Fatalf("被拒绝调用改变了状态")
	}
	_ = et
}

func TestSpecExample(t *testing.T) {
	e := mustNew(t, 1)
	t1 := e.Begin() // clock=1 RT=1
	t2 := e.Begin() // clock=2 RT=2
	if t1 != 1 || t2 != 2 {
		t.Fatalf("ids=%d,%d", t1, t2)
	}
	if ab, err := e.Write(t1, 0, 5); err != nil || ab != nil {
		t.Fatalf("Write t1: ab=%v err=%v", ab, err)
	}
	if et := mustPrecommit(t, e, t1); et != 3 {
		t.Fatalf("ET=%d, want 3", et)
	}
	// T2 RT=2：新不可见（3>=2）；旧版本结束者 T1 预备，2<3，读到 0，无依赖。
	if v := mustRead(t, e, t2, 0); v != 0 {
		t.Fatalf("t2 read=%d, want 0", v)
	}
	if len(e.txns[t2].deps) != 0 {
		t.Fatalf("t2 不应有依赖: %v", e.txns[t2].deps)
	}
	t3 := e.Begin() // clock=4 RT=4
	if v := mustRead(t, e, t3, 0); v != 5 {
		t.Fatalf("t3 read=%d, want 5", v)
	}
	if !hasDep(e, t3, t1) {
		t.Fatalf("t3 应依赖 t1")
	}
	if et := mustPrecommit(t, e, t3); et != 5 {
		t.Fatalf("ET=%d, want 5", et)
	}
	if o := mustFinish(t, e, t3); o != nil {
		t.Fatalf("t3 应等待, got %v", o)
	}
	order := mustFinish(t, e, t1)
	if !reflect.DeepEqual(order, []int{1, 3}) {
		t.Fatalf("order=%v, want [1 3]", order)
	}
	if e.txns[t1].state != Committed || e.txns[t3].state != Committed {
		t.Fatalf("state t1=%d t3=%d", e.txns[t1].state, e.txns[t3].state)
	}
}

func TestSpecExampleAbortCascade(t *testing.T) {
	e := mustNew(t, 1)
	t1 := e.Begin()
	t2 := e.Begin()
	_, _ = e.Write(t1, 0, 5)
	_, _ = e.Precommit(t1)
	_ = mustRead(t, e, t2, 0)
	t3 := e.Begin()
	_ = mustRead(t, e, t3, 0)
	_, _ = e.Precommit(t3)
	_, _ = e.Finish(t3)
	ab := mustAbort(t, e, t1)
	if !reflect.DeepEqual(ab, []int{1, 3}) {
		t.Fatalf("abort=%v, want [1 3]", ab)
	}
	if len(e.txns[t1].dependents) != 0 || len(e.txns[t3].deps) != 0 {
		t.Fatalf("依赖集合未清空")
	}
	// t2 不受影响。
	if e.txns[t2].state != Active {
		t.Fatalf("t2 state=%d", e.txns[t2].state)
	}
}

func TestActiveCreatorInvisibleAndOwnWrite(t *testing.T) {
	e := mustNew(t, 2)
	t1 := e.Begin()
	t2 := e.Begin()
	_, _ = e.Write(t1, 1, 9) // T1 活跃
	if v := mustRead(t, e, t2, 1); v != 0 {
		t.Fatalf("活跃创建者不可见, got %d", v)
	}
	if len(e.txns[t2].deps) != 0 {
		t.Fatalf("不可见不得记依赖")
	}
	if v := mustRead(t, e, t1, 1); v != 9 {
		t.Fatalf("自读自写=%d, want 9", v)
	}
	// 再次写自己的版本：原地改值，不新增版本、不设结束者。
	_, _ = e.Write(t1, 1, 10)
	if len(e.keys[1]) != 2 {
		t.Fatalf("版本链长度=%d, want 2", len(e.keys[1]))
	}
	if v := mustRead(t, e, t1, 1); v != 10 {
		t.Fatalf("原地改值=%d, want 10", v)
	}
}

func TestWriteConflictPreparedEtafterRT(t *testing.T) {
	// 范例：T1 预备 ET=3，T2 RT=2 写 → 写冲突中止 [2]。
	e := mustNew(t, 1)
	t1 := e.Begin()
	t2 := e.Begin()
	_, _ = e.Write(t1, 0, 5)
	_, _ = e.Precommit(t1)
	ab, err := e.Write(t2, 0, 7)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(ab, []int{2}) {
		t.Fatalf("冲突中止=%v, want [2]", ab)
	}
	// 中止不改变版本链：未追加新版本，旧版本结束者仍为 0。
	// t1 写入后链为 [v0(ender=t1), v1(t1)]；冲突中止 t2 不新增版本。
	if len(e.keys[0]) != 2 || e.keys[0][0].ender != t1 {
		t.Fatalf("冲突写改变了版本链")
	}
	if e.keys[0][1].ender != 0 {
		t.Fatalf("t1 的版本不应被 t2 结束")
	}
}

func TestWriteDependsOnPreparedCreator(t *testing.T) {
	// 对预备创建者的版本写入：成功并对创建者产生依赖。
	e := mustNew(t, 1)
	t1 := e.Begin()
	_, _ = e.Write(t1, 0, 1)
	_, _ = e.Precommit(t1) // ET=2
	t2 := e.Begin()        // RT=3, ET=2<3
	if ab, _ := e.Write(t2, 0, 2); ab != nil {
		t.Fatalf("写应成功, ab=%v", ab)
	}
	if !hasDep(e, t2, t1) {
		t.Fatalf("t2 写后应依赖 t1")
	}
	// t1 写入后：v0.ender=t1；t2 写入结束的是 t1 的版本 v1。
	if e.keys[0][0].ender != t1 || e.keys[0][1].ender != t2 {
		t.Fatalf("结束者记号错误: v0.ender=%d v1.ender=%d",
			e.keys[0][0].ender, e.keys[0][1].ender)
	}
}

func TestWriteVersionAlreadyEndedConflict(t *testing.T) {
	// “对已有结束者的版本写入”：正常 API 下最新非垃圾版本不可能带有效
	// 结束者，这里直接构造该内部状态，验证检查确实生效并中止。
	e := mustNew(t, 1)
	t1 := e.Begin()
	t2 := e.Begin()
	// 手工把初始版本的结束者指向活跃的 t1。
	e.keys[0][0].ender = t1
	ab, err := e.Write(t2, 0, 7)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(ab, []int{t2}) {
		t.Fatalf("应中止 t2, got %v", ab)
	}
}

func TestPreparedEnderOldVersionVisible(t *testing.T) {
	// 结束者预备且 RT < ET：旧版本仍可见，且不对结束者产生依赖。
	e := mustNew(t, 1)
	t1 := e.Begin()
	t2 := e.Begin()
	_, _ = e.Write(t1, 0, 1)
	_, _ = e.Precommit(t1) // ET=3（clock: begin1=1,begin2=2,precommit=3）
	// t2 RT=2：新版本不可见；旧版本结束者 t1 预备，2<3，可见读 0。
	if v := mustRead(t, e, t2, 0); v != 0 {
		t.Fatalf("read=%d, want 0", v)
	}
	if len(e.txns[t2].deps) != 0 {
		t.Fatalf("旧版本可见不产生依赖, got %v", e.txns[t2].deps)
	}
}

func TestActiveEnderOldVersionVisible(t *testing.T) {
	e := mustNew(t, 1)
	t1 := e.Begin()
	_, _ = e.Write(t1, 0, 1)
	_, _ = e.Precommit(t1) // ET=2
	t2 := e.Begin()        // RT=3
	_, _ = e.Write(t2, 0, 2)
	// t2 保持活跃：t1 版本的结束者 t2 活跃。
	t3 := e.Begin() // RT=4
	// t2 版本创建者活跃不可见；t1 版本起点 2<4 可见，终点结束者活跃可见
	// → 读到 1；所读版本创建者 t1 仍预备，故对 t1（而非结束者）产生依赖。
	if v := mustRead(t, e, t3, 0); v != 1 {
		t.Fatalf("read=%d, want 1", v)
	}
	if !hasDep(e, t3, t1) {
		t.Fatalf("应对预备创建者 t1 产生依赖")
	}
}

func TestGarbageVersionSkippedAndEnderInvalid(t *testing.T) {
	// 垃圾版本（创建者已中止）被跳过；它作为结束者的记号随之失效。
	e := mustNew(t, 1)
	t1 := e.Begin()
	_, _ = e.Write(t1, 0, 1)
	_, _ = e.Precommit(t1) // ET=2
	t2 := e.Begin()        // RT=3
	_, _ = e.Write(t2, 0, 2)
	// 此时初始版本的结束者 = t2（预备前的活跃）。中止 t2：
	// t2 的版本成为垃圾；初始版本的结束者记号失效（视为无结束者）。
	ab := mustAbort(t, e, t2)
	if !reflect.DeepEqual(ab, []int{t2}) {
		t.Fatalf("ab=%v", ab)
	}
	// 链：v0(ender=t1), v1(t1, ender=t2), v2(t2 垃圾)；
	// t2 中止后 v1 的结束者记号保留但按“无结束者”解释。
	if e.keys[0][1].ender != t2 {
		t.Fatalf("记号本身保留，仅按状态解释")
	}
	// 新事务写：取最新非垃圾版本=初始版本，其结束者 t2 已中止视为无，
	// 创建者为虚拟已提交 ET=0 < RT，成功。
	t3 := e.Begin()
	if a, _ := e.Write(t3, 0, 3); a != nil {
		t.Fatalf("应跳过垃圾并忽略失效结束者, ab=%v", a)
	}
	if v := mustRead(t, e, t3, 0); v != 3 {
		t.Fatalf("read=%d, want 3", v)
	}
	// 版本链追加在垃圾之后，扫描时跳过垃圾读到新值。
	if len(e.keys[0]) != 4 {
		t.Fatalf("链长=%d, want 4", len(e.keys[0]))
	}
}

func TestMultiLayerCommitOrder(t *testing.T) {
	e := mustNew(t, 1)
	t1 := e.Begin()
	_, _ = e.Write(t1, 0, 1)
	_, _ = e.Precommit(t1) // ET2
	t2 := e.Begin()        // RT3
	if v := mustRead(t, e, t2, 0); v != 1 {
		t.Fatalf("t2 read=%d", v)
	}
	_, _ = e.Write(t2, 0, 2)
	_, _ = e.Precommit(t2) // ET4
	t3 := e.Begin()        // RT5
	if v := mustRead(t, e, t3, 0); v != 2 {
		t.Fatalf("t3 read=%d", v)
	}
	_, _ = e.Precommit(t3) // ET6
	if o := mustFinish(t, e, t3); o != nil {
		t.Fatalf("t3 应等待, got %v", o)
	}
	if o := mustFinish(t, e, t2); o != nil {
		t.Fatalf("t2 应等待, got %v", o)
	}
	order := mustFinish(t, e, t1)
	if !reflect.DeepEqual(order, []int{1, 2, 3}) {
		t.Fatalf("order=%v, want [1 2 3]", order)
	}
	// 终结后依赖集合清空。
	for _, id := range []int{t1, t2, t3} {
		tr := e.txns[id]
		if len(tr.deps) != 0 || len(tr.dependents) != 0 {
			t.Fatalf("t%d 依赖未清空", id)
		}
	}
}

func TestCommitOrderMinIdFromReadySet(t *testing.T) {
	// 同一依赖解除后，ready 集合内反复取最小号。
	e := mustNew(t, 2)
	t1 := e.Begin()
	_, _ = e.Write(t1, 0, 1)
	_, _ = e.Write(t1, 1, 1)
	_, _ = e.Precommit(t1) // ET2
	t2 := e.Begin()        // RT3
	t3 := e.Begin()        // RT4
	_ = mustRead(t, e, t2, 0)
	_ = mustRead(t, e, t3, 1)
	_, _ = e.Precommit(t2) // ET5
	_, _ = e.Precommit(t3) // ET6
	_, _ = e.Finish(t3)
	_, _ = e.Finish(t2)
	order := mustFinish(t, e, t1)
	if !reflect.DeepEqual(order, []int{1, 2, 3}) {
		t.Fatalf("order=%v", order)
	}
}

func TestMultiLayerCascadeIncludingActive(t *testing.T) {
	e := mustNew(t, 1)
	t1 := e.Begin()
	_, _ = e.Write(t1, 0, 1)
	_, _ = e.Precommit(t1) // ET2
	t2 := e.Begin()        // RT3
	_ = mustRead(t, e, t2, 0)
	_, _ = e.Write(t2, 0, 2)
	_, _ = e.Precommit(t2) // ET4
	t3 := e.Begin()        // RT5，活跃态依赖者
	_ = mustRead(t, e, t3, 0)
	ab := mustAbort(t, e, t1)
	if !reflect.DeepEqual(ab, []int{1, 2, 3}) {
		t.Fatalf("cascade=%v, want [1 2 3]", ab)
	}
	for _, id := range []int{t1, t2, t3} {
		if e.txns[id].state != Aborted {
			t.Fatalf("t%d 未中止", id)
		}
	}
	// 垃圾被跳过，新事务读到初始值。
	t4 := e.Begin()
	if v := mustRead(t, e, t4, 0); v != 0 {
		t.Fatalf("垃圾未跳过, read=%d", v)
	}
}

func TestWriteConflictCascadesDependents(t *testing.T) {
	// 级联集合包含全部直接与间接依赖者（活跃态同样被级联）。
	e := mustNew(t, 1)
	t1 := e.Begin()
	_, _ = e.Write(t1, 0, 1)
	_, _ = e.Precommit(t1) // ET2
	t2 := e.Begin()        // RT3
	_, _ = e.Write(t2, 0, 2)
	_, _ = e.Precommit(t2) // ET4
	t3 := e.Begin()        // RT5，读 t2 并依赖
	_ = mustRead(t, e, t3, 0)
	t4 := e.Begin() // RT6：写最新版本（t2 创建，ET4<6）成功并依赖 t2
	if ab, _ := e.Write(t4, 0, 3); ab != nil {
		t.Fatalf("t4 write: %v", ab)
	}
	// 中止 t2：t3（读依赖，活跃）、t4（写依赖，活跃）全部级联。
	ab := mustAbort(t, e, t2)
	if !reflect.DeepEqual(ab, []int{2, 3, 4}) {
		t.Fatalf("cascade=%v, want [2 3 4]", ab)
	}
}

func TestNoSpeculativeResidue(t *testing.T) {
	// 无推测残留：提交后新事务读到的是最大 ET < RT 的已提交创建者的值。
	e := mustNew(t, 1)
	t1 := e.Begin()
	_, _ = e.Write(t1, 0, 1)
	_, _ = e.Precommit(t1) // ET2
	t2 := e.Begin()        // RT3，推测读 1
	if v := mustRead(t, e, t2, 0); v != 1 {
		t.Fatalf("read=%d", v)
	}
	// t1 中止 → t2 未对 t1 建依赖（读时 t1 预备会建依赖！）
	// 读已建依赖，故中止会级联 t2。改用不读的独立新事务验证残留：
	ab := mustAbort(t, e, t1)
	if !reflect.DeepEqual(ab, []int{1, 2}) {
		t.Fatalf("ab=%v", ab)
	}
	t3 := e.Begin()
	if v := mustRead(t, e, t3, 0); v != 0 {
		t.Fatalf("存在推测残留: %d", v)
	}
}

func TestInvariantSingleOpenVersionPerKey(t *testing.T) {
	// 与差分测试并行使用的轻量不变量检查在随机测试中做；
	// 这里验证提交后覆盖写不产生两个开放版本。
	e := mustNew(t, 1)
	t1 := e.Begin()
	_, _ = e.Write(t1, 0, 1)
	_, _ = e.Precommit(t1)
	_ = mustFinish(t, e, t1)
	t2 := e.Begin()
	_, _ = e.Write(t2, 0, 2)
	_, _ = e.Precommit(t2)
	_ = mustFinish(t, e, t2)
	open := 0
	for _, v := range e.keys[0] {
		if v.creator == 0 || e.txns[v.creator].state == Aborted {
			continue
		}
		if v.ender == 0 || e.txns[v.ender].state == Aborted {
			open++
		}
	}
	if open != 1 {
		t.Fatalf("开放版本数=%d, want 1", open)
	}
}

func TestConcurrentCalls(t *testing.T) {
	e := mustNew(t, 4)
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(seed int) {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				tr := e.Begin()
				key := (tr + seed) % 4
				_, _ = e.Read(tr, key)
				if i%2 == 0 {
					if ab, _ := e.Write(tr, key, tr*10+i); ab != nil {
						continue
					}
					if _, err := e.Precommit(tr); err == nil {
						_, _ = e.Finish(tr)
					}
				} else {
					_, _ = e.Abort(tr)
				}
			}
		}(g)
	}
	wg.Wait()
}

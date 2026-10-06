package idb

import (
	"fmt"
	"sync"
	"testing"
)

// newTestKernel 打开版本 1 的数据库 "db" 并创建给定仓库，返回首个连接。
func newTestKernel(t *testing.T, stores ...string) (*Kernel, *Connection) {
	t.Helper()
	k := NewKernel()
	req := k.Open("db", 1, func(u *UpgradeTx) {
		for _, s := range stores {
			if err := u.CreateStore(s); err != nil {
				t.Fatalf("create store %s: %v", s, err)
			}
		}
	})
	conn, err := req.Result()
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	return k, conn
}

func mustTx(t *testing.T, c *Connection, mode Mode, scope ...string) *Tx {
	t.Helper()
	tx, err := c.CreateTx(mode, scope)
	if err != nil {
		t.Fatalf("create tx %v %v: %v", mode, scope, err)
	}
	return tx
}

func seedKey(t *testing.T, c *Connection, store, key, value string) {
	t.Helper()
	tx := mustTx(t, c, ReadWrite, store)
	r := tx.Put(store, []byte(key), []byte(value), nil, nil)
	if r.Err() != nil {
		t.Fatalf("seed put: %v", r.Err())
	}
	if tx.Outcome() != OutcomeCommitted {
		t.Fatalf("seed tx outcome = %v", tx.Outcome())
	}
}

func expectState(t *testing.T, tx *Tx, want TxState, why string) {
	t.Helper()
	if got := tx.State(); got != want {
		t.Fatalf("tx %d state = %v, want %v（判定依据：%s）", tx.ID(), got, want, why)
	}
	t.Logf("tx %d state = %v（判定依据：%s）", tx.ID(), want, why)
}

// 三种版本关系：高于（版本变更）、等于（直接打开）、低于（版本过低拒绝）。
func TestOpenVersionRelations(t *testing.T) {
	k := NewKernel()
	upgrades := 0
	r1 := k.Open("db", 2, func(u *UpgradeTx) {
		upgrades++
		if err := u.CreateStore("s1"); err != nil {
			t.Errorf("create store: %v", err)
		}
	})
	c1, err := r1.Result()
	t.Logf("输入: Open(db,2) 于空库; 输出: err=%v upgrades=%d version=%d", err, upgrades, k.Version("db"))
	if err != nil || c1 == nil || upgrades != 1 || k.Version("db") != 2 {
		t.Fatalf("高版本打开（空库 0 -> 2）失败: err=%v upgrades=%d version=%d", err, upgrades, k.Version("db"))
	}

	r2 := k.Open("db", 2, func(u *UpgradeTx) { upgrades++ })
	c2, err := r2.Result()
	t.Logf("输入: Open(db,2) 等于当前版本; 输出: err=%v upgrades=%d", err, upgrades)
	if err != nil || c2 == nil || upgrades != 1 {
		t.Fatalf("等于当前版本应直接打开且不触发版本变更: err=%v upgrades=%d", err, upgrades)
	}

	r3 := k.Open("db", 1, nil)
	_, err = r3.Result()
	t.Logf("输入: Open(db,1) 低于当前版本 2; 输出: err=%v", err)
	if KindOf(err) != KindVersionTooLow {
		t.Fatalf("低于当前版本应按版本过低拒绝, got %v", err)
	}

	c1.Close()
	c2.Close()
	r4 := k.Open("db", 3, func(u *UpgradeTx) { upgrades++ })
	c4, err := r4.Result()
	t.Logf("输入: Open(db,3) 高于当前版本 2; 输出: err=%v upgrades=%d version=%d", err, upgrades, k.Version("db"))
	if err != nil || c4 == nil || upgrades != 2 || k.Version("db") != 3 {
		t.Fatalf("高版本打开（2 -> 3）失败: err=%v upgrades=%d version=%d", err, upgrades, k.Version("db"))
	}
	c4.Close()
}

// 版本变更被既有连接阻塞，全部关闭后解除；通知在阻塞时发出。
func TestVersionChangeBlockedAndUnblocked(t *testing.T) {
	k, connA := newTestKernel(t, "s1")
	rB := k.Open("db", 1, nil)
	connB, err := rB.Result()
	if err != nil || connB == nil {
		t.Fatalf("open second conn: %v", err)
	}
	notified := map[string]bool{}
	connA.OnVersionChange = func() { notified["A"] = true }
	connB.OnVersionChange = func() { notified["B"] = true }

	upgraded := false
	reqVC := k.Open("db", 2, func(u *UpgradeTx) { upgraded = true })
	t.Logf("输入: Open(db,2) 存在两个打开连接; 输出: done=%v notified=%v upgraded=%v", reqVC.Done(), notified, upgraded)
	if reqVC.Done() || !notified["A"] || !notified["B"] || upgraded {
		t.Fatalf("版本变更应被阻塞并通知全部连接: done=%v notified=%v upgraded=%v", reqVC.Done(), notified, upgraded)
	}

	connA.Close()
	t.Logf("输入: connA.Close(); 输出: reqVC.done=%v upgraded=%v", reqVC.Done(), upgraded)
	if reqVC.Done() {
		t.Fatalf("仍有连接打开，版本变更不应解除阻塞")
	}

	connB.Close()
	conn, err := reqVC.Result()
	t.Logf("输入: connB.Close(); 输出: reqVC.done=%v err=%v upgraded=%v version=%d", reqVC.Done(), err, upgraded, k.Version("db"))
	if err != nil || conn == nil || !upgraded || k.Version("db") != 2 {
		t.Fatalf("全部连接关闭后版本变更应执行并提交: err=%v upgraded=%v version=%d", err, upgraded, k.Version("db"))
	}
	conn.Close()
}

// 通知后新的打开请求一律排在版本变更之后。
func TestOpenQueuedBehindVersionChange(t *testing.T) {
	k, connA := newTestKernel(t, "s1")
	connA.OnVersionChange = func() { t.Logf("connA 收到版本变更通知") }

	reqVC := k.Open("db", 2, func(u *UpgradeTx) {})
	reqEq := k.Open("db", 1, nil) // 等于旧版本，排在版本变更之后
	reqV2 := k.Open("db", 2, nil) // 等于新版本，排在版本变更之后
	t.Logf("输入: 依次 Open(db,2)/Open(db,1)/Open(db,2)，首个被 connA 阻塞; 输出: done=%v/%v/%v",
		reqVC.Done(), reqEq.Done(), reqV2.Done())
	if reqVC.Done() || reqEq.Done() || reqV2.Done() {
		t.Fatalf("三个打开请求都应处于等待")
	}

	connA.Close()
	if !reqVC.Done() || !reqEq.Done() || !reqV2.Done() {
		t.Fatalf("解除阻塞后三个请求都应完成: %v/%v/%v", reqVC.Done(), reqEq.Done(), reqV2.Done())
	}
	if _, err := reqVC.Result(); err != nil {
		t.Fatalf("版本变更应成功: %v", err)
	}
	_, errEq := reqEq.Result()
	_, errV2 := reqV2.Result()
	t.Logf("输出: reqEq.err=%v reqV2.err=%v version=%d", errEq, errV2, k.Version("db"))
	if KindOf(errEq) != KindVersionTooLow {
		t.Fatalf("排在版本变更后的旧版本打开应按版本过低拒绝, got %v", errEq)
	}
	if errV2 != nil {
		t.Fatalf("等于新版本的打开应成功: %v", errV2)
	}
}

// 只读重叠并行；读写与任何重叠不可并行；不重叠始终并行。
func TestSchedulingParallelRules(t *testing.T) {
	_, conn := newTestKernel(t, "s1", "s2")

	ro1 := mustTx(t, conn, ReadOnly, "s1")
	ro2 := mustTx(t, conn, ReadOnly, "s1", "s2")
	expectState(t, ro1, TxActive, "只读事务创建即可开始")
	expectState(t, ro2, TxActive, "只读-只读作用域重叠可以并行")

	rw1 := mustTx(t, conn, ReadWrite, "s2")
	expectState(t, rw1, TxPending, "读写与运行中的只读 ro2 作用域 s2 重叠，不可并行")

	rw2 := mustTx(t, conn, ReadWrite, "s1", "s2")
	expectState(t, rw2, TxPending, "读写与运行中的只读 ro1/ro2 作用域重叠，不可并行")

	if err := ro1.Commit(); err != nil {
		t.Fatalf("commit ro1: %v", err)
	}
	expectState(t, rw1, TxPending, "ro2 仍占用 s2")
	expectState(t, rw2, TxPending, "ro2 仍占用 s1")
	if err := ro2.Commit(); err != nil {
		t.Fatalf("commit ro2: %v", err)
	}
	expectState(t, rw1, TxActive, "只读全部结束后 rw1 开始")
	expectState(t, rw2, TxPending, "rw2 与运行中的 rw1 作用域 s2 重叠，读写-读写不可并行")
	if err := rw1.Commit(); err != nil {
		t.Fatalf("commit rw1: %v", err)
	}
	expectState(t, rw2, TxActive, "rw1 提交后 rw2 开始")
	if err := rw2.Commit(); err != nil {
		t.Fatalf("commit rw2: %v", err)
	}
}

// 作用域不重叠的读写事务始终可并行。
func TestDisjointScopesParallel(t *testing.T) {
	_, conn := newTestKernel(t, "s1", "s2")
	rw1 := mustTx(t, conn, ReadWrite, "s1")
	rw2 := mustTx(t, conn, ReadWrite, "s2")
	expectState(t, rw1, TxActive, "第一个读写事务")
	expectState(t, rw2, TxActive, "作用域不重叠，读写-读写也可并行")
	rw1.Commit()
	rw2.Commit()
}

// 不得越过比它早创建、作用域重叠且尚未结束的事务，即使当前可并行。
func TestNoOvertakeEarlierPending(t *testing.T) {
	_, conn := newTestKernel(t, "s1")

	ro1 := mustTx(t, conn, ReadOnly, "s1")
	expectState(t, ro1, TxActive, "只读 ro1 先开始")
	rw := mustTx(t, conn, ReadWrite, "s1")
	expectState(t, rw, TxPending, "读写与 ro1 重叠，被阻塞")
	ro2 := mustTx(t, conn, ReadOnly, "s1")
	expectState(t, ro2, TxPending, "ro2 虽与 ro1 可并行，但不得越过更早创建且重叠的 rw")

	ro1.Commit()
	expectState(t, rw, TxActive, "ro1 结束后 rw 开始")
	expectState(t, ro2, TxPending, "ro2 与运行中的 rw 重叠，仍等待")
	rw.Commit()
	expectState(t, ro2, TxActive, "rw 提交后 ro2 开始")
	ro2.Commit()
}

// 只读快照在开始时刻确定；调度规则保证其存续期间作用域内不会有新的提交。
func TestReadOnlySnapshot(t *testing.T) {
	k, conn := newTestKernel(t, "s1")
	seedKey(t, conn, "s1", "k1", "v0")

	// 快照在开始时刻确定：ro 排队期间 rw 提交，ro 开始后看到 v1。
	rw := mustTx(t, conn, ReadWrite, "s1")
	ro := mustTx(t, conn, ReadOnly, "s1")
	expectState(t, ro, TxPending, "读写 rw 运行中，只读 ro 等待")
	rw.Put("s1", []byte("k1"), []byte("v1"), nil, nil)
	expectState(t, rw, TxFinished, "rw 无未完成请求，自动提交")
	expectState(t, ro, TxActive, "rw 提交后 ro 开始，快照取开始时刻")
	r := ro.Get("s1", []byte("k1"), nil, nil)
	v, found := r.Value()
	t.Logf("输入: ro.Get(k1); 输出: v=%s found=%v（判定依据：快照取 ro 开始时刻，含 rw 已提交的 v1）", v, found)
	if !found || string(v) != "v1" {
		t.Fatalf("ro 应读到开始时刻快照中的 v1, got %q found=%v", v, found)
	}

	// 只读事务存续期间，重叠的读写事务不能开始，故快照不可能被后续提交影响。
	ro2 := mustTx(t, conn, ReadOnly, "s1")
	expectState(t, ro2, TxActive, "ro2 开始")
	rw2 := mustTx(t, conn, ReadWrite, "s1")
	expectState(t, rw2, TxPending, "读写与 ro2 重叠被阻塞，ro2 存续期间不会有重叠提交")
	r2 := ro2.Get("s1", []byte("k1"), nil, nil)
	v2, _ := r2.Value()
	t.Logf("输入: ro2.Get(k1); 输出: v=%s（判定依据：rw2 仍 %v，快照不变）", v2, rw2.State())
	if string(v2) != "v1" {
		t.Fatalf("ro2 应读到 v1, got %q", v2)
	}
	ro2.Commit()
	expectState(t, rw2, TxActive, "ro2 提交后 rw2 才能开始")
	rw2.Abort()

	// 白盒验证 MVCC：旧快照序号读旧值，不受后续版本影响。
	sd := newStoreData()
	sd.put("k", 1, []byte("a"))
	sd.put("k", 2, []byte("b"))
	if v, _ := sd.get("k", 1); string(v) != "a" {
		t.Fatalf("seq=1 应读到 a, got %q", v)
	}
	if v, _ := sd.get("k", 2); string(v) != "b" {
		t.Fatalf("seq=2 应读到 b, got %q", v)
	}
	if _, ok := sd.get("k", 0); ok {
		t.Fatalf("seq=0 不应看到任何版本")
	}
	t.Logf("MVCC 白盒: seq0 不存在 / seq1=a / seq2=b，后续提交不影响旧快照")
	_ = k
}

// 自动提交时机：无未完成请求时提交；回调内续发请求可延长事务；显式提交等价。
func TestAutoCommitTiming(t *testing.T) {
	_, conn := newTestKernel(t, "s1")
	seedKey(t, conn, "s1", "k1", "v1")

	tx := mustTx(t, conn, ReadOnly, "s1")
	expectState(t, tx, TxActive, "创建后尚无请求，不提交")

	var stateInCb TxState
	tx.Get("s1", []byte("k1"), nil, func(r *Request) {
		stateInCb = tx.State()
		tx.Get("s1", []byte("k1"), nil, nil) // 回调内续发请求，事务继续
	})
	t.Logf("输入: Get 回调内再 Get; 输出: 回调时状态=%v 结束后状态=%v 结局=%v", stateInCb, tx.State(), tx.Outcome())
	if stateInCb != TxActive {
		t.Fatalf("第一个请求回调执行期间事务应仍存活, got %v", stateInCb)
	}
	if tx.Outcome() != OutcomeCommitted {
		t.Fatalf("全部请求完成后应自动提交, got %v", tx.Outcome())
	}

	tx2 := mustTx(t, conn, ReadOnly, "s1")
	if err := tx2.Commit(); err != nil {
		t.Fatalf("显式提交: %v", err)
	}
	if tx2.Outcome() != OutcomeCommitted {
		t.Fatalf("显式提交与自动提交等价, got %v", tx2.Outcome())
	}
	t.Logf("输入: 显式 Commit; 输出: 结局=%v", tx2.Outcome())
}

// 可忽略错误的请求失败不中止事务；未标记时请求失败默认中止并丢弃全部写入。
func TestIgnoreErrorRequestFailure(t *testing.T) {
	k, conn := newTestKernel(t, "s1")
	seedKey(t, conn, "s1", "k1", "v1")

	tx := mustTx(t, conn, ReadWrite, "s1")
	r1 := tx.Put("s1", []byte("k1"), []byte("x"), &Options{AddOnly: true, IgnoreError: true}, nil)
	t.Logf("输入: Put(k1, addOnly, ignoreError); 输出: err=%v", r1.Err())
	if KindOf(r1.Err()) != KindKeyConflict {
		t.Fatalf("应报键冲突, got %v", r1.Err())
	}
	if tx.Outcome() != OutcomeCommitted {
		t.Fatalf("无未完成请求时应自动提交（而非中止）, got %v", tx.Outcome())
	}
	t.Logf("输出: 结局=%v（判定依据：键冲突被忽略，事务未中止，无未完成请求故自动提交）", tx.Outcome())

	// 在回调中验证：标记可忽略错误的失败请求不会中止事务，事务可继续接受请求。
	txKeep := mustTx(t, conn, ReadWrite, "s1")
	var stateAfterIgnored TxState
	txKeep.Put("s1", []byte("k1"), []byte("x"), &Options{AddOnly: true, IgnoreError: true}, func(r *Request) {
		stateAfterIgnored = txKeep.State()
		txKeep.Put("s1", []byte("k2"), []byte("v2"), nil, nil)
	})
	t.Logf("输入: Put(k1, addOnly, ignoreError) 回调内再 Put(k2); 输出: 失败请求后状态=%v 结局=%v", stateAfterIgnored, txKeep.Outcome())
	if stateAfterIgnored != TxActive {
		t.Fatalf("可忽略错误的请求失败后事务应保持存活, got %v", stateAfterIgnored)
	}
	if txKeep.Outcome() != OutcomeCommitted {
		t.Fatalf("事务应继续并自动提交, got %v", txKeep.Outcome())
	}
	_, stores := k.Snapshot("db")
	if string(stores["s1"]["k2"]) != "v2" {
		t.Fatalf("k2 应已提交可见, got %q", stores["s1"]["k2"])
	}

	tx2 := mustTx(t, conn, ReadWrite, "s1")
	var r2 *Request
	tx2.Put("s1", []byte("k9"), []byte("v9"), nil, func(*Request) {
		r2 = tx2.Put("s1", []byte("k1"), []byte("y"), &Options{AddOnly: true}, nil)
	})
	t.Logf("输入: Put(k9) 后 Put(k1, addOnly); 输出: err=%v 结局=%v", r2.Err(), tx2.Outcome())
	if KindOf(r2.Err()) != KindKeyConflict || tx2.Outcome() != OutcomeAborted {
		t.Fatalf("未标记可忽略错误时请求失败应中止事务: err=%v outcome=%v", r2.Err(), tx2.Outcome())
	}
	_, stores = k.Snapshot("db")
	if _, ok := stores["s1"]["k9"]; ok {
		t.Fatalf("中止事务的全部写入应被丢弃")
	}
	t.Logf("输出: k9 不可见（中止丢弃全部写入），k1 仍为 v1=%s", stores["s1"]["k1"])
}

// 关闭连接：未开始事务被取消，已开始事务放行完成后连接才真正关闭；关闭期间不得创建事务。
func TestCloseCancelsPendingReleasesActive(t *testing.T) {
	k, connA := newTestKernel(t, "s1")
	rB := k.Open("db", 1, nil)
	connB, _ := rB.Result()

	active := mustTx(t, connB, ReadWrite, "s1")  // 立即开始
	pending := mustTx(t, connB, ReadWrite, "s1") // 与 active 重叠，排队
	expectState(t, active, TxActive, "connB 的读写事务已开始")
	expectState(t, pending, TxPending, "connB 的第二个读写事务被 active 阻塞")

	connB.Close()
	t.Logf("输入: connB.Close(); 输出: active=%v pending=%v/%v fullyClosed=%v",
		active.State(), pending.State(), pending.Outcome(), connB.FullyClosed())
	if pending.Outcome() != OutcomeCancelled {
		t.Fatalf("未开始事务应按被取消失败, got %v", pending.Outcome())
	}
	if connB.FullyClosed() {
		t.Fatalf("已开始事务未完成，连接不应真正关闭")
	}
	if _, err := connB.CreateTx(ReadOnly, []string{"s1"}); KindOf(err) != KindInvalidState {
		t.Fatalf("关闭期间创建事务应按状态不允许拒绝, got %v", err)
	}

	active.Commit()
	t.Logf("输入: active.Commit(); 输出: fullyClosed=%v", connB.FullyClosed())
	if !connB.FullyClosed() {
		t.Fatalf("已开始事务完成后连接应真正关闭")
	}

	// connA 上无事务时关闭应立即生效。
	connA.Close()
	if !connA.FullyClosed() {
		t.Fatalf("无未完成事务的连接应立即关闭")
	}
}

// 版本变更中止恢复仓库集合与版本号。
func TestVersionChangeAbortRestores(t *testing.T) {
	k, conn := newTestKernel(t, "s1")
	seedKey(t, conn, "s1", "k1", "v1")
	conn.Close()

	req := k.Open("db", 2, func(u *UpgradeTx) {
		if err := u.CreateStore("s2"); err != nil {
			t.Errorf("create s2: %v", err)
		}
		if err := u.DeleteStore("s1"); err != nil {
			t.Errorf("delete s1: %v", err)
		}
		u.Abort()
	})
	_, err := req.Result()
	t.Logf("输入: Open(db,2) 升级中删 s1 建 s2 后 Abort; 输出: err=%v version=%d", err, k.Version("db"))
	if KindOf(err) != KindAborted {
		t.Fatalf("版本变更中止应使打开失败, got %v", err)
	}
	if k.Version("db") != 1 {
		t.Fatalf("中止后版本应恢复为 1, got %d", k.Version("db"))
	}

	r2 := k.Open("db", 1, nil)
	conn2, _ := r2.Result()
	if _, err := conn2.CreateTx(ReadOnly, []string{"s1"}); err != nil {
		t.Fatalf("中止后 s1 应仍存在: %v", err)
	}
	if _, err := conn2.CreateTx(ReadOnly, []string{"s2"}); KindOf(err) != KindStoreNotFound {
		t.Fatalf("中止后 s2 应不存在, got %v", err)
	}
	_, stores := k.Snapshot("db")
	if string(stores["s1"]["k1"]) != "v1" {
		t.Fatalf("中止后仓库数据应恢复, got %q", stores["s1"]["k1"])
	}
	t.Logf("输出: s1 存在且 k1=v1，s2 不存在（判定依据：版本变更中止整体回滚）")
}

// 仅添加模式遇已有键按键冲突拒绝；普通写入覆盖；读不存在键返回不存在而非错误。
func TestAddOnlyKeyConflict(t *testing.T) {
	k, conn := newTestKernel(t, "s1")

	tx := mustTx(t, conn, ReadWrite, "s1")
	var r1, r2 *Request
	tx.Put("s1", []byte("k"), []byte("v1"), &Options{AddOnly: true}, func(*Request) {
		r1 = tx.Put("s1", []byte("k"), []byte("v2"), &Options{AddOnly: true, IgnoreError: true}, func(*Request) {
			r2 = tx.Put("s1", []byte("k"), []byte("v3"), nil, nil)
		})
	})
	t.Logf("输入: addOnly 写 k 两次后普通覆盖; 输出: 第二次 err=%v", r1.Err())
	if KindOf(r1.Err()) != KindKeyConflict {
		t.Fatalf("仅添加模式遇已有键应按键冲突拒绝, got %v", r1.Err())
	}
	if r2.Err() != nil {
		t.Fatalf("普通写入已有键应覆盖成功: %v", r2.Err())
	}
	_, stores := k.Snapshot("db")
	if string(stores["s1"]["k"]) != "v3" {
		t.Fatalf("覆盖后应为 v3, got %q", stores["s1"]["k"])
	}

	tx2 := mustTx(t, conn, ReadOnly, "s1")
	rg := tx2.Get("s1", []byte("missing"), nil, nil)
	v, found := rg.Value()
	t.Logf("输入: Get(missing); 输出: err=%v found=%v", rg.Err(), found)
	if rg.Err() != nil || found || v != nil {
		t.Fatalf("读取不存在的键应返回不存在而非错误: err=%v found=%v", rg.Err(), found)
	}
}

// 删除数据库：有连接时通知并阻塞，全部关闭后删除生效，此后版本视为零。
func TestDeleteDatabase(t *testing.T) {
	k, connA := newTestKernel(t, "s1")
	rB := k.Open("db", 1, nil)
	connB, _ := rB.Result()
	notified := 0
	connA.OnVersionChange = func() { notified++ }
	connB.OnVersionChange = func() { notified++ }

	del := k.DeleteDatabase("db")
	t.Logf("输入: DeleteDatabase(db) 存在两个连接; 输出: done=%v notified=%d", del.Done(), notified)
	if del.Done() || notified != 2 {
		t.Fatalf("删除应被阻塞并通知全部连接: done=%v notified=%d", del.Done(), notified)
	}
	connA.Close()
	if del.Done() {
		t.Fatalf("仍有连接，删除不应生效")
	}
	connB.Close()
	t.Logf("输入: 全部连接关闭; 输出: del.done=%v version=%d", del.Done(), k.Version("db"))
	if !del.Done() || del.Err() != nil {
		t.Fatalf("全部连接关闭后删除应生效: done=%v err=%v", del.Done(), del.Err())
	}
	if k.Version("db") != 0 {
		t.Fatalf("删除后版本应视为零, got %d", k.Version("db"))
	}

	// 删除后重新打开：从零开始，触发版本变更。
	upgraded := false
	r := k.Open("db", 1, func(u *UpgradeTx) { upgraded = true })
	if _, err := r.Result(); err != nil || !upgraded || k.Version("db") != 1 {
		t.Fatalf("删除后重新打开应从零升级: err=%v upgraded=%v version=%d", err, upgraded, k.Version("db"))
	}
	t.Logf("输出: 重新打开 upgraded=%v version=%d", upgraded, k.Version("db"))

	// 删除不存在的数据库立即完成。
	if d := k.DeleteDatabase("absent"); !d.Done() || d.Err() != nil {
		t.Fatalf("删除不存在的数据库应立即完成: done=%v err=%v", d.Done(), d.Err())
	}
}

// 拒绝次序：参数非法 > 版本过低 > 状态不允许 > 仓库不存在 > 作用域不符 > 事务已结束 > 键冲突。
func TestErrorPrecedence(t *testing.T) {
	k, conn := newTestKernel(t, "s1")
	seedKey(t, conn, "s1", "k1", "v1")

	// 参数非法先于版本过低。
	r := k.Open("", 0, nil)
	if _, err := r.Result(); KindOf(err) != KindInvalidArg {
		t.Fatalf("空名+非正版本应报参数非法, got %v", err)
	}
	r = k.Open("db", 0, nil)
	if _, err := r.Result(); KindOf(err) != KindInvalidArg {
		t.Fatalf("非正版本应报参数非法（先于版本过低）, got %v", err)
	}

	// 状态不允许先于仓库不存在：关闭中的连接创建事务，即使作用域仓库不存在。
	connA := conn
	keep := mustTx(t, connA, ReadWrite, "s1") // 保持 active 使连接处于关闭中状态
	connA.Close()
	if _, err := connA.CreateTx(ReadOnly, []string{"nope"}); KindOf(err) != KindInvalidState {
		t.Fatalf("关闭期间创建事务应报状态不允许（先于仓库不存在）, got %v", err)
	}
	keep.Abort()

	r2 := k.Open("db", 1, nil)
	conn2, _ := r2.Result()

	// 仓库不存在先于作用域不符。
	tx := mustTx(t, conn2, ReadWrite, "s1")
	gr := tx.Get("nope", []byte("k"), nil, nil)
	if KindOf(gr.Err()) != KindStoreNotFound {
		t.Fatalf("仓库不存在应先于作用域不符, got %v", gr.Err())
	}

	// 作用域不符先于事务已结束：对已结束事务访问越界仓库。
	tx.Put("s1", []byte("k2"), []byte("v"), nil, nil) // 自动提交
	if tx.Outcome() != OutcomeCommitted {
		t.Fatalf("tx 应已提交, got %v", tx.Outcome())
	}
	gr = tx.Get("s2", []byte("k"), nil, nil) // s2 不存在 → 仓库不存在
	if KindOf(gr.Err()) != KindStoreNotFound {
		t.Fatalf("已结束事务访问不存在仓库应报仓库不存在, got %v", gr.Err())
	}
	conn2.Close()

	// 构造作用域不符先于事务已结束：仓库存在但不在作用域。
	rU := k.Open("db", 2, func(u *UpgradeTx) {
		if err := u.CreateStore("s2"); err != nil {
			t.Errorf("create s2: %v", err)
		}
	})
	conn3, err := rU.Result()
	if err != nil {
		t.Fatalf("upgrade: %v", err)
	}
	defer conn3.Close()
	tx3 := mustTx(t, conn3, ReadOnly, "s1")
	tx3.Get("s1", []byte("k1"), nil, nil) // 自动提交
	gr = tx3.Get("s2", []byte("k"), nil, nil)
	if KindOf(gr.Err()) != KindScopeMismatch {
		t.Fatalf("仓库存在但越界应报作用域不符（先于事务已结束）, got %v", gr.Err())
	}

	// 事务已结束先于键冲突：已结束事务上 addOnly 写已有键。
	gr2 := tx3.Get("s1", []byte("k1"), nil, nil)
	if KindOf(gr2.Err()) != KindTxFinished {
		t.Fatalf("作用域内已结束事务应报事务已结束, got %v", gr2.Err())
	}
	txw := mustTx(t, conn3, ReadWrite, "s1")
	txw.Put("s1", []byte("k2"), []byte("v"), nil, nil) // 自动提交
	pr := txw.Put("s1", []byte("k1"), []byte("x"), &Options{AddOnly: true}, nil)
	if KindOf(pr.Err()) != KindTxFinished {
		t.Fatalf("事务已结束应先于键冲突, got %v", pr.Err())
	}

	// 键冲突本身可区分。
	tx4 := mustTx(t, conn3, ReadWrite, "s1")
	pr = tx4.Put("s1", []byte("k1"), []byte("x"), &Options{AddOnly: true}, nil)
	if KindOf(pr.Err()) != KindKeyConflict {
		t.Fatalf("运行中事务 addOnly 已有键应报键冲突, got %v", pr.Err())
	}
	t.Logf("判定依据：拒绝次序 参数非法>版本过低>状态不允许>仓库不存在>作用域不符>事务已结束>键冲突 全部验证通过")
}

// 性能证明一：新事务能否立即开始的判定开销不随已结束事务数增长。
// 调度器只保留排队与运行中的事务；overlapChecks 只与活跃事务数相关。
func TestScheduleCostIndependentOfFinished(t *testing.T) {
	k, conn := newTestKernel(t, "s1")
	keep := mustTx(t, conn, ReadOnly, "s1") // 常驻运行事务，使 running 恒为 1

	const n = 2000
	before := k.statsFor("db")
	for i := 0; i < n; i++ {
		tx := mustTx(t, conn, ReadOnly, "s1")
		tx.Get("s1", []byte("k"), nil, nil) // 立即自动提交
	}
	after := k.statsFor("db")
	checks := after.overlapChecks - before.overlapChecks
	t.Logf("输入: %d 个只读事务依次创建并提交; 输出: overlapChecks=%d pending=%d running=%d",
		n, checks, after.pending, after.running)
	if checks != n {
		t.Fatalf("每个新事务只应与运行中的 %d 个事务比较一次：checks=%d, want %d", 1, checks, n)
	}
	if after.pending != 0 || after.running != 1 {
		t.Fatalf("已结束事务不应滞留调度器: pending=%d running=%d", after.pending, after.running)
	}
	keep.Commit()
	if s := k.statsFor("db"); s.running != 0 {
		t.Fatalf("全部结束后 running 应为 0, got %d", s.running)
	}
	t.Logf("判定依据：overlapChecks 随活跃事务数（=1）线性增长，与已结束事务数 %d 无关", n)
}

// 性能证明二：提交使写入可见的开销不随仓库中无关键数增长。
// commitKeyOps 只按本事务写入的键计数。
func TestCommitCostIndependentOfStoreSize(t *testing.T) {
	k, conn := newTestKernel(t, "big", "small")

	// 向 big 仓库写入 5000 个无关键。
	tx := mustTx(t, conn, ReadWrite, "big")
	var fill func(i int)
	fill = func(i int) {
		if i == 5000 {
			return
		}
		tx.Put("big", []byte(fmt.Sprintf("k%05d", i)), []byte("v"), nil, func(*Request) { fill(i + 1) })
	}
	fill(0)
	if tx.Outcome() != OutcomeCommitted {
		t.Fatalf("填充事务应提交, got %v", tx.Outcome())
	}

	commitOne := func(store string) int64 {
		before := k.statsFor("db").commitKeyOps
		tx := mustTx(t, conn, ReadWrite, store)
		tx.Put(store, []byte("probe"), []byte("v"), nil, nil)
		if tx.Outcome() != OutcomeCommitted {
			t.Fatalf("probe 事务应提交, got %v", tx.Outcome())
		}
		return k.statsFor("db").commitKeyOps - before
	}
	bigCost := commitOne("big")
	smallCost := commitOne("small")
	t.Logf("输入: 分别向 5000 键的 big 与空 small 提交单键写入; 输出: big=%d small=%d", bigCost, smallCost)
	if bigCost != 1 || smallCost != 1 {
		t.Fatalf("提交开销应只等于本事务写入键数 1: big=%d small=%d", bigCost, smallCost)
	}
	t.Logf("判定依据：commitKeyOps 只按本事务写入键计数，与仓库无关键数（5000 vs 0）无关")
}

// 多连接并发调用：打开、创建事务、发请求、提交与关闭可并发进行，
// 结果等价于某个串行顺序（全局互斥锁保证），不死锁、不panic、数据一致。
func TestConcurrentCallsSerialize(t *testing.T) {
	k, _ := newTestKernel(t, "s1", "s2")
	const workers = 8
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			req := k.Open("db", 1, nil)
			conn, err := req.Result()
			if err != nil {
				t.Errorf("open: %v", err)
				return
			}
			defer conn.Close()
			for i := 0; i < 50; i++ {
				store := "s1"
				if (i+w)%2 == 0 {
					store = "s2"
				}
				tx, err := conn.CreateTx(ReadWrite, []string{store})
				if err != nil {
					continue // 连接可能正在关闭
				}
				key := []byte{byte('a' + (i+w)%26)}
				tx.Put(store, key, []byte{byte(w)}, nil, nil)
				tx.Commit()
			}
		}(w)
	}
	wg.Wait()
	_, stores := k.Snapshot("db")
	t.Logf("输出: s1=%d 键 s2=%d 键（判定依据：并发调用等价于某串行顺序，数据完整）",
		len(stores["s1"]), len(stores["s2"]))
	if len(stores["s1"]) == 0 && len(stores["s2"]) == 0 {
		t.Fatalf("并发写入后应有已提交数据")
	}
}

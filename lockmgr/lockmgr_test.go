package lockmgr_test

import (
	"errors"
	"fmt"
	"math/rand"
	"reflect"
	"strings"
	"sync"
	"testing"

	"ontology/lockmgr"
)

// logOp 打印一次操作的输入、输出与判定依据。
func logOp(t *testing.T, input, output, why string) {
	t.Helper()
	t.Logf("输入: %s | 输出: %s | 判定依据: %s", input, output, why)
}

func errStr(err error) string {
	if err == nil {
		return "成功"
	}
	return "拒绝(" + err.Error() + ")"
}

func mustStatus(t *testing.T, m *lockmgr.Manager, txID uint64) lockmgr.TxStatus {
	t.Helper()
	st, ok := m.Status(txID)
	if !ok {
		t.Fatalf("事务 %d 不存在", txID)
	}
	return st
}

func mustKey(t *testing.T, m *lockmgr.Manager, key string) lockmgr.KeyInfo {
	t.Helper()
	ki, ok := m.KeyInfoOf(key)
	if !ok {
		t.Fatalf("键 %q 不存在", key)
	}
	return ki
}

// TestOldWoundsYoung 老事务伤害年轻持有者：其全部锁立即释放、状态变已伤害。
func TestOldWoundsYoung(t *testing.T) {
	m := lockmgr.NewManager()
	t1 := m.Begin() // 年龄 1，更老
	t2 := m.Begin() // 年龄 2，更年轻

	if err := m.Acquire(t2, "a"); err != nil {
		t.Fatal(err)
	}
	if err := m.Acquire(t2, "b"); err != nil {
		t.Fatal(err)
	}
	logOp(t, "T2(年龄2) 申请 a,b", "成功，持有 a 和 b", "键空闲直接授予")

	err := m.Acquire(t1, "a")
	logOp(t, "T1(年龄1) 申请 a", errStr(err), "持有者 T2(年龄2) 更年轻，伤害 T2 后授予 T1")
	if err != nil {
		t.Fatal(err)
	}

	st := mustStatus(t, m, t2)
	if st.State != lockmgr.StateWounded || st.WoundedBy != 1 {
		t.Fatalf("T2 应为已伤害且伤害者年龄号为 1，实际 %+v", st)
	}
	logOp(t, "查询 T2 状态", fmt.Sprintf("%s，伤害者年龄号 %d", st.State, st.WoundedBy), "被伤害事务记录伤害者")

	if ki := mustKey(t, m, "a"); ki.Holder != t1 {
		t.Fatalf("键 a 持有者应为 T1，实际 %d", ki.Holder)
	}
	if ki := mustKey(t, m, "b"); ki.Holder != 0 {
		t.Fatalf("键 b 应已释放，实际持有者 %d", ki.Holder)
	}
	logOp(t, "查询键 a,b", "a 持有者为 T1，b 空闲", "被伤害事务持有的全部锁立即释放")
}

// TestYoungWaitsForOld 年轻事务排队等老持有者；持有者自己重复申请不改变状态。
func TestYoungWaitsForOld(t *testing.T) {
	m := lockmgr.NewManager()
	t1 := m.Begin()
	t2 := m.Begin()

	if err := m.Acquire(t1, "a"); err != nil {
		t.Fatal(err)
	}
	before := m.Snapshot()
	if err := m.Acquire(t1, "a"); err != nil {
		t.Fatal(err)
	}
	if after := m.Snapshot(); !reflect.DeepEqual(before, after) {
		t.Fatalf("持有者重复申请不应改变任何状态\n前: %+v\n后: %+v", before, after)
	}
	logOp(t, "T1 再次申请 a", "成功，状态不变", "持有者就是自己，视为已持有")

	err := m.Acquire(t2, "a")
	logOp(t, "T2(年龄2) 申请 a", errStr(err), "持有者 T1(年龄1) 更老，T2 进入等待队列")
	if err != nil {
		t.Fatal(err)
	}
	st := mustStatus(t, m, t2)
	if st.State != lockmgr.StateWaiting || st.WaitingOn != "a" {
		t.Fatalf("T2 应等待键 a，实际 %+v", st)
	}
	ki := mustKey(t, m, "a")
	if ki.Holder != t1 || !reflect.DeepEqual(ki.Queue, []uint64{t2}) {
		t.Fatalf("键 a 应为 T1 持有、T2 排队，实际 %+v", ki)
	}
	logOp(t, "查询 T2 与键 a", fmt.Sprintf("T2 等待 a；a 持有者 T1，队列 %v", ki.Queue), "年轻事务排队等老持有者")
}

// TestGrantByAgeNotArrival 释放后按年龄号而非到达先后授予。
func TestGrantByAgeNotArrival(t *testing.T) {
	m := lockmgr.NewManager()
	t1 := m.Begin()
	t2 := m.Begin()
	t3 := m.Begin()

	if err := m.Acquire(t1, "a"); err != nil {
		t.Fatal(err)
	}
	if err := m.Acquire(t3, "a"); err != nil { // T3 先到
		t.Fatal(err)
	}
	if err := m.Acquire(t2, "a"); err != nil { // T2 后到
		t.Fatal(err)
	}
	logOp(t, "T3、T2 依次申请 a", "都进入队列", "持有者 T1 最老，年轻者排队")

	if err := m.Commit(t1); err != nil {
		t.Fatal(err)
	}
	ki := mustKey(t, m, "a")
	if ki.Holder != t2 {
		t.Fatalf("应为年龄号更小的 T2 获得键 a，实际持有者 %d", ki.Holder)
	}
	logOp(t, "T1 提交", "T2 获得 a，T3 继续等待", "队列中年龄号最小者(2<3)优先，而非先到的 T3")

	if err := m.Commit(t2); err != nil {
		t.Fatal(err)
	}
	if ki := mustKey(t, m, "a"); ki.Holder != t3 {
		t.Fatalf("T2 提交后应为 T3 获得键 a，实际 %d", ki.Holder)
	}
	if st := mustStatus(t, m, t3); st.State != lockmgr.StateActive {
		t.Fatalf("T3 获得锁后应回到活跃，实际 %s", st.State)
	}
	logOp(t, "T2 提交", "T3 获得 a 并回到活跃", "锁释放时授予队列中年龄号最小的等待者")
}

// TestWoundCancelsQueueRequests 被伤害者在别处的排队请求一并撤销。
func TestWoundCancelsQueueRequests(t *testing.T) {
	m := lockmgr.NewManager()
	t1 := m.Begin() // 年龄 1
	t2 := m.Begin() // 年龄 2

	if err := m.Acquire(t1, "a"); err != nil {
		t.Fatal(err)
	}
	if err := m.Acquire(t2, "b"); err != nil {
		t.Fatal(err)
	}
	if err := m.Acquire(t2, "a"); err != nil { // T2 等 T1 的 a
		t.Fatal(err)
	}
	logOp(t, "T2 持有 b 并排队等 a", "T2 等待 a", "T1 更老，T2 排队")

	err := m.Acquire(t1, "b")
	logOp(t, "T1(年龄1) 申请 b", errStr(err), "持有者 T2 更年轻，伤害 T2：释放 b 并撤销其在 a 上的排队")
	if err != nil {
		t.Fatal(err)
	}
	if ki := mustKey(t, m, "a"); len(ki.Queue) != 0 {
		t.Fatalf("T2 在键 a 上的排队请求应被撤销，实际队列 %v", ki.Queue)
	}
	if ki := mustKey(t, m, "b"); ki.Holder != t1 {
		t.Fatalf("键 b 应授予 T1，实际持有者 %d", ki.Holder)
	}
	st := mustStatus(t, m, t2)
	if st.State != lockmgr.StateWounded || st.WoundedBy != 1 {
		t.Fatalf("T2 应为已伤害，实际 %+v", st)
	}
	logOp(t, "查询键 a 队列与 T2 状态", "a 队列空，b 归 T1，T2 已伤害", "伤害同时撤销全部排队请求")
}

// TestRestartKeepsAgeEventuallyOldest 重启保持年龄号，终成最老而不再被伤害。
func TestRestartKeepsAgeEventuallyOldest(t *testing.T) {
	m := lockmgr.NewManager()
	t1 := m.Begin()
	t2 := m.Begin()
	t3 := m.Begin()

	if err := m.Acquire(t3, "x"); err != nil {
		t.Fatal(err)
	}
	if err := m.Acquire(t1, "x"); err != nil { // 伤害 T3
		t.Fatal(err)
	}
	st := mustStatus(t, m, t3)
	logOp(t, "T1 申请 T3 持有的 x", fmt.Sprintf("T3 %s，伤害者年龄号 %d", st.State, st.WoundedBy), "老伤年轻")

	if err := m.Restart(t3); err != nil {
		t.Fatal(err)
	}
	st = mustStatus(t, m, t3)
	if st.State != lockmgr.StateActive || st.Age != 3 || st.WoundedBy != 0 {
		t.Fatalf("重启后应活跃、年龄号不变为 3，实际 %+v", st)
	}
	logOp(t, "重启 T3", "活跃，年龄号仍为 3，无锁", "重启保持年龄号")

	// 已伤害事务只能重启：重启前申请/提交均被拒绝。
	if err := m.Acquire(t1, "y"); err != nil {
		t.Fatal(err)
	}
	if err := m.Commit(t1); err != nil {
		t.Fatal(err)
	}
	if err := m.Commit(t2); err != nil {
		t.Fatal(err)
	}
	// 现在 T3(年龄3) 是活跃事务中最老的。
	t4 := m.Begin() // 年龄 4
	if err := m.Acquire(t4, "z"); err != nil {
		t.Fatal(err)
	}
	err := m.Acquire(t3, "z")
	logOp(t, "T3(年龄3) 申请 T4(年龄4) 持有的 z", errStr(err), "T3 已成最老，伤害 T4 而不再被伤害")
	if err != nil {
		t.Fatal(err)
	}
	if ki := mustKey(t, m, "z"); ki.Holder != t3 {
		t.Fatalf("键 z 应归 T3，实际 %d", ki.Holder)
	}
	if st := mustStatus(t, m, t4); st.State != lockmgr.StateWounded || st.WoundedBy != 3 {
		t.Fatalf("T4 应被 T3 伤害，实际 %+v", st)
	}
}

// TestWoundedOnlyRestart 已伤害事务只能重启，中止仍允许。
func TestWoundedOnlyRestart(t *testing.T) {
	m := lockmgr.NewManager()
	t1 := m.Begin()
	t2 := m.Begin()

	if err := m.Acquire(t2, "a"); err != nil {
		t.Fatal(err)
	}
	if err := m.Acquire(t1, "a"); err != nil { // 伤害 T2
		t.Fatal(err)
	}
	if err := m.Acquire(t2, "c"); !errors.Is(err, lockmgr.ErrTxWounded) {
		t.Fatalf("已伤害事务申请应报 ErrTxWounded，实际 %v", err)
	}
	logOp(t, "T2(已伤害) 申请 c", errStr(m.Acquire(t2, "c")), "已伤害事务只能重启")
	if err := m.Commit(t2); !errors.Is(err, lockmgr.ErrTxWounded) {
		t.Fatalf("已伤害事务提交应报 ErrTxWounded，实际 %v", err)
	}
	if err := m.Abort(t2); err != nil {
		t.Fatalf("已伤害事务允许中止，实际 %v", err)
	}
	logOp(t, "T2(已伤害) 中止", "成功", "中止在活跃、等待、已伤害时都允许")
}

// TestInvalidOperationsOrdered 非法情形按固定顺序只报第一个，且不改变任何状态。
func TestInvalidOperationsOrdered(t *testing.T) {
	m := lockmgr.NewManager()

	if err := m.Acquire(999, ""); !errors.Is(err, lockmgr.ErrEmptyKey) {
		t.Fatalf("空键应最先报 ErrEmptyKey，实际 %v", err)
	}
	logOp(t, "Acquire(999, \"\")", errStr(m.Acquire(999, "")), "固定顺序第一：键为空（即使事务也不存在）")

	if err := m.Acquire(999, "a"); !errors.Is(err, lockmgr.ErrTxNotFound) {
		t.Fatalf("应报 ErrTxNotFound，实际 %v", err)
	}
	if err := m.Commit(999); !errors.Is(err, lockmgr.ErrTxNotFound) {
		t.Fatalf("提交不存在事务应报 ErrTxNotFound，实际 %v", err)
	}
	if err := m.Restart(999); !errors.Is(err, lockmgr.ErrTxNotFound) {
		t.Fatalf("重启不存在事务应报 ErrTxNotFound，实际 %v", err)
	}
	if err := m.Abort(999); !errors.Is(err, lockmgr.ErrTxNotFound) {
		t.Fatalf("中止不存在事务应报 ErrTxNotFound，实际 %v", err)
	}

	t1 := m.Begin()
	if err := m.Acquire(t1, "k"); err != nil {
		t.Fatal(err)
	}
	if err := m.Commit(t1); err != nil {
		t.Fatal(err)
	}
	if err := m.Acquire(t1, "k"); !errors.Is(err, lockmgr.ErrTxTerminated) {
		t.Fatalf("已提交事务申请应报 ErrTxTerminated，实际 %v", err)
	}
	if err := m.Commit(t1); !errors.Is(err, lockmgr.ErrTxTerminated) {
		t.Fatalf("重复提交应报 ErrTxTerminated，实际 %v", err)
	}
	if err := m.Abort(t1); !errors.Is(err, lockmgr.ErrTxTerminated) {
		t.Fatalf("中止已终结事务应报 ErrTxTerminated，实际 %v", err)
	}
	logOp(t, "已提交事务的申请/提交/中止", "均拒绝(事务已提交或已中止)", "固定顺序：已终结优先于已伤害与等待")

	t2 := m.Begin()
	t3 := m.Begin()
	if err := m.Acquire(t2, "k"); err != nil {
		t.Fatal(err)
	}
	if err := m.Acquire(t3, "k"); err != nil { // T3 排队
		t.Fatal(err)
	}
	before := m.Snapshot()
	if err := m.Acquire(t3, "other"); !errors.Is(err, lockmgr.ErrTxWaiting) {
		t.Fatalf("等待中事务申请应报 ErrTxWaiting，实际 %v", err)
	}
	if err := m.Commit(t3); !errors.Is(err, lockmgr.ErrTxWaiting) {
		t.Fatalf("等待中事务提交应报 ErrTxWaiting，实际 %v", err)
	}
	if after := m.Snapshot(); !reflect.DeepEqual(before, after) {
		t.Fatalf("被拒绝的操作不得改变任何状态\n前: %+v\n后: %+v", before, after)
	}
	logOp(t, "T3(等待中) 申请/提交", "均拒绝(事务正在等待)，快照不变", "被拒绝的操作不得改变任何状态")

	if err := m.Restart(t2); !errors.Is(err, lockmgr.ErrTxNotWounded) {
		t.Fatalf("重启活跃事务应报 ErrTxNotWounded，实际 %v", err)
	}
	if err := m.Restart(t3); !errors.Is(err, lockmgr.ErrTxNotWounded) {
		t.Fatalf("重启等待事务应报 ErrTxNotWounded，实际 %v", err)
	}
	logOp(t, "重启非已伤害事务", "拒绝(事务并非已伤害状态，不能重启)", "重启有独立拒绝原因")
}

// TestAbortVariants 中止在活跃、等待、已伤害时都允许并释放全部锁与排队请求。
func TestAbortVariants(t *testing.T) {
	m := lockmgr.NewManager()
	t1 := m.Begin()
	t2 := m.Begin()
	t3 := m.Begin()

	if err := m.Acquire(t1, "a"); err != nil {
		t.Fatal(err)
	}
	if err := m.Acquire(t2, "a"); err != nil { // T2 排队
		t.Fatal(err)
	}
	if err := m.Acquire(t3, "a"); err != nil { // T3 排队
		t.Fatal(err)
	}
	// 中止等待者 T2：从队列移除。
	if err := m.Abort(t2); err != nil {
		t.Fatal(err)
	}
	if ki := mustKey(t, m, "a"); !reflect.DeepEqual(ki.Queue, []uint64{t3}) {
		t.Fatalf("T2 中止后队列应只剩 T3，实际 %v", ki.Queue)
	}
	logOp(t, "中止等待者 T2", "成功，队列剩 [T3]", "中止释放排队请求")

	// 中止活跃持有者 T1：锁授予队列中年龄号最小的 T3。
	if err := m.Abort(t1); err != nil {
		t.Fatal(err)
	}
	if ki := mustKey(t, m, "a"); ki.Holder != t3 {
		t.Fatalf("T1 中止后键 a 应授予 T3，实际 %d", ki.Holder)
	}
	logOp(t, "中止持有者 T1", "T3 获得 a", "中止释放全部锁并按年龄授予等待者")
}

// randomOps 用同一种子生成确定性操作序列。
func randomOps(seed int64, n, maxTx, maxKey int) []string {
	r := rand.New(rand.NewSource(seed))
	ops := make([]string, 0, n)
	for i := 0; i < n; i++ {
		txID := uint64(r.Intn(maxTx) + 1)
		key := fmt.Sprintf("k%d", r.Intn(maxKey))
		switch r.Intn(5) {
		case 0:
			ops = append(ops, "begin")
		case 1:
			ops = append(ops, fmt.Sprintf("acquire %d %s", txID, key))
		case 2:
			ops = append(ops, fmt.Sprintf("commit %d", txID))
		case 3:
			ops = append(ops, fmt.Sprintf("abort %d", txID))
		case 4:
			ops = append(ops, fmt.Sprintf("restart %d", txID))
		}
	}
	return ops
}

// applyOp 执行一条操作并返回结果描述。
func applyOp(m *lockmgr.Manager, op string) string {
	var txID uint64
	var key string
	if op == "begin" {
		return fmt.Sprintf("begin -> T%d", m.Begin())
	}
	var name string
	if strings.HasPrefix(op, "acquire") {
		_, _ = fmt.Sscanf(op, "%s %d %s", &name, &txID, &key)
		return fmt.Sprintf("%s -> %s", op, errStr(m.Acquire(txID, key)))
	}
	_, _ = fmt.Sscanf(op, "%s %d", &name, &txID)
	switch name {
	case "commit":
		return fmt.Sprintf("%s -> %s", op, errStr(m.Commit(txID)))
	case "abort":
		return fmt.Sprintf("%s -> %s", op, errStr(m.Abort(txID)))
	case "restart":
		return fmt.Sprintf("%s -> %s", op, errStr(m.Restart(txID)))
	}
	return op + " -> ?"
}

// TestDeterministicReplay 相同操作序列重放结果完全相同。
func TestDeterministicReplay(t *testing.T) {
	ops := randomOps(42, 500, 6, 4)
	run := func() (lockmgr.Snapshot, []string) {
		m := lockmgr.NewManager()
		results := make([]string, 0, len(ops))
		for _, op := range ops {
			results = append(results, applyOp(m, op))
		}
		return m.Snapshot(), results
	}
	snap1, res1 := run()
	snap2, res2 := run()
	if !reflect.DeepEqual(res1, res2) {
		t.Fatal("两次重放的每步结果不一致")
	}
	if !reflect.DeepEqual(snap1, snap2) {
		t.Fatalf("两次重放的最终快照不一致\n1: %+v\n2: %+v", snap1, snap2)
	}
	t.Logf("重放 %d 步操作，两次结果与最终快照完全一致", len(ops))
}

// checkInvariants 自检等待关系：任意时刻每个键至多一个持有者、
// 每个等待者都比持有者年轻、已伤害或已终结的事务不持锁也不在队列。
func checkInvariants(snap lockmgr.Snapshot) error {
	status := make(map[uint64]lockmgr.TxStatus, len(snap.Txs))
	lockedBy := make(map[uint64][]string) // tx -> keys
	queuedIn := make(map[uint64]string)   // tx -> key
	for _, st := range snap.Txs {
		status[st.ID] = st
	}
	for _, ki := range snap.Keys {
		if ki.Holder == 0 && len(ki.Queue) != 0 {
			return fmt.Errorf("键 %s 空闲却有等待队列 %v", ki.Key, ki.Queue)
		}
		if ki.Holder != 0 {
			holder, ok := status[ki.Holder]
			if !ok {
				return fmt.Errorf("键 %s 持有者 T%d 不存在", ki.Key, ki.Holder)
			}
			if holder.State != lockmgr.StateActive {
				return fmt.Errorf("键 %s 持有者 T%d 状态为 %s，应为活跃", ki.Key, ki.Holder, holder.State)
			}
			lockedBy[ki.Holder] = append(lockedBy[ki.Holder], ki.Key)
			for _, id := range ki.Queue {
				w, ok := status[id]
				if !ok {
					return fmt.Errorf("键 %s 等待者 T%d 不存在", ki.Key, id)
				}
				if w.State != lockmgr.StateWaiting || w.WaitingOn != ki.Key {
					return fmt.Errorf("键 %s 等待者 T%d 状态为 %s(%q)", ki.Key, id, w.State, w.WaitingOn)
				}
				if w.Age <= holder.Age {
					return fmt.Errorf("键 %s 等待者 T%d(年龄%d) 不比持有者 T%d(年龄%d) 年轻",
						ki.Key, id, w.Age, holder.ID, holder.Age)
				}
				if _, dup := queuedIn[id]; dup {
					return fmt.Errorf("T%d 出现在多个队列", id)
				}
				queuedIn[id] = ki.Key
			}
		}
	}
	for _, st := range snap.Txs {
		switch st.State {
		case lockmgr.StateWounded, lockmgr.StateCommitted, lockmgr.StateAborted:
			if keys := lockedBy[st.ID]; len(keys) != 0 {
				return fmt.Errorf("%s事务 T%d 仍持有锁 %v", st.State, st.ID, keys)
			}
			if key, ok := queuedIn[st.ID]; ok {
				return fmt.Errorf("%s事务 T%d 仍在键 %s 的队列中", st.State, st.ID, key)
			}
		case lockmgr.StateWaiting:
			if queuedIn[st.ID] != st.WaitingOn {
				return fmt.Errorf("等待事务 T%d 不在键 %s 的队列中", st.ID, st.WaitingOn)
			}
		}
	}
	return nil
}

// TestConcurrentRandomInterleaving 并发随机交错下的等待关系自检。
func TestConcurrentRandomInterleaving(t *testing.T) {
	m := lockmgr.NewManager()
	const workers = 8
	const opsPerWorker = 400

	stop := make(chan struct{})
	var checkerWG sync.WaitGroup
	checkerWG.Add(1)
	go func() {
		defer checkerWG.Done()
		for {
			select {
			case <-stop:
				return
			default:
				if err := checkInvariants(m.Snapshot()); err != nil {
					t.Errorf("不变量被破坏: %v", err)
					return
				}
			}
		}
	}()

	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(seed int64) {
			defer wg.Done()
			for _, op := range randomOps(seed, opsPerWorker, 12, 6) {
				applyOp(m, op)
			}
		}(int64(w*1000 + 7))
	}
	wg.Wait()
	close(stop)
	checkerWG.Wait()

	if err := checkInvariants(m.Snapshot()); err != nil {
		t.Fatalf("最终快照不变量被破坏: %v", err)
	}
	snap := m.Snapshot()
	t.Logf("并发交错完成：%d 个事务、%d 个键，全部不变量成立", len(snap.Txs), len(snap.Keys))
	for _, ki := range snap.Keys {
		t.Logf("键 %s：持有者 T%d，队列 %v", ki.Key, ki.Holder, ki.Queue)
	}
}

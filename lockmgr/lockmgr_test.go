package lockmgr

import (
	"fmt"
	"math/rand"
	"strings"
	"sync"
	"testing"
)

// checkReject 断言 err 为 *RejectError 且原因与预期一致，并打印判定依据。
func checkReject(t *testing.T, err error, reason RejectReason) {
	t.Helper()
	if err == nil {
		t.Fatalf("期望拒绝（原因=%s），实际成功", reason)
	}
	rerr, ok := err.(*RejectError)
	if !ok {
		t.Fatalf("期望 *RejectError，实际 %T: %v", err, err)
	}
	if rerr.Reason != reason {
		t.Fatalf("判定依据不匹配: 期望原因 %s，实际 %s", reason, rerr.Reason)
	}
	t.Logf("输出: 拒绝符合预期 op=%s txn=%d key=%q 原因=%s", rerr.Op, rerr.TxnID, rerr.Key, rerr.Reason)
}

// mustOK 断言操作成功并打印日志。
func mustOK(t *testing.T, op string, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("%s 期望成功，实际被拒绝: %v", op, err)
	}
	t.Logf("输出: %s 成功", op)
}

// checkTxn 断言事务处于期望状态，返回其快照并打印判定依据。
func checkTxn(t *testing.T, m *Manager, id int, want TxnState) TxnInfo {
	t.Helper()
	info, ok := m.TxnInfo(id)
	if !ok {
		t.Fatalf("事务 %d 不存在", id)
	}
	if info.State != want {
		t.Fatalf("判定依据不匹配: txn=%d 期望状态 %s，实际 %s", id, want, info.State)
	}
	t.Logf("输出: txn=%d age=%d state=%s waitingKey=%q woundedBy=%d held=%v（判定依据: 期望状态 %s）",
		info.ID, info.Age, info.State, info.WaitingKey, info.WoundedBy, info.HeldKeys, want)
	return info
}

// checkKey 断言键的持有者与等待队列（队列按到达顺序），并打印判定依据。
func checkKey(t *testing.T, m *Manager, key string, wantHolder int, wantQueue ...int) {
	t.Helper()
	info := m.KeyInfo(key)
	if info.Holder != wantHolder {
		t.Fatalf("判定依据不匹配: key=%q 期望持有者 %d，实际 %d", key, wantHolder, info.Holder)
	}
	if len(info.Queue) != len(wantQueue) {
		t.Fatalf("判定依据不匹配: key=%q 期望队列 %v，实际 %v", key, wantQueue, info.Queue)
	}
	for i, id := range wantQueue {
		if info.Queue[i].TxnID != id {
			t.Fatalf("判定依据不匹配: key=%q 期望队列 %v，实际 %v", key, wantQueue, info.Queue)
		}
	}
	t.Logf("输出: key=%q holder=%d holderAge=%d queue=%v（判定依据: 期望持有者 %d 队列 %v）",
		key, info.Holder, info.HolderAge, info.Queue, wantHolder, wantQueue)
}

// snapshot 返回系统全量状态的字符串表示，用于"被拒绝的操作不得改变任何状态"
// 与"相同操作序列重放结果完全相同"的判定。
func snapshot(m *Manager) string {
	var sb strings.Builder
	for _, ti := range m.TxnInfos() {
		fmt.Fprintf(&sb, "txn=%d age=%d state=%s waitingKey=%q woundedBy=%d held=%v\n",
			ti.ID, ti.Age, ti.State, ti.WaitingKey, ti.WoundedBy, ti.HeldKeys)
	}
	for _, ki := range m.KeyInfos() {
		fmt.Fprintf(&sb, "key=%q holder=%d holderAge=%d queue=%v\n", ki.Key, ki.Holder, ki.HolderAge, ki.Queue)
	}
	return sb.String()
}

func TestOldWoundsYoung(t *testing.T) {
	m := NewManager()
	t1 := m.Begin()
	t2 := m.Begin()
	t.Logf("输入: T1(age=1)、T2(age=2)；T2 先持有 a、b，更老的 T1 再申请 a")

	mustOK(t, "T2.Acquire(a)", m.Acquire(t2, "a"))
	mustOK(t, "T2.Acquire(b)", m.Acquire(t2, "b"))
	mustOK(t, "T1.Acquire(a)", m.Acquire(t1, "a"))

	// 判定依据：持有者 T2 更年轻，应被伤害；其全部锁立即释放，a 授予 T1，b 变空闲。
	w := checkTxn(t, m, t2, Wounded)
	if w.WoundedBy != 1 {
		t.Fatalf("判定依据不匹配: T2.WoundedBy 期望 1，实际 %d", w.WoundedBy)
	}
	if len(w.HeldKeys) != 0 {
		t.Fatalf("判定依据不匹配: 被伤害事务不应持有任何锁，实际 %v", w.HeldKeys)
	}
	checkTxn(t, m, t1, Active)
	checkKey(t, m, "a", t1)
	checkKey(t, m, "b", 0)
}

func TestYoungWaitsForOld(t *testing.T) {
	m := NewManager()
	t1 := m.Begin()
	t2 := m.Begin()
	t.Logf("输入: T1(age=1) 持有 a，更年轻的 T2(age=2) 申请 a")

	mustOK(t, "T1.Acquire(a)", m.Acquire(t1, "a"))
	mustOK(t, "T2.Acquire(a)", m.Acquire(t2, "a"))

	// 判定依据：持有者更老，申请者应进入该键的等待队列。
	w := checkTxn(t, m, t2, Waiting)
	if w.WaitingKey != "a" {
		t.Fatalf("判定依据不匹配: T2.WaitingKey 期望 a，实际 %q", w.WaitingKey)
	}
	checkKey(t, m, "a", t1, t2)

	// 持有者就是自己：视为已持有，不改变任何状态。
	before := snapshot(m)
	mustOK(t, "T1.Acquire(a) 重复申请", m.Acquire(t1, "a"))
	if after := snapshot(m); after != before {
		t.Fatalf("重复申请不应改变任何状态\n之前:\n%s\n之后:\n%s", before, after)
	}
	t.Logf("输出: 重复申请已持有的键后状态快照不变（判定依据: 持有者就是自己）")
}

func TestGrantByAgeNotArrival(t *testing.T) {
	m := NewManager()
	t1 := m.Begin()
	t2 := m.Begin()
	t3 := m.Begin()
	t4 := m.Begin()
	t.Logf("输入: T1 持有 a；等待者到达先后为 T4(age=4)、T2(age=2)、T3(age=3)")

	mustOK(t, "T1.Acquire(a)", m.Acquire(t1, "a"))
	mustOK(t, "T4.Acquire(a)", m.Acquire(t4, "a"))
	mustOK(t, "T2.Acquire(a)", m.Acquire(t2, "a"))
	mustOK(t, "T3.Acquire(a)", m.Acquire(t3, "a"))
	checkKey(t, m, "a", t1, t4, t2, t3)

	// 判定依据：锁释放时授予年龄号最小的等待者，而非最先到达者。
	mustOK(t, "T1.Commit", m.Commit(t1))
	checkKey(t, m, "a", t2, t4, t3)
	checkTxn(t, m, t2, Active)

	mustOK(t, "T2.Commit", m.Commit(t2))
	checkKey(t, m, "a", t3, t4)

	mustOK(t, "T3.Commit", m.Commit(t3))
	checkKey(t, m, "a", t4)
}

func TestWoundCancelsQueueRequest(t *testing.T) {
	m := NewManager()
	t1 := m.Begin()
	t2 := m.Begin()
	t.Logf("输入: T1(age=1) 持有 a；T2(age=2) 持有 b 且在 a 上排队；T1 申请 b")

	mustOK(t, "T1.Acquire(a)", m.Acquire(t1, "a"))
	mustOK(t, "T2.Acquire(b)", m.Acquire(t2, "b"))
	mustOK(t, "T2.Acquire(a)", m.Acquire(t2, "a"))
	checkKey(t, m, "a", t1, t2)

	mustOK(t, "T1.Acquire(b)", m.Acquire(t1, "b"))

	// 判定依据：T2 被伤害，其在 a 上的排队请求一并撤销，b 授予申请者 T1。
	w := checkTxn(t, m, t2, Wounded)
	if w.WoundedBy != 1 {
		t.Fatalf("判定依据不匹配: T2.WoundedBy 期望 1，实际 %d", w.WoundedBy)
	}
	checkKey(t, m, "a", t1)
	checkKey(t, m, "b", t1)
}

func TestRestartKeepsAgeAndEventuallyOldest(t *testing.T) {
	m := NewManager()
	t1 := m.Begin()
	t2 := m.Begin()
	t3 := m.Begin()
	t.Logf("输入: T3(age=3) 持有 a 被 T1(age=1) 伤害后重启；T1、T2 随后提交，T3 成为最老活跃事务")

	mustOK(t, "T3.Acquire(a)", m.Acquire(t3, "a"))
	mustOK(t, "T1.Acquire(a)", m.Acquire(t1, "a"))
	checkTxn(t, m, t3, Wounded)

	mustOK(t, "T3.Restart", m.Restart(t3))
	info := checkTxn(t, m, t3, Active)
	if info.Age != 3 {
		t.Fatalf("判定依据不匹配: 重启后年龄号应保持 3，实际 %d", info.Age)
	}
	if len(info.HeldKeys) != 0 {
		t.Fatalf("判定依据不匹配: 重启后不应持有任何锁，实际 %v", info.HeldKeys)
	}
	t.Logf("输出: 重启后年龄号保持 %d、不持有任何锁（判定依据: 年龄号不变才能终将成为最老）", info.Age)

	mustOK(t, "T1.Commit", m.Commit(t1))
	mustOK(t, "T2.Commit", m.Commit(t2))

	// T3 现在是活跃事务中最老的，更年轻的 T4 只能排队，无法伤害它。
	t4 := m.Begin()
	mustOK(t, "T3.Acquire(a)", m.Acquire(t3, "a"))
	mustOK(t, "T3.Acquire(b)", m.Acquire(t3, "b"))
	mustOK(t, "T4.Acquire(a)", m.Acquire(t4, "a"))
	checkTxn(t, m, t4, Waiting)
	checkTxn(t, m, t3, Active)

	mustOK(t, "T3.Commit", m.Commit(t3))
	checkKey(t, m, "a", t4)
	mustOK(t, "T4.Commit", m.Commit(t4))
	t.Logf("输出: T3 重启后终成最老、不再被伤害并顺利完成（判定依据: 等待边只从年轻指向年老）")
}

func TestRejectOrderAndNoStateChange(t *testing.T) {
	m := NewManager()
	t1 := m.Begin()
	t2 := m.Begin()
	t3 := m.Begin()
	t4 := m.Begin()

	mustOK(t, "T1.Acquire(done)", m.Acquire(t1, "done"))
	mustOK(t, "T1.Commit", m.Commit(t1))
	mustOK(t, "T3.Acquire(x)", m.Acquire(t3, "x"))
	mustOK(t, "T2.Acquire(x)", m.Acquire(t2, "x"))
	mustOK(t, "T4.Acquire(x)", m.Acquire(t4, "x"))

	before := snapshot(m)
	t.Logf("输入: 对不存在/已提交/已伤害/等待中的事务执行各类非法操作，并重启非已伤害事务")

	// 判定依据：固定顺序只报第一个——键为空优先于事务不存在。
	checkReject(t, m.Acquire(999, ""), ReasonEmptyKey)
	checkReject(t, m.Acquire(999, "k"), ReasonTxnNotFound)
	checkReject(t, m.Acquire(t1, "k"), ReasonTxnFinished)
	checkReject(t, m.Commit(t1), ReasonTxnFinished)
	checkReject(t, m.Acquire(t3, "k"), ReasonTxnWounded)
	checkReject(t, m.Commit(t3), ReasonTxnWounded)
	checkReject(t, m.Acquire(t4, "k"), ReasonTxnWaiting)
	checkReject(t, m.Commit(t4), ReasonTxnWaiting)
	// 重启一个并非已伤害的事务：独立原因。
	checkReject(t, m.Restart(t2), ReasonNotWounded)
	checkReject(t, m.Restart(t4), ReasonNotWounded)
	checkReject(t, m.Restart(999), ReasonTxnNotFound)

	if after := snapshot(m); after != before {
		t.Fatalf("被拒绝的操作不得改变任何状态\n之前:\n%s\n之后:\n%s", before, after)
	}
	t.Logf("输出: 全部拒绝后状态快照与之前完全一致（判定依据: 被拒绝的操作不得改变任何状态）")
}

func TestAbortInAllStates(t *testing.T) {
	m := NewManager()
	t1 := m.Begin()
	t2 := m.Begin()
	t3 := m.Begin()
	t4 := m.Begin()
	t.Logf("输入: 分别在活跃、等待、已伤害状态下中止事务")

	// 活跃时中止：释放全部锁。
	mustOK(t, "T1.Acquire(a)", m.Acquire(t1, "a"))
	mustOK(t, "T1.Abort", m.Abort(t1))
	checkTxn(t, m, t1, Aborted)
	checkKey(t, m, "a", 0)

	// 等待时中止：撤销排队请求，持有者的锁不受影响。
	mustOK(t, "T2.Acquire(b)", m.Acquire(t2, "b"))
	mustOK(t, "T3.Acquire(b)", m.Acquire(t3, "b"))
	mustOK(t, "T3.Abort", m.Abort(t3))
	checkTxn(t, m, t3, Aborted)
	checkKey(t, m, "b", t2)

	// 已伤害时中止。
	mustOK(t, "T4.Acquire(c)", m.Acquire(t4, "c"))
	mustOK(t, "T2.Acquire(c)", m.Acquire(t2, "c"))
	checkTxn(t, m, t4, Wounded)
	mustOK(t, "T4.Abort", m.Abort(t4))
	checkTxn(t, m, t4, Aborted)

	// 终态与不存在的事务不可中止。
	checkReject(t, m.Abort(t1), ReasonTxnFinished)
	checkReject(t, m.Abort(999), ReasonTxnNotFound)
}

// checkInvariants 自检任意时刻必须成立的不变量（可由多个 goroutine 并发调用）。
func checkInvariants(t *testing.T, m *Manager) {
	t.Helper()
	txns := make(map[int]TxnInfo)
	txnList, keyList := m.Snapshot()
	for _, ti := range txnList {
		txns[ti.ID] = ti
	}
	edges := make(map[int]int) // 等待边：等待者 -> 持有者
	for _, ki := range keyList {
		if ki.Holder != 0 {
			h, ok := txns[ki.Holder]
			if !ok {
				t.Errorf("key=%q 持有者 %d 不存在", ki.Key, ki.Holder)
				continue
			}
			if h.State != Active && h.State != Waiting {
				t.Errorf("key=%q 持有者 %d 状态为 %s，应为 Active 或 Waiting", ki.Key, ki.Holder, h.State)
			}
			if h.Age != ki.HolderAge {
				t.Errorf("key=%q 持有者年龄号不一致: %d vs %d", ki.Key, h.Age, ki.HolderAge)
			}
			found := false
			for _, k := range h.HeldKeys {
				if k == ki.Key {
					found = true
				}
			}
			if !found {
				t.Errorf("key=%q 持有者 %d 的 HeldKeys 缺少该键", ki.Key, ki.Holder)
			}
		}
		seen := make(map[int]bool)
		for _, w := range ki.Queue {
			if ki.Holder == 0 {
				t.Errorf("key=%q 空闲但等待队列非空", ki.Key)
			}
			if w.Age <= ki.HolderAge {
				t.Errorf("key=%q 等待者 txn=%d(age=%d) 不比持有者(age=%d) 年轻", ki.Key, w.TxnID, w.Age, ki.HolderAge)
			}
			if seen[w.TxnID] {
				t.Errorf("key=%q 队列中 txn=%d 重复", ki.Key, w.TxnID)
			}
			seen[w.TxnID] = true
			wt, ok := txns[w.TxnID]
			if !ok || wt.State != Waiting || wt.WaitingKey != ki.Key {
				t.Errorf("key=%q 队列中 txn=%d 状态不一致: %+v", ki.Key, w.TxnID, wt)
			}
			edges[w.TxnID] = ki.Holder
		}
	}
	for _, ti := range txns {
		switch ti.State {
		case Wounded, Committed, Aborted:
			if len(ti.HeldKeys) != 0 {
				t.Errorf("txn=%d 状态 %s 不应持有锁: %v", ti.ID, ti.State, ti.HeldKeys)
			}
		case Waiting:
			if _, ok := edges[ti.ID]; !ok {
				t.Errorf("txn=%d 处于 Waiting 却不在任何队列", ti.ID)
			}
		}
	}
	// 等待关系自检：沿等待边年龄严格递减故不可能成环，此处显式探测环。
	for start := range edges {
		visited := make(map[int]bool)
		cur := start
		for {
			next, ok := edges[cur]
			if !ok {
				break
			}
			if visited[cur] {
				t.Errorf("检测到等待环，起点 txn=%d", start)
				break
			}
			visited[cur] = true
			cur = next
		}
	}
}

// finishTxn 把一个事务驱动到终态：活跃则提交，等待则中止，已伤害则重启后重试。
func finishTxn(m *Manager, id int) {
	for i := 0; i < 100000; i++ {
		info, ok := m.TxnInfo(id)
		if !ok {
			return
		}
		switch info.State {
		case Committed, Aborted:
			return
		case Active:
			if m.Commit(id) == nil {
				return
			}
		case Waiting:
			if m.Abort(id) == nil {
				return
			}
		case Wounded:
			m.Restart(id)
		}
	}
}

func TestConcurrentRandomInterleaving(t *testing.T) {
	m := NewManager()
	const workers = 8
	const opsPerWorker = 400
	keys := []string{"a", "b", "c", "d", "e"}
	t.Logf("输入: %d 个 goroutine 各自以固定种子随机执行 %d 次 Begin/Acquire/Commit/Abort/Restart，键集 %v",
		workers, opsPerWorker, keys)

	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(seed int64) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(seed))
			var mine []int
			for i := 0; i < opsPerWorker; i++ {
				if len(mine) == 0 || rng.Intn(4) == 0 {
					mine = append(mine, m.Begin())
					continue
				}
				id := mine[rng.Intn(len(mine))]
				key := keys[rng.Intn(len(keys))]
				switch rng.Intn(4) {
				case 0:
					m.Acquire(id, key)
				case 1:
					m.Commit(id)
				case 2:
					m.Abort(id)
				case 3:
					m.Restart(id)
				}
				if i%50 == 0 {
					checkInvariants(t, m)
				}
			}
			for _, id := range mine {
				finishTxn(m, id)
			}
		}(int64(w + 1))
	}
	wg.Wait()

	checkInvariants(t, m)
	unfinished := 0
	for _, ti := range m.TxnInfos() {
		if ti.State != Committed && ti.State != Aborted {
			unfinished++
		}
	}
	for _, ki := range m.KeyInfos() {
		if ki.Holder != 0 || len(ki.Queue) != 0 {
			t.Errorf("key=%q 在全部事务终结后仍有持有者或等待者: %+v", ki.Key, ki)
		}
	}
	if unfinished != 0 {
		t.Errorf("仍有 %d 个事务未终结", unfinished)
	}
	t.Logf("输出: 并发交错结束，未终结事务=%d；判定依据: 每键至多一个持有者、等待者均比持有者年轻、"+
		"等待图无环、已伤害或已终结事务不持锁也不在任何队列", unfinished)
}

type scriptOp struct {
	kind string // begin / acquire / commit / abort / restart
	txn  int    // begin 的序号（从 0 开始）
	key  string
}

func runScript(script []scriptOp) string {
	m := NewManager()
	var ids []int
	var sb strings.Builder
	for _, o := range script {
		var err error
		switch o.kind {
		case "begin":
			ids = append(ids, m.Begin())
			fmt.Fprintf(&sb, "begin -> txn=%d\n", ids[len(ids)-1])
		case "acquire":
			err = m.Acquire(ids[o.txn], o.key)
		case "commit":
			err = m.Commit(ids[o.txn])
		case "abort":
			err = m.Abort(ids[o.txn])
		case "restart":
			err = m.Restart(ids[o.txn])
		}
		if o.kind != "begin" {
			fmt.Fprintf(&sb, "%s txn=%d key=%q -> err=%v\n", o.kind, ids[o.txn], o.key, err)
		}
	}
	sb.WriteString(snapshot(m))
	return sb.String()
}

func TestDeterministicReplay(t *testing.T) {
	script := []scriptOp{
		{"begin", 0, ""},
		{"begin", 0, ""},
		{"begin", 0, ""},
		{"begin", 0, ""},
		{"acquire", 1, "a"}, // T2 持有 a
		{"acquire", 2, "b"}, // T3 持有 b
		{"acquire", 0, "a"}, // T1 伤害 T2
		{"acquire", 3, "a"}, // T4 等待 T1
		{"restart", 1, ""},  // T2 重启，年龄号不变
		{"acquire", 1, "b"}, // T2 伤害 T3
		{"commit", 0, ""},   // T1 提交，a 授予 T4
		{"abort", 2, ""},    // T3 已伤害，允许中止
		{"commit", 3, ""},
		{"commit", 1, ""},
	}
	t.Logf("输入: 固定脚本共 %d 个操作，执行两次", len(script))
	first := runScript(script)
	second := runScript(script)
	if first != second {
		t.Fatalf("相同操作序列重放结果不同\n第一次:\n%s\n第二次:\n%s", first, second)
	}
	t.Logf("输出: 两次重放的操作结果与最终状态完全一致（判定依据: 全部操作在单互斥锁下串行化，无随机与时间依赖）")
}

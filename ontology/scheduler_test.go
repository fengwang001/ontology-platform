package ontology

import (
	"errors"
	"log/slog"
	"os"
	"testing"
)

func rejectCode(t *testing.T, err error) RejectCode {
	t.Helper()
	var re *RejectError
	if !errors.As(err, &re) {
		t.Fatalf("判定依据不符: 期望 *RejectError, 实际 %v", err)
	}
	return re.Code
}

func abortReason(t *testing.T, err error) AbortReason {
	t.Helper()
	var ae *AbortError
	if !errors.As(err, &ae) {
		t.Fatalf("判定依据不符: 期望 *AbortError, 实际 %v", err)
	}
	return ae.Reason
}

func newLoggedScheduler(t *testing.T) *Scheduler {
	t.Helper()
	s := NewScheduler()
	s.SetLogger(slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo})))
	return s
}

// TestRejectOrder 验证「键为空 → 事务不存在 → 已提交 → 已中止」只报第一个，且被拒绝操作不改状态。
func TestRejectOrder(t *testing.T) {
	s := newLoggedScheduler(t)
	ts := s.Begin()

	if _, err := s.Read(ts, ""); rejectCode(t, err) != RejectEmptyKey {
		t.Fatalf("输入 read(ts=%d,key=\"\") 的输出应为 empty_key", ts)
	}
	if err := s.Write(ts, "", 1); rejectCode(t, err) != RejectEmptyKey {
		t.Fatalf("输入 write(ts=%d,key=\"\") 的输出应为 empty_key", ts)
	}

	const ghost int64 = 999
	if _, err := s.Read(ghost, "k"); rejectCode(t, err) != RejectNoSuchTx {
		t.Fatal("不存在事务应优先报 no_such_transaction")
	}
	if err := s.Write(ghost, "k", 1); rejectCode(t, err) != RejectNoSuchTx {
		t.Fatal("不存在事务应优先报 no_such_transaction")
	}
	if _, err := s.Commit(ghost); rejectCode(t, err) != RejectNoSuchTx {
		t.Fatal("不存在事务提交应报 no_such_transaction")
	}

	if _, err := s.Commit(ts); err != nil {
		t.Fatalf("空缓冲提交应成功, 实际 %v", err)
	}
	if _, err := s.Read(ts, "k"); rejectCode(t, err) != RejectTxCommitted {
		t.Fatal("已提交事务上的读应报 transaction_committed")
	}
	if err := s.Write(ts, "k", 1); rejectCode(t, err) != RejectTxCommitted {
		t.Fatal("已提交事务上的写应报 transaction_committed")
	}
	if _, err := s.Commit(ts); rejectCode(t, err) != RejectTxCommitted {
		t.Fatal("重复提交应报 transaction_committed")
	}

	ab := s.Begin()
	_ = s.Write(ab, "x", 5)
	if err := s.Write(ab, "y", 1); err != nil {
		t.Fatalf("准备中止环境失败: %v", err)
	}
	// 让 y 的读时间戳高于 ab，制造「写过晚」中止并丢弃缓冲 {x,y}。
	later := s.Begin()
	if _, err := s.Read(later, "y"); err != nil {
		t.Fatalf("提升读时间戳的读失败: %v", err)
	}
	if err := s.Write(ab, "y", 9); abortReason(t, err) != AbortWriteLate {
		t.Fatal("应因写过晚中止")
	}
	if info, ok := s.TxInfo(ab); !ok || info.Status != TxAborted || len(info.Buffer) != 0 {
		t.Fatalf("中止后应为 aborted 且缓冲清空, 实际 %+v", info)
	}
	if _, err := s.Read(ab, "x"); rejectCode(t, err) != RejectTxAborted {
		t.Fatal("已中止事务应报 transaction_aborted")
	}
	if err := s.Write(ab, "x", 1); rejectCode(t, err) != RejectTxAborted {
		t.Fatal("已中止事务应报 transaction_aborted")
	}
	if _, err := s.Commit(ab); rejectCode(t, err) != RejectTxAborted {
		t.Fatal("已中止事务提交应报 transaction_aborted")
	}

	// 被拒绝与被中止的写均不得落盘：任何键的写时间戳与值都必须保持初值。
	for _, k := range []string{"k", "x", "y"} {
		ks, ok := s.KeyState(k)
		if ok && (ks.WriteTS != 0 || ks.Value != 0) {
			t.Fatalf("判定依据: 键 %s 不应被安装任何写, 实际 %+v", k, ks)
		}
	}
}

// TestReadLate 验证「读过晚」：ts < 键写时间戳时读中止，且缓冲被丢弃。
func TestReadLate(t *testing.T) {
	s := newLoggedScheduler(t)
	early := s.Begin() // ts=1：迟到的读者
	late := s.Begin()  // ts=2：先写并提交，使 writeTS(a)=2
	if err := s.Write(late, "a", 10); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Commit(late); err != nil {
		t.Fatal(err)
	}
	// early 的缓冲中先放一笔其它键的写，用于验证中止时缓冲被整体丢弃。
	if err := s.Write(early, "z", 1); err != nil {
		t.Fatal(err)
	}
	_, err := s.Read(early, "a")
	if r := abortReason(t, err); r != AbortReadLate {
		t.Fatalf("输入 read(ts=1, writeTS=2) 应输出读过晚, 实际 %v", r)
	}
	if info, ok := s.TxInfo(early); !ok || info.Status != TxAborted || len(info.Buffer) != 0 {
		t.Fatalf("读过晚后事务应为 aborted 且缓冲清空, 实际 %+v", info)
	}
	if _, err := s.Read(early, "z"); rejectCode(t, err) != RejectTxAborted {
		t.Fatal("中止后该事务的操作应被整体拒绝")
	}
}

// TestWriteLate 验证「写过晚」：ts < 键读时间戳时写立即中止，且不进入缓冲。
func TestWriteLate(t *testing.T) {
	s := newLoggedScheduler(t)
	earlyWriter := s.Begin() // ts=1：迟到的写者
	lateReader := s.Begin()  // ts=2：先读 a，把 readTS 抬到 2
	if _, err := s.Read(lateReader, "a"); err != nil {
		t.Fatal(err)
	}
	err := s.Write(earlyWriter, "a", 7)
	if r := abortReason(t, err); r != AbortWriteLate {
		t.Fatalf("输入 write(ts=1, readTS=2) 应输出写过晚, 实际 %v", r)
	}
	if info, _ := s.TxInfo(earlyWriter); info.Status != TxAborted || len(info.Buffer) != 0 {
		t.Fatalf("写过晚应立即中止并丢弃缓冲, 实际 %+v", info)
	}
	if ks, ok := s.KeyState("a"); !ok || ks.Value != 0 || ks.WriteTS != 0 {
		t.Fatalf("判定依据: 中止的写不得落盘, 实际 %+v", ks)
	}
}

// TestEqualTimestampNoAbort 验证 ts 恰等于读/写时间戳时用的是严格小于，不中止。
func TestEqualTimestampNoAbort(t *testing.T) {
	s := newLoggedScheduler(t)
	t1 := s.Begin() // 1
	t2 := s.Begin() // 2

	// t1 写并提交：a.writeTS = 1。
	if err := s.Write(t1, "a", 11); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Commit(t1); err != nil {
		t.Fatal(err)
	}
	// t2 读 a：ts=2 >= writeTS=1，读成功，readTS 抬到 2。
	v, err := s.Read(t2, "a")
	if err != nil || v != 11 {
		t.Fatalf("t2 应读到 11, 实际 v=%d err=%v", v, err)
	}
	// 同一事务再次写 a：ts=2 恰等于 readTS=2，不能中止。
	if err := s.Write(t2, "a", 22); err != nil {
		t.Fatalf("判定依据: ts == readTS 不是严格小于, 不应中止, 实际 %v", err)
	}
	// 提交复查：ts=2 恰等于 readTS=2，通过；安装，writeTS=2。
	res, err := s.Commit(t2)
	if err != nil {
		t.Fatalf("ts == readTS 复查不应中止, 实际 %v", err)
	}
	if len(res.Installed) != 1 || res.Installed[0] != "a" {
		t.Fatalf("应安装 a, 实际 %+v", res)
	}
	if ks, _ := s.KeyState("a"); ks.ReadTS != 2 || ks.WriteTS != 2 || ks.Value != 22 {
		t.Fatalf("时间戳/值不符合预期: %+v", ks)
	}
}

// TestIgnoredWrite 验证过时写被忽略：值与写时间戳都不变。
func TestIgnoredWrite(t *testing.T) {
	s := newLoggedScheduler(t)
	early := s.Begin() // 1
	late := s.Begin()  // 2

	// late 先写 b 并提交：b.value=20, writeTS=2。
	if err := s.Write(late, "b", 20); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Commit(late); err != nil {
		t.Fatal(err)
	}
	// early 的缓冲写早于 b 的安装；提交时 ts=1 < writeTS=2，应被忽略。
	if err := s.Write(early, "b", 10); err != nil {
		t.Fatal(err)
	}
	res, err := s.Commit(early)
	if err != nil {
		t.Fatalf("忽略不是中止, 提交应成功: %v", err)
	}
	if len(res.Ignored) != 1 || res.Ignored[0] != "b" || len(res.Installed) != 0 {
		t.Fatalf("b 应出现在 Ignored 且不安装, 实际 %+v", res)
	}
	if ks, _ := s.KeyState("b"); ks.Value != 20 || ks.WriteTS != 2 || ks.ReadTS != 0 {
		t.Fatalf("判定依据: 忽略写不得改值也不得改写时间戳, 实际 %+v", ks)
	}
}

// TestCommitRecheckZeroInstall 验证提交复查失败时整体中止、零安装。
func TestCommitRecheckZeroInstall(t *testing.T) {
	s := newLoggedScheduler(t)
	tw := s.Begin() // 1: 缓冲写 a=1,b=2
	if err := s.Write(tw, "a", 1); err != nil {
		t.Fatal(err)
	}
	if err := s.Write(tw, "b", 2); err != nil {
		t.Fatal(err)
	}
	tr := s.Begin() // 2: 提交前读 b，readTS(b)=2
	if _, err := s.Read(tr, "b"); err != nil {
		t.Fatal(err)
	}
	_, err := s.Commit(tw)
	if r := abortReason(t, err); r != AbortWriteLate {
		t.Fatalf("复查 b 时 ts=1 < readTS=2 应写过晚中止, 实际 %v", r)
	}
	if info, _ := s.TxInfo(tw); info.Status != TxAborted || len(info.Buffer) != 0 {
		t.Fatalf("中止事务缓冲应清空: %+v", info)
	}
	for _, k := range []string{"a", "b"} {
		if ks, ok := s.KeyState(k); !ok || ks.Value != 0 || ks.WriteTS != 0 {
			t.Fatalf("判定依据: 复查失败必须零安装, 键 %s 实际 %+v", k, ks)
		}
	}
}

// TestReadOwnBufferedWrite 验证缓冲命中：返回缓冲值且不触碰键记录。
func TestReadOwnBufferedWrite(t *testing.T) {
	s := newLoggedScheduler(t)
	t1 := s.Begin()
	t2 := s.Begin()
	if err := s.Write(t2, "k", 99); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Commit(t2); err != nil {
		t.Fatal(err)
	}
	// t1 的缓冲写 k=7；即便键的 writeTS=2 > ts=1，读缓冲也不触发读过晚。
	if err := s.Write(t1, "k", 7); err != nil {
		t.Fatal(err)
	}
	v, err := s.Read(t1, "k")
	if err != nil || v != 7 {
		t.Fatalf("应读到自身缓冲值 7, 实际 v=%d err=%v", v, err)
	}
	if ks, _ := s.KeyState("k"); ks.ReadTS != 0 || ks.WriteTS != 2 || ks.Value != 99 {
		t.Fatalf("判定依据: 读缓冲不得触碰任何键记录, 实际 %+v", ks)
	}
}

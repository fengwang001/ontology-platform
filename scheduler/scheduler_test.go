package scheduler

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

func abortReason(t *testing.T, err error) AbortReason {
	t.Helper()
	var ae *AbortError
	if !errors.As(err, &ae) {
		t.Fatalf("期望中止错误 *AbortError，实际 %T: %v", err, err)
	}
	return ae.Reason
}

// TestReadTooLate 覆盖「读过晚」：更早的事务读到了更晚事务已安装的值。
func TestReadTooLate(t *testing.T) {
	var log bytes.Buffer
	s := NewWithLogger(&log)

	t1, t2 := s.Begin(), s.Begin() // t1=1, t2=2
	if err := s.Write(t2, "k", 20); err != nil {
		t.Fatalf("t2 write: %v", err)
	}
	if err := s.Commit(t2); err != nil {
		t.Fatalf("t2 commit: %v", err)
	}
	_, err := s.Read(t1, "k")
	if reason := abortReason(t, err); reason != AbortReadLate {
		t.Fatalf("期望 %s，得到 %s", AbortReadLate, reason)
	}

	status, reason, err := s.Status(t1)
	if err != nil || status != "aborted" || reason != AbortReadLate {
		t.Fatalf("t1 应为 aborted/读过晚，得到 %q %q %v", status, reason, err)
	}
	if got := s.Inspect("k"); got.Value != 20 || got.WriteTS != 2 {
		t.Fatalf("k 应保持 {20, WTS=2}，得到 %+v", got)
	}
	if _, err := s.Read(t1, "k"); !errors.Is(err, ErrTxAborted) {
		t.Fatalf("中止后操作应被拒绝 ErrTxAborted，得到 %v", err)
	}
	if !strings.Contains(log.String(), "读过晚") ||
		!strings.Contains(log.String(), "输入: READ ts=1") ||
		!strings.Contains(log.String(), "ts=1 < 写时间戳 WTS=2") {
		t.Fatalf("日志缺少输入/输出/判定依据:\n%s", log.String())
	}
}

// TestWriteTooLate 覆盖「写过晚」：写时与提交复查两个时点都会检查 RTS。
func TestWriteTooLate(t *testing.T) {
	t.Run("write 时点", func(t *testing.T) {
		s := New()
		ta, tb := s.Begin(), s.Begin() // 1, 2
		if v, err := s.Read(tb, "k"); err != nil || v != 0 {
			t.Fatalf("tb 初始读: v=%d err=%v", v, err)
		}
		err := s.Write(ta, "k", 10)
		if reason := abortReason(t, err); reason != AbortWriteLate {
			t.Fatalf("期望 %s，得到 %s", AbortWriteLate, reason)
		}
		if err := s.Commit(ta); !errors.Is(err, ErrTxAborted) {
			t.Fatalf("中止后提交应被拒绝，得到 %v", err)
		}
		if got := s.Inspect("k"); got != (KeyState{ReadTS: 2, WriteTS: 0, Value: 0}) {
			t.Fatalf("中止不得安装任何写，得到 %+v", got)
		}
	})

	t.Run("commit 复查失败零安装", func(t *testing.T) {
		var log bytes.Buffer
		s := NewWithLogger(&log)
		tw := s.Begin() // 1：先缓冲两个键
		if err := s.Write(tw, "a", 1); err != nil {
			t.Fatal(err)
		}
		if err := s.Write(tw, "b", 2); err != nil {
			t.Fatal(err)
		}
		tr := s.Begin() // 2：读 a，使 RTS(a)=2
		if _, err := s.Read(tr, "a"); err != nil {
			t.Fatal(err)
		}
		err := s.Commit(tw) // 复查 a 时 ts=1 < RTS=2
		if reason := abortReason(t, err); reason != AbortWriteLate {
			t.Fatalf("期望 %s，得到 %s", AbortWriteLate, reason)
		}
		for _, k := range []string{"a", "b"} {
			if got := s.Inspect(k); got.Value != 0 || got.WriteTS != 0 {
				t.Fatalf("复查失败必须零安装，key=%s 得到 %+v", k, got)
			}
		}
		if !strings.Contains(log.String(), "复查 key=\"a\"") ||
			!strings.Contains(log.String(), "零安装") {
			t.Fatalf("日志缺少复查失败判定依据:\n%s", log.String())
		}
	})
}

// TestTSEqualsRTS 覆盖事务时间戳恰等于读时间戳时不中止（严格小于才中止）。
func TestTSEqualsRTS(t *testing.T) {
	s := New()
	t1 := s.Begin()
	if _, err := s.Read(t1, "k"); err != nil {
		t.Fatal(err)
	}
	// RTS(k)=1；ts=1 再写、再提交都不中止。
	if err := s.Write(t1, "k", 7); err != nil {
		t.Fatalf("ts==RTS 时写不应中止: %v", err)
	}
	// 另一事务先把 RTS 抬到 2，再验证提交复查时相等也合法。
	t2 := s.Begin()
	if _, err := s.Read(t2, "m"); err != nil {
		t.Fatal(err)
	}
	if err := s.Write(t2, "m", 9); err != nil {
		t.Fatalf("ts==RTS 时写不应中止: %v", err)
	}
	if err := s.Commit(t1); err != nil {
		t.Fatalf("t1 提交失败: %v", err)
	}
	if err := s.Commit(t2); err != nil {
		t.Fatalf("ts==RTS 时提交复查不应中止: %v", err)
	}
	if got := s.Inspect("m"); got.Value != 9 || got.WriteTS != 2 {
		t.Fatalf("m 应为 {9, WTS=2}，得到 %+v", got)
	}
}

// TestIgnoredWrite 覆盖过时写被忽略：值与写时间戳都不变。
func TestIgnoredWrite(t *testing.T) {
	var log bytes.Buffer
	s := NewWithLogger(&log)

	old, young := s.Begin(), s.Begin() // old=1, young=2
	// old 先缓冲 k=11（此刻 RTS(k)=0，合法）；young 提交 k=22，WTS(k)=2。
	if err := s.Write(old, "k", 11); err != nil {
		t.Fatal(err)
	}
	if err := s.Write(young, "k", 22); err != nil {
		t.Fatal(err)
	}
	if err := s.Commit(young); err != nil {
		t.Fatal(err)
	}
	// young 未读过 k，RTS(k) 仍为 0，old 的提交复查通过；
	// 安装时 ts=1 < WTS=2，该写被忽略。
	if err := s.Commit(old); err != nil {
		t.Fatalf("复查通过后过时写只应被忽略而非中止: %v", err)
	}
	got := s.Inspect("k")
	if got.Value != 22 || got.WriteTS != 2 || got.ReadTS != 0 {
		t.Fatalf("被忽略的写不得改值也不得改写时间戳，得到 %+v", got)
	}
	status, _, err := s.Status(old)
	if err != nil || status != "committed" {
		t.Fatalf("old 应已提交，得到 %q %v", status, err)
	}
	if !strings.Contains(log.String(), "该写（值 11）被忽略") {
		t.Fatalf("日志缺少忽略规则判定依据:\n%s", log.String())
	}
}

// TestRejectionOrderAndNoStateChange 覆盖拒绝顺序：
// 键为空 → 事务不存在 → 已提交 → 已中止，只报第一个；且拒绝不改状态。
func TestRejectionOrderAndNoStateChange(t *testing.T) {
	s := New()
	t1 := s.Begin()

	if _, err := s.Read(t1, ""); !errors.Is(err, ErrEmptyKey) {
		t.Fatalf("已存在事务 + 空键应先报空键，得到 %v", err)
	}
	if err := s.Write(999, "", 0); !errors.Is(err, ErrEmptyKey) {
		t.Fatalf("不存在事务 + 空键应先报空键，得到 %v", err)
	}
	if _, err := s.Read(999, "k"); !errors.Is(err, ErrTxNotFound) {
		t.Fatalf("不存在事务应报 ErrTxNotFound，得到 %v", err)
	}

	if err := s.Write(t1, "k", 1); err != nil {
		t.Fatal(err)
	}
	if err := s.Commit(t1); err != nil {
		t.Fatal(err)
	}
	if err := s.Write(t1, "k", 2); !errors.Is(err, ErrTxCommitted) {
		t.Fatalf("已提交应报 ErrTxCommitted，得到 %v", err)
	}

	t2 := s.Begin()
	t3 := s.Begin()
	if err := s.Write(t3, "x", 3); err != nil {
		t.Fatal(err)
	}
	if err := s.Commit(t3); err != nil {
		t.Fatal(err)
	}
	_, err := s.Read(t2, "x") // ts=2 < WTS=3，读过晚中止
	if reason := abortReason(t, err); reason != AbortReadLate {
		t.Fatalf("期望读过晚，得到 %v", err)
	}
	if err := s.Write(t2, "x", 9); !errors.Is(err, ErrTxAborted) {
		t.Fatalf("已中止应报 ErrTxAborted，得到 %v", err)
	}
	if _, _, err := s.Status(999); !errors.Is(err, ErrTxNotFound) {
		t.Fatalf("Status 不存在事务应报错，得到 %v", err)
	}

	// 被拒绝的操作不得改变任何状态：k 仍是 t1 提交的值，时间戳不前进之外
	// 再开的事务必须拿到连续时间戳 4。
	if got := s.Inspect("k"); got.Value != 1 || got.WriteTS != 1 {
		t.Fatalf("拒绝不得改状态，得到 %+v", got)
	}
	if next := s.Begin(); next != 4 {
		t.Fatalf("时间戳应连续递增为 4，得到 %d", next)
	}
}

// TestReadOwnWrite 覆盖读私有缓冲：不触碰键的读时间戳。
func TestReadOwnWrite(t *testing.T) {
	s := New()
	t1, t2 := s.Begin(), s.Begin()
	if err := s.Write(t1, "k", 55); err != nil {
		t.Fatal(err)
	}
	v, err := s.Read(t1, "k")
	if err != nil || v != 55 {
		t.Fatalf("应读到自己的缓冲值 55，得到 v=%d err=%v", v, err)
	}
	// t2 的读仍应看到初值 0，且 RTS(k) 不受 t1 缓冲读影响。
	v, err = s.Read(t2, "k")
	if err != nil || v != 0 {
		t.Fatalf("缓冲未安装，t2 应读到 0，得到 v=%d err=%v", v, err)
	}
	err = s.Commit(t1)
	if reason := abortReason(t, err); reason != AbortWriteLate {
		t.Fatalf("t1 提交复查应因 ts=1 < RTS=2 中止，得到 %v", err)
	}
	if got := s.Inspect("k"); got.Value != 0 || got.WriteTS != 0 {
		t.Fatalf("复查失败零安装，得到 %+v", got)
	}
}

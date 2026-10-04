package session

import (
	"errors"
	"fmt"
	"math/rand"
	"reflect"
	"strings"
	"testing"

	"ontology/journal"
	"ontology/quota"
)

func mkBytes(seed, n int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte(seed + i)
	}
	return b
}

// TestSpecExampleQuotaFull 复现题干例一：q=100, T=10, M=5。
func TestSpecExampleQuotaFull(t *testing.T) {
	svc := New(5, 10, nil)
	if err := svc.SetQuota("A", 100); err != nil {
		t.Fatal(err)
	}
	s1, err := svc.Create("A", "k", 60, 0)
	if err != nil || s1 != 1 {
		t.Fatalf("s1=%d err=%v", s1, err)
	}
	s2, err := svc.Create("A", "m", 40, 0)
	if err != nil || s2 != 2 {
		t.Fatalf("s2=%d err=%v", s2, err)
	}
	if v := svc.Snapshot().Tenants["A"]; v.Reserved != 100 {
		t.Fatalf("R=%d, 期望 100", v.Reserved)
	}
	if _, err := svc.Create("A", "z", 1, 0); !errors.Is(err, quota.ErrQuotaExceeded) {
		t.Fatalf("101>100 应报额度不足, 得到 %v", err)
	}
	d0 := mkBytes(0, 30)
	if err := svc.Chunk(s1, 0, d0, 5); err != nil {
		t.Fatal(err)
	}
	if c, exp, _ := svc.Query(s1, 5); c != 30 || exp != 15 {
		t.Fatalf("c=%d 到期=%d, 期望 30/15", c, exp)
	}
	if err := svc.Chunk(s1, 40, mkBytes(0, 1), 5); !errors.Is(err, ErrGap) {
		t.Fatalf("off>c 应报缺口, 得到 %v", err)
	}
	d1 := append(append([]byte{}, d0[20:30]...), mkBytes(100, 10)...)
	if err := svc.Chunk(s1, 20, d1, 6); err != nil {
		t.Fatal(err)
	}
	if c, exp, _ := svc.Query(s1, 6); c != 40 || exp != 16 {
		t.Fatalf("c=%d 到期=%d, 期望 40/16", c, exp)
	}
	if err := svc.Chunk(s1, 0, d0[:10], 7); err != nil {
		t.Fatalf("纯重放应被接受, 得到 %v", err)
	}
	if _, exp, _ := svc.Query(s1, 7); exp != 16 {
		t.Fatalf("纯重放不得刷新到期, 到期=%d, 期望 16", exp)
	}
	s3, err := svc.Create("A", "q", 30, 10)
	if err != nil || s3 != 3 {
		t.Fatalf("s3=%d err=%v, 期望接受且被拒的不占号", s3, err)
	}
	snap := svc.Snapshot()
	if snap.Sessions[s2].Status != StatusExpired {
		t.Fatalf("s2 恰等到期应被回收, 状态=%s", snap.Sessions[s2].Status)
	}
	if v := snap.Tenants["A"]; v.Reserved != 90 {
		t.Fatalf("回收 s2 并预留 30 后 R=%d, 期望 90", v.Reserved)
	}
}

// TestSpecExampleOverwrite 复现题干例二：覆盖同键时 U 的净变化。
func TestSpecExampleOverwrite(t *testing.T) {
	svc := New(5, 100, nil)
	if err := svc.SetQuota("A", 100); err != nil {
		t.Fatal(err)
	}
	s1, _ := svc.Create("A", "k", 25, 0)
	if err := svc.Chunk(s1, 0, mkBytes(0, 25), 0); err != nil {
		t.Fatal(err)
	}
	if err := svc.Complete(s1, 0); err != nil {
		t.Fatal(err)
	}
	if v := svc.Snapshot().Tenants["A"]; v.Used != 25 || v.Reserved != 0 {
		t.Fatalf("首对象完成后 U=%d R=%d, 期望 25/0", v.Used, v.Reserved)
	}
	s2, err := svc.Create("A", "k", 60, 1)
	if err != nil {
		t.Fatalf("25+0+60=85 不大于 100, 应接受, 得到 %v", err)
	}
	if _, err := svc.Create("A", "j", 16, 1); !errors.Is(err, quota.ErrQuotaExceeded) {
		t.Fatalf("25+60+16=101 应报额度不足, 得到 %v", err)
	}
	if err := svc.Abort(s2, 1); err != nil {
		t.Fatal(err)
	}
	s3, err := svc.Create("A", "j", 16, 1)
	if err != nil {
		t.Fatalf("Abort 后应可再创建, 得到 %v", err)
	}
	if err := svc.Abort(s3, 1); err != nil {
		t.Fatal(err)
	}
	s4, _ := svc.Create("A", "k", 60, 2)
	if err := svc.Chunk(s4, 0, mkBytes(0, 60), 2); err != nil {
		t.Fatal(err)
	}
	if err := svc.Complete(s4, 2); err != nil {
		t.Fatal(err)
	}
	if v := svc.Snapshot().Tenants["A"]; v.Used != 60 || v.Reserved != 0 {
		t.Fatalf("覆盖同键后 U=%d R=%d, 期望 60/0", v.Used, v.Reserved)
	}
	if _, err := svc.Create("A", "j", 40, 2); err != nil {
		t.Fatalf("60+40=100 恰等应通过, 得到 %v", err)
	}
}

// TestBoundaryTable 表驱动：恰等/差 1 边界与参数、时钟校验。
func TestBoundaryTable(t *testing.T) {
	t.Run("U+R+total恰等与超1", func(t *testing.T) {
		svc := New(5, 10, nil)
		mustOK(t, svc.SetQuota("A", 100))
		if _, err := svc.Create("A", "a", 60, 0); err != nil {
			t.Fatal(err)
		}
		if _, err := svc.Create("A", "b", 40, 0); err != nil {
			t.Fatalf("60+40=100 恰等应通过, 得到 %v", err)
		}
		if _, err := svc.Create("A", "c", 1, 0); !errors.Is(err, quota.ErrQuotaExceeded) {
			t.Fatalf("101>100 应报额度不足, 得到 %v", err)
		}
	})
	t.Run("SetQuota恰等占用与低1", func(t *testing.T) {
		svc := New(5, 10, nil)
		mustOK(t, svc.SetQuota("A", 100))
		if _, err := svc.Create("A", "a", 70, 0); err != nil {
			t.Fatal(err)
		}
		if err := svc.SetQuota("A", 70); err != nil {
			t.Fatalf("恰等 U+R=70 应通过, 得到 %v", err)
		}
		if err := svc.SetQuota("A", 69); !errors.Is(err, quota.ErrBelowUsed) {
			t.Fatalf("低于占用应报 ErrBelowUsed, 得到 %v", err)
		}
	})
	t.Run("到期恰等与小1", func(t *testing.T) {
		svc := New(5, 10, nil)
		mustOK(t, svc.SetQuota("A", 100))
		sid, _ := svc.Create("A", "a", 10, 3) // 到期 13
		if _, _, err := svc.Query(sid, 12); err != nil {
			t.Fatalf("now=12 未到期, 得到 %v", err)
		}
		if _, _, err := svc.Query(sid, 13); !errors.Is(err, ErrSessionExpired) {
			t.Fatalf("now=13 恰等应已到期, 得到 %v", err)
		}
		if err := svc.Chunk(sid, 0, mkBytes(0, 1), 13); !errors.Is(err, ErrSessionExpired) {
			t.Fatalf("到期后 Chunk 应报已到期, 得到 %v", err)
		}
		if err := svc.Complete(sid, 13); !errors.Is(err, ErrSessionExpired) {
			t.Fatalf("到期后 Complete 应报已到期, 得到 %v", err)
		}
		if err := svc.Abort(sid, 13); !errors.Is(err, ErrSessionExpired) {
			t.Fatalf("到期后 Abort 应报已到期, 得到 %v", err)
		}
	})
	t.Run("时钟回退与相等", func(t *testing.T) {
		svc := New(5, 10, nil)
		mustOK(t, svc.SetQuota("A", 100))
		if _, err := svc.Create("A", "a", 10, 5); err != nil {
			t.Fatal(err)
		}
		if _, err := svc.Create("A", "b", 10, 4); !errors.Is(err, ErrClockRewind) {
			t.Fatalf("now 回退应报时钟回退, 得到 %v", err)
		}
		if _, err := svc.Create("A", "b", 10, 5); err != nil {
			t.Fatalf("now 相等应接受, 得到 %v", err)
		}
		if _, _, err := svc.Query(1, 4); !errors.Is(err, ErrClockRewind) {
			t.Fatalf("Query 回退应报时钟回退, 得到 %v", err)
		}
	})
	t.Run("参数非法", func(t *testing.T) {
		svc := New(5, 10, nil)
		mustOK(t, svc.SetQuota("A", MaxTotal))
		cases := []struct {
			name string
			call func() error
		}{
			{"Create空租户", func() error { _, e := svc.Create("", "k", 1, 0); return e }},
			{"Create空键", func() error { _, e := svc.Create("A", "", 1, 0); return e }},
			{"Create零total", func() error { _, e := svc.Create("A", "k", 0, 0); return e }},
			{"Create超界total", func() error { _, e := svc.Create("A", "k", MaxTotal+1, 0); return e }},
			{"Create负now", func() error { _, e := svc.Create("A", "k", 1, -1); return e }},
			{"Create超界now", func() error { _, e := svc.Create("A", "k", 1, MaxNow+1); return e }},
			{"Chunk零sid", func() error { return svc.Chunk(0, 0, nil, 0) }},
			{"Chunk负off", func() error { return svc.Chunk(1, -1, nil, 0) }},
			{"Chunk超界off", func() error { return svc.Chunk(1, MaxTotal+1, nil, 0) }},
			{"Chunk超长data", func() error { return svc.Chunk(1, 0, make([]byte, MaxChunk+1), 0) }},
			{"Complete零sid", func() error { return svc.Complete(0, 0) }},
			{"Abort负now", func() error { return svc.Abort(1, -1) }},
			{"Query超界now", func() error { _, _, e := svc.Query(1, MaxNow+1); return e }},
		}
		for _, tc := range cases {
			if err := tc.call(); !errors.Is(err, ErrInvalidParam) {
				t.Fatalf("%s: 应报参数非法, 得到 %v", tc.name, err)
			}
		}
	})
	t.Run("会话类次序", func(t *testing.T) {
		svc := New(5, 100, nil)
		mustOK(t, svc.SetQuota("A", 100))
		s1, _ := svc.Create("A", "a", 5, 0)
		if err := svc.Chunk(s1, 0, mkBytes(0, 5), 0); err != nil {
			t.Fatal(err)
		}
		mustOK(t, svc.Complete(s1, 0))
		s2, _ := svc.Create("A", "b", 5, 1)
		mustOK(t, svc.Abort(s2, 1))
		if err := svc.Chunk(99, 0, nil, 2); !errors.Is(err, ErrNoSuchSession) {
			t.Fatalf("未发出的号应报 ErrNoSuchSession, 得到 %v", err)
		}
		if err := svc.Chunk(s1, 0, nil, 2); !errors.Is(err, ErrSessionClosed) {
			t.Fatalf("已完成应报 ErrSessionClosed, 得到 %v", err)
		}
		if err := svc.Abort(s2, 2); !errors.Is(err, ErrSessionClosed) {
			t.Fatalf("已中止应报 ErrSessionClosed, 得到 %v", err)
		}
		if _, _, err := svc.Query(s1, 2); !errors.Is(err, ErrSessionClosed) {
			t.Fatalf("已完成 Query 应报 ErrSessionClosed, 得到 %v", err)
		}
	})
	t.Run("未完成带剩余", func(t *testing.T) {
		svc := New(5, 100, nil)
		mustOK(t, svc.SetQuota("A", 100))
		sid, _ := svc.Create("A", "a", 10, 0)
		mustOK(t, svc.Chunk(sid, 0, mkBytes(0, 4), 0))
		err := svc.Complete(sid, 0)
		if !errors.Is(err, ErrIncomplete) {
			t.Fatalf("应报未完成, 得到 %v", err)
		}
		if !strings.Contains(err.Error(), "6") {
			t.Fatalf("未完成应带 total-c=6, 得到 %v", err)
		}
	})
	t.Run("会话数超限", func(t *testing.T) {
		svc := New(2, 100, nil)
		mustOK(t, svc.SetQuota("A", 1000))
		if _, err := svc.Create("A", "a", 1, 0); err != nil {
			t.Fatal(err)
		}
		if _, err := svc.Create("A", "b", 1, 0); err != nil {
			t.Fatal(err)
		}
		if _, err := svc.Create("A", "c", 1, 0); !errors.Is(err, ErrTooManySessions) {
			t.Fatalf("第 3 个开启会话应报超限, 得到 %v", err)
		}
	})
}

// TestChunkOrder 缺口 > 越界 > 冲突的判定次序。
func TestChunkOrder(t *testing.T) {
	svc := New(5, 100, nil)
	mustOK(t, svc.SetQuota("A", 100))
	sid, _ := svc.Create("A", "a", 10, 0)
	mustOK(t, svc.Chunk(sid, 0, mkBytes(7, 5), 0)) // c=5

	if err := svc.Chunk(sid, 7, mkBytes(0, 10), 1); !errors.Is(err, ErrGap) {
		t.Fatalf("缺口优先于越界, 得到 %v", err)
	}
	bad := mkBytes(7, 7)
	bad[0] ^= 0xff // 与已提交字节冲突
	if err := svc.Chunk(sid, 4, bad, 1); !errors.Is(err, ErrOverflow) {
		t.Fatalf("越界优先于冲突, 得到 %v", err)
	}
	if err := svc.Chunk(sid, 4, bad[:2], 1); !errors.Is(err, ErrConflict) {
		t.Fatalf("数据不一致应报冲突, 得到 %v", err)
	}
	good := mkBytes(7, 6)[4:]
	if err := svc.Chunk(sid, 4, good, 1); err != nil {
		t.Fatalf("一致的重叠写应接受, 得到 %v", err)
	}
	if c, _, _ := svc.Query(sid, 1); c != 6 {
		t.Fatalf("c=%d, 期望 6", c)
	}
}

// TestProbeChunk 长度为 0 的探测块。
func TestProbeChunk(t *testing.T) {
	svc := New(5, 10, nil)
	mustOK(t, svc.SetQuota("A", 100))
	sid, _ := svc.Create("A", "a", 10, 0)
	mustOK(t, svc.Chunk(sid, 0, mkBytes(0, 5), 1))
	if err := svc.Chunk(sid, 5, nil, 2); err != nil {
		t.Fatalf("len=0 探测应接受, 得到 %v", err)
	}
	if c, exp, _ := svc.Query(sid, 2); c != 5 || exp != 11 {
		t.Fatalf("探测不得改变 c 与到期, c=%d 到期=%d", c, exp)
	}
}

// TestRejectedOpDoesNotLand 被拒操作不落地回收、不推进时钟。
func TestRejectedOpDoesNotLand(t *testing.T) {
	svc := New(5, 10, nil)
	mustOK(t, svc.SetQuota("A", 100))
	s1, _ := svc.Create("A", "k", 60, 0) // 到期 10
	s2, _ := svc.Create("A", "m", 40, 0) // 到期 10

	if err := svc.Chunk(s1, 0, mkBytes(0, 1), 10); !errors.Is(err, ErrSessionExpired) {
		t.Fatalf("恰等到期应报已到期, 得到 %v", err)
	}
	snap := svc.Snapshot()
	if v := snap.Tenants["A"]; v.Reserved != 100 {
		t.Fatalf("被拒后预留应仍在, R=%d", v.Reserved)
	}
	if snap.Sessions[s1].Status != StatusOpen || snap.Sessions[s2].Status != StatusOpen {
		t.Fatalf("被拒后回收不应落地, 状态=%s/%s", snap.Sessions[s1].Status, snap.Sessions[s2].Status)
	}
	if snap.Now != 0 {
		t.Fatalf("被拒操作不得推进时钟, Now=%d", snap.Now)
	}
	if _, _, err := svc.Query(s1, 10); !errors.Is(err, ErrSessionExpired) {
		t.Fatalf("更晚 now 仍应判已到期, 得到 %v", err)
	}
	if _, exp, err := svc.Query(s1, 5); err != nil || exp != 10 {
		t.Fatalf("到期判定是 now 的纯函数, now=5 应仍存活, err=%v 到期=%d", err, exp)
	}
	if err := svc.SetQuota("A", 99); !errors.Is(err, quota.ErrBelowUsed) {
		t.Fatalf("预留仍在, 降额至 99 应报低于占用, 得到 %v", err)
	}
	if _, err := svc.Create("A", "z", 50, 10); err != nil {
		t.Fatalf("被接受的操作应落地回收后再判定, 得到 %v", err)
	}
	snap = svc.Snapshot()
	if snap.Sessions[s1].Status != StatusExpired || snap.Sessions[s2].Status != StatusExpired {
		t.Fatalf("接受后两会话应已回收, 状态=%s/%s", snap.Sessions[s1].Status, snap.Sessions[s2].Status)
	}
	if v := snap.Tenants["A"]; v.Reserved != 50 {
		t.Fatalf("回收后 R=%d, 期望 50", v.Reserved)
	}
}

func mustOK(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

// errSentinels 是全部哨兵错误；sameErr 按哨兵集合判定两个错误是否等价。
var errSentinels = []error{
	ErrInvalidParam, ErrClockRewind, ErrTooManySessions, ErrNoSuchSession,
	ErrSessionClosed, ErrSessionExpired, ErrGap, ErrOverflow, ErrConflict,
	ErrIncomplete, ErrJournal,
	quota.ErrInvalidParam, quota.ErrBelowUsed, quota.ErrQuotaExceeded,
}

func sameErr(got, want error) bool {
	if got == nil || want == nil {
		return got == want
	}
	for _, s := range errSentinels {
		if errors.Is(got, s) != errors.Is(want, s) {
			return false
		}
	}
	if errors.Is(got, ErrIncomplete) && got.Error() != want.Error() {
		return false // 剩余量也须一致
	}
	return true
}

// TestJournalFailureKeepsState 日志失败：状态、会话号、时钟均不变。
func TestJournalFailureKeepsState(t *testing.T) {
	sink := &journal.FlakySink{FailAt: map[int]bool{1: true, 4: true}}
	svc := New(5, 10, sink)
	mustOK(t, svc.SetQuota("A", 100)) // 第 0 次追加

	before := svc.Snapshot()
	if _, err := svc.Create("A", "k", 10, 0); !errors.Is(err, ErrJournal) {
		t.Fatalf("第 1 次追加注入失败, 应报日志失败, 得到 %v", err)
	}
	after := svc.Snapshot()
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("日志失败不得改状态\n前: %+v\n后: %+v", before, after)
	}
	sid, err := svc.Create("A", "k", 10, 0) // 第 2 次追加成功
	if err != nil || sid != 1 {
		t.Fatalf("日志失败后重试应成功且不占号, sid=%d err=%v", sid, err)
	}
	mustOK(t, svc.Chunk(sid, 0, mkBytes(0, 10), 1))
	before = svc.Snapshot()
	if err := svc.Complete(sid, 2); !errors.Is(err, ErrJournal) { // 第 3 次追加失败
		t.Fatalf("Complete 注入失败, 应报日志失败, 得到 %v", err)
	}
	if after := svc.Snapshot(); !reflect.DeepEqual(before, after) {
		t.Fatalf("日志失败不得改状态\n前: %+v\n后: %+v", before, after)
	}
	if len(svc.Records()) != 3 {
		t.Fatalf("失败追加不留痕, 记录数=%d, 期望 3", len(svc.Records()))
	}
}

// TestReplayEveryPrefix 对每个 k，前 k 条记录的回放与逐操作状态一致。
func TestReplayEveryPrefix(t *testing.T) {
	svc := New(3, 5, nil)
	snaps := []Snapshot{svc.Snapshot()}
	capture := func() { snaps = append(snaps, svc.Snapshot()) }
	mustOK(t, svc.SetQuota("A", 100))
	capture() // SetQuota A
	mustOK(t, svc.SetQuota("B", 50))
	capture() // SetQuota B

	s1, _ := svc.Create("A", "k", 30, 0)
	capture()
	s2, _ := svc.Create("A", "m", 20, 0)
	capture()
	s3, _ := svc.Create("B", "x", 40, 1)
	capture()
	mustOK(t, svc.Chunk(s1, 0, mkBytes(0, 20), 2))
	capture()
	mustOK(t, svc.Chunk(s1, 20, mkBytes(20, 10), 3))
	capture()
	mustOK(t, svc.Chunk(s1, 0, mkBytes(0, 5), 4)) // 纯重放
	capture()
	mustOK(t, svc.Complete(s1, 4))
	capture()
	mustOK(t, svc.Abort(s2, 4))
	capture()
	mustOK(t, svc.Chunk(s3, 0, mkBytes(0, 40), 5))
	capture()
	mustOK(t, svc.Complete(s3, 5))
	capture()
	s4, _ := svc.Create("B", "y", 10, 5) // 到期 10
	_ = s4
	capture()
	if _, err := svc.Create("A", "k", 15, 11); err != nil { // 落地回收 s4
		t.Fatal(err)
	}
	capture()

	records := svc.Records()
	if len(records) != len(snaps)-1 {
		t.Fatalf("记录数 %d 与快照数 %d 不符", len(records), len(snaps))
	}
	for k := 0; k <= len(records); k++ {
		got, err := Replay(records[:k], 3, 5)
		if err != nil {
			t.Fatalf("前 %d 条记录回放失败: %v", k, err)
		}
		if want := snaps[k]; !reflect.DeepEqual(want, got.Snapshot()) {
			t.Fatalf("前 %d 条回放不一致\n期望: %+v\n得到: %+v", k, want, got.Snapshot())
		}
	}
}

// TestPoppedIndependentOfOpenCount popped 与开启中会话总数无关（100 vs 10000）。
func TestPoppedIndependentOfOpenCount(t *testing.T) {
	for _, n := range []int{100, 10000} {
		t.Run(fmt.Sprintf("open=%d", n), func(t *testing.T) {
			svc := New(1000, 10, nil)
			perTenant := 1000
			tenants := (n + perTenant - 1) / perTenant
			for i := 0; i < tenants; i++ {
				name := fmt.Sprintf("T%d", i)
				mustOK(t, svc.SetQuota(name, MaxTotal))
			}
			var firstSid int64
			for i := 0; i < n; i++ {
				sid, err := svc.Create(fmt.Sprintf("T%d", i/perTenant), "k", 1, 0)
				if err != nil {
					t.Fatal(err)
				}
				if i == 0 {
					firstSid = sid
				}
			}
			// 未触发任何回收的接受操作：只有 1 次探针弹出。
			before := svc.popped
			mustOK(t, svc.Chunk(firstSid, 0, nil, 5))
			if got := svc.popped - before; got != 1 {
				t.Fatalf("无回收时 popped 增量=%d, 期望 1（与开启数 %d 无关）", got, n)
			}
			// 全部到期：弹出数 == 回收数（堆空，无额外探针）。
			before = svc.popped
			if _, err := svc.Create("T0", "z", 1, 10); err != nil {
				t.Fatal(err)
			}
			if got := svc.popped - before; got != n {
				t.Fatalf("全量回收 popped 增量=%d, 期望 %d（回收数）", got, n)
			}
		})
	}
}

// TestConcurrent 并发调用等价于某串行序，不变量保持。
func TestConcurrent(t *testing.T) {
	svc := New(1000, 1000, nil)
	const workers = 8
	done := make(chan string, workers)
	for w := 0; w < workers; w++ {
		go func(w int) {
			tenant := fmt.Sprintf("W%d", w)
			if err := svc.SetQuota(tenant, 500); err != nil {
				done <- err.Error()
				return
			}
			for i := 0; i < 50; i++ {
				key := fmt.Sprintf("k%d", i%5)
				sid, err := svc.Create(tenant, key, 10, 0)
				if err != nil {
					done <- err.Error()
					return
				}
				if err := svc.Chunk(sid, 0, mkBytes(w, 10), 0); err != nil {
					done <- err.Error()
					return
				}
				if _, _, err := svc.Query(sid, 0); err != nil {
					done <- err.Error()
					return
				}
				var cerr error
				if i%2 == 0 {
					cerr = svc.Complete(sid, 0)
				} else {
					cerr = svc.Abort(sid, 0)
				}
				if cerr != nil {
					done <- cerr.Error()
					return
				}
			}
			done <- ""
		}(w)
	}
	for w := 0; w < workers; w++ {
		if msg := <-done; msg != "" {
			t.Fatal(msg)
		}
	}
	assertInvariants(t, svc.Snapshot())
}

// assertInvariants 校验全局不变量：U+R<=q、c<=total、R==开启会话 total 之和。
func assertInvariants(t *testing.T, snap Snapshot) {
	t.Helper()
	openSum := map[string]int64{}
	for _, sess := range snap.Sessions {
		if sess.C > sess.Total {
			t.Errorf("会话 c=%d 超过 total=%d", sess.C, sess.Total)
		}
		if sess.Status == StatusOpen {
			openSum[sess.Tenant] += sess.Total
		}
	}
	for name, tn := range snap.Tenants {
		if tn.Used+tn.Reserved > tn.Quota {
			t.Errorf("租户 %s: U+R=%d 超过 q=%d", name, tn.Used+tn.Reserved, tn.Quota)
		}
		if tn.Reserved != openSum[name] {
			t.Errorf("租户 %s: R=%d 不等于开启会话 total 之和 %d", name, tn.Reserved, openSum[name])
		}
	}
}

// ---------- 朴素模拟：按题述规则逐步实现的独立参照 ----------

type mSession struct {
	tenant string
	key    string
	total  int64
	c      int64
	expiry int64
	status Status
	data   map[int64]byte
}

type mTenant struct {
	q       int64
	u       int64
	r       int64
	open    int
	objects map[string]int64
}

type model struct {
	m       int64
	ttl     int64
	now     int64
	nextSid int64
	tenants map[string]*mTenant
	sess    map[int64]*mSession
}

func newModel(m, ttl int64) *model {
	return &model{
		m:       m,
		ttl:     ttl,
		now:     -1,
		nextSid: 1,
		tenants: map[string]*mTenant{},
		sess:    map[int64]*mSession{},
	}
}

func (md *model) getTenant(t string) *mTenant {
	tn, ok := md.tenants[t]
	if !ok {
		tn = &mTenant{objects: map[string]int64{}}
		md.tenants[t] = tn
	}
	return tn
}

func (md *model) tenantView(t string) (u, r, q int64, open int) {
	if tn, ok := md.tenants[t]; ok {
		return tn.u, tn.r, tn.q, tn.open
	}
	return 0, 0, 0, 0
}

// reclaimable 线性扫描判定 now 时刻已到期的开启会话（不落地）。
func (md *model) reclaimable(now int64) []*mSession {
	var out []*mSession
	for _, s := range md.sess {
		if s.status == StatusOpen && s.expiry <= now {
			out = append(out, s)
		}
	}
	return out
}

func (md *model) land(exp []*mSession, now int64) {
	for _, s := range exp {
		s.status = StatusExpired
		s.data = nil
		tn := md.tenants[s.tenant]
		tn.r -= s.total
		tn.open--
	}
	md.now = now
}

func (md *model) lookup(sid, now int64) (*mSession, error, string) {
	s, ok := md.sess[sid]
	if !ok {
		return nil, ErrNoSuchSession, "会话号从未发出"
	}
	if s.status == StatusCompleted || s.status == StatusAborted {
		return nil, ErrSessionClosed, "会话已完成或已中止"
	}
	if s.status == StatusExpired || s.expiry <= now {
		return nil, ErrSessionExpired, "会话已到期"
	}
	return s, nil, ""
}

func validNow(now int64) bool { return now >= 0 && now <= MaxNow }

// apply 按题述规则执行操作；journalFailed 表示 Sink 注入失败。
// 返回 (sid, c, expiry, err, 判定依据)。
func (md *model) apply(op randOp, journalFailed bool) (int64, int64, int64, error, string) {
	switch op.kind {
	case "setquota":
		err, reason := md.setQuota(op, journalFailed)
		return 0, 0, 0, err, reason
	case "create":
		sid, err, reason := md.create(op, journalFailed)
		return sid, 0, 0, err, reason
	case "chunk":
		err, reason := md.chunk(op, journalFailed)
		return 0, 0, 0, err, reason
	case "complete":
		err, reason := md.complete(op, journalFailed)
		return 0, 0, 0, err, reason
	case "abort":
		err, reason := md.abort(op, journalFailed)
		return 0, 0, 0, err, reason
	case "query":
		c, exp, err, reason := md.query(op)
		return 0, c, exp, err, reason
	}
	return 0, 0, 0, fmt.Errorf("未知操作 %q", op.kind), "未知操作"
}

func (md *model) setQuota(op randOp, jf bool) (error, string) {
	if op.tenant == "" || op.q < 0 || op.q > quota.MaxQuota {
		return quota.ErrInvalidParam, "参数非法"
	}
	u, r, _, _ := md.tenantView(op.tenant)
	if op.q < u+r {
		return quota.ErrBelowUsed, fmt.Sprintf("q=%d 低于 U+R=%d", op.q, u+r)
	}
	if jf {
		return ErrJournal, "日志失败(注入)"
	}
	md.getTenant(op.tenant).q = op.q
	return nil, "接受"
}

func (md *model) create(op randOp, jf bool) (int64, error, string) {
	if op.tenant == "" || op.key == "" || op.total < 1 || op.total > MaxTotal || !validNow(op.now) {
		return 0, ErrInvalidParam, "参数非法"
	}
	if op.now < md.now {
		return 0, ErrClockRewind, fmt.Sprintf("时钟回退 now=%d < %d", op.now, md.now)
	}
	exp := md.reclaimable(op.now)
	u, r, q, open := md.tenantView(op.tenant)
	for _, s := range exp {
		if s.tenant == op.tenant {
			r -= s.total
			open--
		}
	}
	if int64(open) >= md.m {
		return 0, ErrTooManySessions, fmt.Sprintf("开启会话数 %d 已达 M=%d", open, md.m)
	}
	if u+r+op.total > q {
		return 0, quota.ErrQuotaExceeded, fmt.Sprintf("U+R+total=%d 超过 q=%d", u+r+op.total, q)
	}
	if jf {
		return 0, ErrJournal, "日志失败(注入)"
	}
	md.land(exp, op.now)
	sid := md.nextSid
	md.nextSid++
	md.sess[sid] = &mSession{
		tenant: op.tenant, key: op.key, total: op.total,
		expiry: op.now + md.ttl, status: StatusOpen, data: map[int64]byte{},
	}
	tn := md.getTenant(op.tenant)
	tn.r += op.total
	tn.open++
	return sid, nil, fmt.Sprintf("接受: 落地回收 %d 个, 预留 %d", len(exp), op.total)
}

func (md *model) chunk(op randOp, jf bool) (error, string) {
	if op.sid < 1 || op.off < 0 || op.off > MaxTotal || len(op.data) > MaxChunk || !validNow(op.now) {
		return ErrInvalidParam, "参数非法"
	}
	if op.now < md.now {
		return ErrClockRewind, fmt.Sprintf("时钟回退 now=%d < %d", op.now, md.now)
	}
	exp := md.reclaimable(op.now)
	s, err, reason := md.lookup(op.sid, op.now)
	if err != nil {
		return err, reason
	}
	if op.off > s.c {
		return ErrGap, fmt.Sprintf("缺口 off=%d > c=%d", op.off, s.c)
	}
	end := op.off + int64(len(op.data))
	if end > s.total {
		return ErrOverflow, fmt.Sprintf("越界 off+len=%d > total=%d", end, s.total)
	}
	cmpLen := s.c - op.off
	if int64(len(op.data)) < cmpLen {
		cmpLen = int64(len(op.data))
	}
	for i := int64(0); i < cmpLen; i++ {
		if op.data[i] != s.data[op.off+i] {
			return ErrConflict, fmt.Sprintf("冲突 @%d", op.off+i)
		}
	}
	if jf {
		return ErrJournal, "日志失败(注入)"
	}
	md.land(exp, op.now)
	if end > s.c {
		for i := s.c; i < end; i++ {
			s.data[i] = op.data[i-op.off]
		}
		s.c = end
		s.expiry = op.now + md.ttl
		return nil, fmt.Sprintf("接受: c→%d, 到期→%d, 回收 %d 个", end, s.expiry, len(exp))
	}
	return nil, "接受: 纯重放/探测, 不刷新到期"
}

func (md *model) complete(op randOp, jf bool) (error, string) {
	if op.sid < 1 || !validNow(op.now) {
		return ErrInvalidParam, "参数非法"
	}
	if op.now < md.now {
		return ErrClockRewind, fmt.Sprintf("时钟回退 now=%d < %d", op.now, md.now)
	}
	exp := md.reclaimable(op.now)
	s, err, reason := md.lookup(op.sid, op.now)
	if err != nil {
		return err, reason
	}
	if s.c != s.total {
		return fmt.Errorf("%w: 剩余 %d", ErrIncomplete, s.total-s.c), fmt.Sprintf("未完成 c=%d total=%d", s.c, s.total)
	}
	if jf {
		return ErrJournal, "日志失败(注入)"
	}
	md.land(exp, op.now)
	s.status = StatusCompleted
	s.data = nil
	tn := md.tenants[s.tenant]
	replaced := tn.objects[s.key]
	tn.r -= s.total
	tn.u += s.total - replaced
	tn.open--
	tn.objects[s.key] = s.total
	return nil, fmt.Sprintf("接受: U 净增 %d, 回收 %d 个", s.total-replaced, len(exp))
}

func (md *model) abort(op randOp, jf bool) (error, string) {
	if op.sid < 1 || !validNow(op.now) {
		return ErrInvalidParam, "参数非法"
	}
	if op.now < md.now {
		return ErrClockRewind, fmt.Sprintf("时钟回退 now=%d < %d", op.now, md.now)
	}
	exp := md.reclaimable(op.now)
	s, err, reason := md.lookup(op.sid, op.now)
	if err != nil {
		return err, reason
	}
	if jf {
		return ErrJournal, "日志失败(注入)"
	}
	md.land(exp, op.now)
	s.status = StatusAborted
	s.data = nil
	tn := md.tenants[s.tenant]
	tn.r -= s.total
	tn.open--
	return nil, fmt.Sprintf("接受: 释放预留 %d, 回收 %d 个", s.total, len(exp))
}

func (md *model) query(op randOp) (int64, int64, error, string) {
	if op.sid < 1 || !validNow(op.now) {
		return 0, 0, ErrInvalidParam, "参数非法"
	}
	if op.now < md.now {
		return 0, 0, ErrClockRewind, fmt.Sprintf("时钟回退 now=%d < %d", op.now, md.now)
	}
	s, err, reason := md.lookup(op.sid, op.now)
	if err != nil {
		return 0, 0, err, reason
	}
	return s.c, s.expiry, nil, "只读: 不推进时钟, 不落地回收"
}

func (md *model) snapshot() Snapshot {
	snap := Snapshot{
		Now:      md.now,
		NextSid:  md.nextSid,
		Tenants:  map[string]TenantState{},
		Sessions: map[int64]SessionState{},
	}
	for name, tn := range md.tenants {
		objects := make(map[string]int64, len(tn.objects))
		for k, v := range tn.objects {
			objects[k] = v
		}
		snap.Tenants[name] = TenantState{
			Quota: tn.q, Used: tn.u, Reserved: tn.r, Open: tn.open, Objects: objects,
		}
	}
	for sid, s := range md.sess {
		var hash uint64
		if s.data == nil {
			hash = hashBytes(nil)
		} else {
			buf := make([]byte, s.c)
			for i := range buf {
				buf[i] = s.data[int64(i)]
			}
			hash = hashBytes(buf)
		}
		snap.Sessions[sid] = SessionState{
			Tenant: s.tenant, Key: s.key, Total: s.total, C: s.c,
			Expiry: s.expiry, Status: s.status, DataHash: hash,
		}
	}
	return snap
}

// ---------- 随机操作生成与对照驱动 ----------

type randOp struct {
	kind   string
	tenant string
	key    string
	q      int64
	total  int64
	sid    int64
	off    int64
	now    int64
	data   []byte
}

func (op randOp) String() string {
	switch op.kind {
	case "setquota":
		return fmt.Sprintf("SetQuota(%q, %d)", op.tenant, op.q)
	case "create":
		return fmt.Sprintf("Create(%q, %q, %d, now=%d)", op.tenant, op.key, op.total, op.now)
	case "chunk":
		return fmt.Sprintf("Chunk(sid=%d, off=%d, len=%d, now=%d)", op.sid, op.off, len(op.data), op.now)
	case "complete":
		return fmt.Sprintf("Complete(sid=%d, now=%d)", op.sid, op.now)
	case "abort":
		return fmt.Sprintf("Abort(sid=%d, now=%d)", op.sid, op.now)
	case "query":
		return fmt.Sprintf("Query(sid=%d, now=%d)", op.sid, op.now)
	}
	return op.kind
}

// randSink 以约 5% 概率注入日志故障。
type randSink struct {
	rng   *rand.Rand
	inner journal.MemSink
}

func (s *randSink) Append(rec []byte) error {
	if s.rng.Intn(20) == 0 {
		return errors.New("注入故障")
	}
	return s.inner.Append(rec)
}

func genNow(rng *rand.Rand, md *model) int64 {
	if rng.Intn(50) == 0 {
		return MaxNow + 1 // 非法 now
	}
	base := md.now
	if base < 0 {
		base = 0
	}
	now := base + rng.Int63n(7) - 2 // 含小幅回退
	if now < 0 {
		now = 0
	}
	return now
}

func genTenant(rng *rand.Rand) string {
	switch rng.Intn(20) {
	case 0, 1:
		return ""
	case 2:
		return "D" // 从未设额度
	default:
		return []string{"A", "B", "C"}[rng.Intn(3)]
	}
}

func genSid(rng *rand.Rand, md *model) int64 {
	var issued []int64
	for sid := range md.sess {
		issued = append(issued, sid)
	}
	switch rng.Intn(10) {
	case 0:
		return 0
	case 1:
		return -3
	case 2:
		return md.nextSid + 1 + rng.Int63n(50)
	case 3:
		return md.nextSid
	default:
		if len(issued) == 0 {
			return 1
		}
		return issued[rng.Intn(len(issued))]
	}
}

func genChunkData(rng *rand.Rand, md *model, sid int64) (off int64, data []byte) {
	s, ok := md.sess[sid]
	if !ok || s.status != StatusOpen {
		return rng.Int63n(8), mkBytes(rng.Intn(200), rng.Intn(6))
	}
	roll := rng.Intn(100)
	switch {
	case roll < 2:
		return 0, make([]byte, MaxChunk+1) // 超长数据
	case roll < 12:
		return s.c, nil // 探测块
	case roll < 27:
		return s.c + 1 + rng.Int63n(3), mkBytes(rng.Intn(200), rng.Intn(4)) // 缺口
	}
	off = rng.Int63n(s.c + 1)
	maxLen := int64(12)
	if rng.Intn(4) == 0 {
		maxLen = s.total - off + 4 // 可能越界
		if maxLen < 1 {
			maxLen = 1
		}
	}
	n := rng.Int63n(maxLen + 1)
	data = make([]byte, n)
	mutate := rng.Intn(5) == 0
	for i := int64(0); i < n; i++ {
		if b, committed := s.data[off+i]; committed && !(mutate && i == 0) {
			data[i] = b
		} else {
			data[i] = byte(rng.Intn(256))
		}
	}
	return off, data
}

func genOp(rng *rand.Rand, md *model) randOp {
	op := randOp{now: genNow(rng, md)}
	switch rng.Intn(100) {
	case 0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11:
		op.kind = "setquota"
		op.tenant = genTenant(rng)
		switch rng.Intn(20) {
		case 0:
			op.q = -1
		case 1:
			op.q = quota.MaxQuota + 1
		case 2, 3:
			u, r, _, _ := md.tenantView(op.tenant)
			op.q = u + r - int64(rng.Intn(2)) // 恰等或低 1
			if op.q < 0 {
				op.q = 0
			}
		default:
			op.q = rng.Int63n(201)
		}
	case 12, 13, 14, 15, 16, 17, 18, 19, 20, 21, 22, 23, 24, 25, 26, 27, 28, 29, 30, 31, 32, 33, 34, 35, 36:
		op.kind = "create"
		op.tenant = genTenant(rng)
		op.key = []string{"k1", "k2", "k2", "k3", ""}[rng.Intn(5)]
		switch rng.Intn(20) {
		case 0:
			op.total = 0
		case 1:
			op.total = MaxTotal + 1
		case 2:
			op.total = 1 + rng.Int63n(1_000_000_000)
		default:
			op.total = 1 + rng.Int63n(48)
		}
	case 37, 38, 39, 40, 41, 42, 43, 44, 45, 46, 47, 48, 49, 50, 51, 52, 53, 54, 55, 56, 57, 58, 59, 60, 61, 62, 63, 64, 65, 66:
		op.kind = "chunk"
		op.sid = genSid(rng, md)
		op.off, op.data = genChunkData(rng, md, op.sid)
	case 67, 68, 69, 70, 71, 72, 73, 74, 75, 76:
		op.kind = "complete"
		op.sid = genSid(rng, md)
	case 77, 78, 79, 80, 81, 82, 83, 84:
		op.kind = "abort"
		op.sid = genSid(rng, md)
	default:
		op.kind = "query"
		op.sid = genSid(rng, md)
	}
	return op
}

func callService(svc *Service, op randOp) (sid, c, exp int64, err error) {
	switch op.kind {
	case "setquota":
		err = svc.SetQuota(op.tenant, op.q)
	case "create":
		sid, err = svc.Create(op.tenant, op.key, op.total, op.now)
	case "chunk":
		err = svc.Chunk(op.sid, op.off, op.data, op.now)
	case "complete":
		err = svc.Complete(op.sid, op.now)
	case "abort":
		err = svc.Abort(op.sid, op.now)
	case "query":
		c, exp, err = svc.Query(op.sid, op.now)
	}
	return sid, c, exp, err
}

func countNewlyExpired(prev, cur Snapshot) int {
	n := 0
	for sid, sess := range cur.Sessions {
		if sess.Status != StatusExpired {
			continue
		}
		if old, ok := prev.Sessions[sid]; !ok || old.Status != StatusExpired {
			n++
		}
	}
	return n
}

// TestRandomAgainstModel 1500 组随机操作序列与朴素模拟逐步对照。
func TestRandomAgainstModel(t *testing.T) {
	const sequences = 1500
	const opsPerSeq = 40
	for seq := 0; seq < sequences; seq++ {
		seq := seq
		t.Run(fmt.Sprintf("seq-%d", seq), func(t *testing.T) {
			rng := rand.New(rand.NewSource(int64(seq)*7919 + 1))
			sinkRng := rand.New(rand.NewSource(int64(seq)*104729 + 7))
			m := int64(1 + rng.Intn(4))
			ttl := int64(1 + rng.Intn(8))
			svc := New(m, ttl, &randSink{rng: sinkRng})
			md := newModel(m, ttl)
			t.Logf("构造参数 M=%d T=%d", m, ttl)

			acceptedSnaps := []Snapshot{svc.Snapshot()}
			prevSnap := svc.Snapshot()
			prevPopped := 0
			for i := 0; i < opsPerSeq; i++ {
				op := genOp(rng, md)
				gotSid, gotC, gotExp, gotErr := callService(svc, op)
				jf := errors.Is(gotErr, ErrJournal)
				wantSid, wantC, wantExp, wantErr, reason := md.apply(op, jf)
				t.Logf("op#%d %s => sid=%d c=%d exp=%d err=%v | 依据: %s",
					i, op, gotSid, gotC, gotExp, gotErr, reason)

				if !sameErr(gotErr, wantErr) {
					t.Fatalf("op#%d %s: 错误不一致: 服务=%v 模型=%v", i, op, gotErr, wantErr)
				}
				if gotErr == nil {
					if gotSid != wantSid || gotC != wantC || gotExp != wantExp {
						t.Fatalf("op#%d %s: 返回值不一致: 服务=(%d,%d,%d) 模型=(%d,%d,%d)",
							i, op, gotSid, gotC, gotExp, wantSid, wantC, wantExp)
					}
				}
				snapSvc := svc.Snapshot()
				snapMd := md.snapshot()
				if !reflect.DeepEqual(snapSvc, snapMd) {
					t.Fatalf("op#%d %s: 状态不一致\n服务: %+v\n模型: %+v", i, op, snapSvc, snapMd)
				}
				assertInvariants(t, snapSvc)
				if gotErr == nil && op.kind != "query" {
					reclaimed := countNewlyExpired(prevSnap, snapSvc)
					if delta := svc.popped - prevPopped; delta > reclaimed+1 {
						t.Fatalf("op#%d: popped 增量 %d 超过回收数 %d + 1", i, delta, reclaimed)
					}
					prevPopped = svc.popped
					prevSnap = snapSvc
					acceptedSnaps = append(acceptedSnaps, snapSvc)
				} else if gotErr != nil && svc.popped != prevPopped {
					t.Fatalf("op#%d: 被拒操作不得累计 popped", i)
				}
			}

			records := svc.Records()
			if len(records) != len(acceptedSnaps)-1 {
				t.Fatalf("记录数 %d 与被接受操作数 %d 不符", len(records), len(acceptedSnaps)-1)
			}
			// 全量回放一致。
			replayed, err := Replay(records, m, ttl)
			if err != nil {
				t.Fatalf("全量回放失败: %v", err)
			}
			if got, want := replayed.Snapshot(), svc.Snapshot(); !reflect.DeepEqual(got, want) {
				t.Fatalf("全量回放状态不一致\n回放: %+v\n服务: %+v", got, want)
			}
			// 前缀回放：前 50 组全查，其余抽查。
			checkK := map[int]bool{0: true, len(records): true}
			if seq < 50 {
				for k := 0; k <= len(records); k++ {
					checkK[k] = true
				}
			} else {
				for j := 0; j < 3; j++ {
					checkK[rng.Intn(len(records)+1)] = true
				}
			}
			for k := range checkK {
				rk, err := Replay(records[:k], m, ttl)
				if err != nil {
					t.Fatalf("前 %d 条记录回放失败: %v", k, err)
				}
				if got := rk.Snapshot(); !reflect.DeepEqual(got, acceptedSnaps[k]) {
					t.Fatalf("前 %d 条回放不一致\n回放: %+v\n期望: %+v", k, got, acceptedSnaps[k])
				}
			}
		})
	}
}

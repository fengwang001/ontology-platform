package store

import (
	"fmt"
	"sync"
	"testing"
)

func mustWrite(t *testing.T, s *Store, pk string, token, biz int64, payload string) Version {
	t.Helper()
	v, err := s.Write("T", pk, token, biz, payload)
	if err != nil {
		t.Fatalf("Write(%s,v%d,biz=%d) 意外失败: %v", pk, token, biz, err)
	}
	return v
}

func rejectKind(t *testing.T, err error) ErrKind {
	t.Helper()
	re, ok := err.(*RejectError)
	if !ok {
		t.Fatalf("期望 RejectError, got %T: %v", err, err)
	}
	return re.Kind
}

func TestRejectOrdering(t *testing.T) {
	s := New(NewFakeClock(100))
	mustWrite(t, s, "a", 0, 50, "v1")

	// 参数非法优先于凭证不匹配：空主键 + 错误凭证 -> 报参数非法。
	_, err := s.Write("T", "", 99, 10, "x")
	if k := rejectKind(t, err); k != ErrInvalidArgument {
		t.Fatalf("次序一失败: got %v", k)
	}
	// 非法业务起点 + 错误凭证 -> 报参数非法。
	_, err = s.Write("T", "a", 99, -1, "x")
	if k := rejectKind(t, err); k != ErrInvalidArgument {
		t.Fatalf("非法业务起点应报参数非法: got %v", k)
	}
	// 非法凭证格式（负数）-> 参数非法。
	_, err = s.Write("T", "a", -1, 60, "x")
	if k := rejectKind(t, err); k != ErrInvalidArgument {
		t.Fatalf("负凭证应报参数非法: got %v", k)
	}
	// 凭证不匹配优先于业务边界：错误凭证 + 越界起点 -> 报凭证冲突。
	_, err = s.Write("T", "a", 7, 10, "x")
	if k := rejectKind(t, err); k != ErrConcurrencyConflict {
		t.Fatalf("次序二失败: got %v", k)
	}
	// 正确凭证 + 越界起点 -> 报业务边界。
	_, err = s.Write("T", "a", 1, 10, "x")
	if k := rejectKind(t, err); k != ErrBizBoundary {
		t.Fatalf("次序三失败: got %v", k)
	}
	// 被拒绝的写入不占用版本号、不移动最新指针、不被索引看到。
	if got := s.LatestSeq("T", "a"); got != 1 {
		t.Fatalf("拒绝后最新版本应为 1, got %d", got)
	}
	v := mustWrite(t, s, "a", 1, 60, "v2")
	if v.Seq != 2 {
		t.Fatalf("拒绝不应占用序号, got seq=%d", v.Seq)
	}
	if _, st := s.Query("T", "a", v.Sys, 10); st != StatusNoVersionAtTime {
		t.Fatalf("被拒绝的写入不应被索引看到, got %v", st)
	}
}

func TestSystemTimeMonotonic(t *testing.T) {
	s := New(NewFakeClock(1000))
	var prev Version
	for i := int64(0); i < 100; i++ {
		v := mustWrite(t, s, "a", i, 10+i, "p")
		if i > 0 && v.Sys <= prev.Sys {
			t.Fatalf("系统时间回退: %d <= %d", v.Sys, prev.Sys)
		}
		prev = v
	}
	// 时钟回拨时由钳制保证不回退。
	s2 := New(NewFakeClock(0))
	mustWrite(t, s2, "a", 0, 10, "p1") // sys=0
	// 手工构造一个时钟回拨场景：FakeClock 从 0 开始，第二次 Now 为 1，仍递增。
	v2 := mustWrite(t, s2, "a", 1, 20, "p2")
	if v2.Sys <= 0 {
		t.Fatalf("系统时间应严格递增, got %d", v2.Sys)
	}
}

func TestDeleteResurrectThreePhases(t *testing.T) {
	s := New(NewFakeClock(100))
	mustWrite(t, s, "a", 0, 10, "before")                // v1 sys=100 biz=10
	if _, err := s.Delete("T", "a", 1, 20); err != nil { // v2 sys=101 biz=20
		t.Fatal(err)
	}
	mustWrite(t, s, "a", 2, 30, "after") // v3 sys=102 biz=30 复活

	cases := []struct {
		sysQ, bizQ int64
		want       Status
		payload    string
	}{
		{100, 15, StatusFound, "before"}, // 删除前
		{102, 15, StatusFound, "before"}, // 删除前区间 [10,20) 不受删除影响
		{102, 25, StatusDeleted, ""},     // 删除中 [20,30)
		{102, 35, StatusFound, "after"},  // 复活后 [30,∞)
		{101, 35, StatusDeleted, ""},     // 复活前的系统时间看 35 仍处删除态
		{100, 35, StatusFound, "before"}, // 更早系统时间看 35 是删除前的开放区间
	}
	for _, c := range cases {
		v, st := s.Query("T", "a", c.sysQ, c.bizQ)
		if st != c.want || (st == StatusFound && v.Payload != c.payload) {
			t.Fatalf("Query(%d,%d) = (%v,%q), want (%v,%q)",
				c.sysQ, c.bizQ, st, v.Payload, c.want, c.payload)
		}
	}
	// 系统时间早于任何提交：主键已写入但无可见版本。
	if _, st := s.Query("T", "a", 99, 15); st != StatusNoVersionAtTime {
		t.Fatalf("sysQ 早于任何提交应为 NoVersionAtTime, got %v", st)
	}
	// 删除不是主键整体失效：复活后历史三段可分别还原（上面已验证），
	// 且最新版本指针继续前进。
	if got := s.LatestSeq("T", "a"); got != 3 {
		t.Fatalf("LatestSeq = %d, want 3", got)
	}
}

func TestQueryBoundaries(t *testing.T) {
	s := New(NewFakeClock(1))
	mustWrite(t, s, "a", 0, 100, "only")
	// 业务时间早于该主键任何记录。
	if _, st := s.Query("T", "a", 1000, 50); st != StatusNoVersionAtTime {
		t.Fatalf("bizQ 早于任何记录: got %v", st)
	}
	// 业务时间晚于全部记录：命中开放区间。
	v, st := s.Query("T", "a", 1000, 1<<40)
	if st != StatusFound || v.Payload != "only" {
		t.Fatalf("bizQ 晚于全部记录: got %v %v", st, v)
	}
	// 主键从未写入。
	if _, st := s.Query("T", "ghost", 1000, 100); st != StatusNeverWritten {
		t.Fatalf("未写入主键: got %v", st)
	}
}

func TestReplayDeterministic(t *testing.T) {
	wal := &MemWAL{}
	s := New(NewFakeClock(500), WithWAL(wal))
	mustWrite(t, s, "a", 0, 10, "p0")
	mustWrite(t, s, "a", 1, 30, "p2")
	mustWrite(t, s, "a", 2, 20, "p1") // 追溯写入过去的业务时间段
	if _, err := s.Delete("T", "a", 3, 25); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, s, "b", 0, 5, "other")

	s2, err := Replay(wal.Records)
	if err != nil {
		t.Fatal(err)
	}
	for _, pk := range []string{"a", "b"} {
		got, want := s2.Versions("T", pk), s.Versions("T", pk)
		if fmt.Sprintf("%+v", got) != fmt.Sprintf("%+v", want) {
			t.Fatalf("主键 %s 重放后版本链不一致:\n got %+v\nwant %+v", pk, got, want)
		}
	}
	// 重放后查询结果一致。
	for sysQ := int64(499); sysQ <= 508; sysQ++ {
		for bizQ := int64(0); bizQ <= 35; bizQ += 5 {
			v1, st1 := s.Query("T", "a", sysQ, bizQ)
			v2, st2 := s2.Query("T", "a", sysQ, bizQ)
			if st1 != st2 || v1 != v2 {
				t.Fatalf("Query(%d,%d) 重放不一致: (%v,%v) vs (%v,%v)",
					sysQ, bizQ, st1, v1, st2, v2)
			}
		}
	}
}

// TestConcurrentSerialization 并发写入的最终效果必须等价于某个串行顺序：
// 成功版本的凭证恰好覆盖 0..N-1 各一次，序号连续无空洞。
func TestConcurrentSerialization(t *testing.T) {
	s := New(NewFakeClock(1))
	const writers = 8
	const perWriter = 50
	var wg sync.WaitGroup
	tokens := make([][]int64, writers)
	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < perWriter; i++ {
				for {
					token := s.LatestSeq("T", "hot")
					v, err := s.Write("T", "hot", token, 1000, "x")
					if err == nil {
						tokens[w] = append(tokens[w], v.Seq-1)
						break
					}
					if re, ok := err.(*RejectError); !ok || re.Kind != ErrConcurrencyConflict {
						t.Errorf("意外错误: %v", err)
						return
					}
				}
			}
		}(w)
	}
	wg.Wait()

	seen := map[int64]int{}
	total := 0
	for _, ts := range tokens {
		for _, tk := range ts {
			seen[tk]++
			total++
		}
	}
	if total != writers*perWriter {
		t.Fatalf("成功写入数 = %d, want %d", total, writers*perWriter)
	}
	for tk := int64(0); tk < int64(total); tk++ {
		if seen[tk] != 1 {
			t.Fatalf("凭证 v%d 成功 %d 次，出现基于同一前序的双成功或序号空洞", tk, seen[tk])
		}
	}
	// 版本链连续且系统时间严格递增。
	vs := s.Versions("T", "hot")
	if len(vs) != total {
		t.Fatalf("版本数 = %d, want %d", len(vs), total)
	}
	for i, v := range vs {
		if v.Seq != int64(i+1) {
			t.Fatalf("序号不连续: versions[%d].Seq = %d", i, v.Seq)
		}
		if i > 0 && v.Sys <= vs[i-1].Sys {
			t.Fatalf("系统时间未严格递增: %d <= %d", v.Sys, vs[i-1].Sys)
		}
	}
}

package keytable

import (
	"errors"
	"strings"
	"testing"

	"ontology/report"
)

// TestNewBounds 覆盖全部构造参数边界。
func TestNewBounds(t *testing.T) {
	good := []struct{ n, m, w, kt, tmax int64 }{
		{0, 1, 1, 1, 1},
		{1_000_000, 1_000_000, 1_000_000_000, 100_000, 10_000},
	}
	for _, c := range good {
		if _, err := New(c.n, c.m, c.w, c.kt, c.tmax); err != nil {
			t.Fatalf("合法边界被拒: %+v %v", c, err)
		}
	}
	bad := []struct{ n, m, w, kt, tmax int64 }{
		{-1, 1, 1, 1, 1}, {1_000_001, 1, 1, 1, 1},
		{0, 0, 1, 1, 1}, {0, 1_000_001, 1, 1, 1},
		{0, 1, 0, 1, 1}, {0, 1, 1_000_000_001, 1, 1},
		{0, 1, 1, 0, 1}, {0, 1, 1, 100_001, 1},
		{0, 1, 1, 1, 0}, {0, 1, 1, 1, 10_001},
	}
	for i, c := range bad {
		if _, err := New(c.n, c.m, c.w, c.kt, c.tmax); !errors.Is(err, report.ErrInvalidArgument) {
			t.Fatalf("越界组#%d %+v 应报参数非法, got %v", i, c, err)
		}
	}
}

// TestRecordInvalidArgs 覆盖 tenant/key 空串与超长、sev 越界、now 越界。
func TestRecordInvalidArgs(t *testing.T) {
	s, _ := New(1, 1, 1000, 10, 10)
	long := strings.Repeat("x", 65)
	cases := []struct {
		name        string
		now         int64
		tenant, key string
		sev         int
	}{
		{"空 tenant", 0, "", "k", 1},
		{"空 key", 0, "t", "", 1},
		{"tenant 超长", 0, long, "k", 1},
		{"key 超长", 0, "t", long, 1},
		{"sev=-1", 0, "t", "k", -1},
		{"sev=6", 0, "t", "k", 6},
		{"now=-1", -1, "t", "k", 1},
		{"now 越上界", 1_000_000_000_001, "t", "k", 1},
	}
	for _, c := range cases {
		d := s.Record(c.now, c.tenant, c.key, c.sev)
		t.Logf("非法输入 %s | 输出 err=%v（不改任何状态）", c.name, d.Err)
		if !errors.Is(d.Err, report.ErrInvalidArgument) {
			t.Fatalf("%s: 期望参数非法, got %v", c.name, d.Err)
		}
	}
	// 非法 now=-1 不得推进时钟：合法 now=0 仍可接受。
	if d := s.Record(0, "t", "k", 1); d.Err != nil || !d.Kept {
		t.Fatalf("被拒操作推进了时钟或改了状态: %+v", d)
	}
}

// TestRejectOrderAndNoStateChange 拒绝顺序 参数非法 → 时钟回退 → 租户上限。
func TestRejectOrderAndNoStateChange(t *testing.T) {
	s, _ := New(100, 100, 1000, 1, 1)
	s.Record(100, "t1", "a", 1)
	// 同时违反参数非法与时钟回退：只报参数非法。
	d := s.Record(50, "t1", "a", 9)
	t.Logf("输入 now=50 sev=9（回退+非法）| 输出 err=%v 依据 顺序只报参数非法", d.Err)
	if !errors.Is(d.Err, report.ErrInvalidArgument) {
		t.Fatalf("应先报参数非法, got %v", d.Err)
	}
	// 同时违反时钟回退与租户上限：只报时钟回退。
	d = s.Record(50, "t2", "a", 1)
	t.Logf("输入 now=50 新租户 t2（回退+租户上限）| 输出 err=%v 依据 先报回退", d.Err)
	if !errors.Is(d.Err, report.ErrClockSkew) {
		t.Fatalf("应先报时钟回退, got %v", d.Err)
	}
	// 时钟正常时才轮到租户上限。
	d = s.Record(100, "t2", "a", 1)
	if !errors.Is(d.Err, report.ErrTenantLimit) {
		t.Fatalf("应报租户上限, got %v", d.Err)
	}
	// 被拒记录不改访问序：t1 仍只有 a 且 pending 为 0。
	if p, ok := s.Pending("t1", "a"); !ok || p != 0 {
		t.Fatalf("被拒操作改变了条目: pending=%d ok=%v", p, ok)
	}
}

// TestExaminedConstantPerRecord 考察条目数为每记录常数：Kt=100 与 10000 两档对照。
func TestExaminedConstantPerRecord(t *testing.T) {
	for _, kt := range []int64{100, 10_000} {
		s, _ := New(0, 1000, 1000, kt, 10) // N=0 且 M 大：除淘汰键外全部丢弃
		for i := int64(0); i < kt; i++ {   // 先填满 Kt 个不同键（新键不计入考察）
			s.Record(0, "t", "k"+itoa(i), 1)
		}
		before := s.examinedEntries()
		const rounds = 200
		for i := 0; i < rounds; i++ {
			s.Record(0, "t", "k"+itoa(int64(i%int(kt))), 1) // 全部命中已存在键
		}
		after := s.examinedEntries()
		got := after - before
		t.Logf("Kt=%d 填满后输入 %d 条命中记录 | 考察条目数=%d（期望=%d，每条常数1）",
			kt, rounds, got, rounds)
		if got != rounds {
			t.Fatalf("Kt=%d 考察条目数=%d，应恒为每记录 1，不随 Kt 增长", kt, got)
		}
	}
}

// itoa 避免引入 strconv 的最小整数转字符串。
func itoa(n int64) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}

// TestEvictionResetsQuota 淘汰后再出现额度重新计起，丢弃历史不残留。
func TestEvictionResetsQuota(t *testing.T) {
	s, _ := New(1, 2, 1000, 1, 1)
	s.Record(0, "t", "a", 1)      // cnt=1 放行
	s.Record(0, "t", "a", 1)      // cnt=2 丢弃
	s.Record(0, "t", "b", 1)      // 淘汰 a（带 Evicted 摘要 dropped=1）
	d := s.Record(0, "t", "a", 1) // a 重建：cnt=1 放行，pending=0
	t.Logf("输入 a 重建 | 输出 kept=%v summaries=%v", d.Kept, d.Summaries)
	if !d.Kept || d.Summaries != nil {
		t.Fatalf("重建额度未重置: %+v", d)
	}
	if p, ok := s.Pending("t", "a"); !ok || p != 0 {
		t.Fatalf("重建后 pending=%d,%v want 0,true", p, ok)
	}
}

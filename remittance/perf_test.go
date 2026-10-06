package remittance

// 性能可证明性：
//  1. TestInternalStateBounded —— 白盒验证内部状态规模有界，
//     不随历史汇款总数增长（窗口滑出即清除，过期队列即出即清）。
//  2. TestConstantWorkPerOp —— 用 testing.AllocsPerRun 对比小历史与
//     巨大历史下的单操作分配次数，证明单操作开销与历史规模无关。
//  3. Benchmark* —— 对比小/大历史下的 ns/op，可重复验证。

import (
	"fmt"
	"testing"
)

func perfConfig() Config {
	return Config{
		SingleLimit:     1 << 40,
		DayLimit:        1 << 50,
		YearLimit:       1 << 60,
		QuoteTTL:        10,
		ReviewThreshold: 1,
		ReviewTimeout:   10,
	}
}

// fillHistory 以每日一笔的节奏为 remitter 制造 days 天的历史。
func fillHistory(t testing.TB, s *System, remitter string, days int, now int64) int64 {
	t.Helper()
	for i := 0; i < days; i++ {
		q, err := s.RequestQuote(remitter, 10, 1_000_000, now)
		if err != nil {
			t.Fatalf("RequestQuote: %v", err)
		}
		if _, err := s.Submit(remitter, fmt.Sprintf("%s-%d", remitter, i), q, "p", now); err != nil {
			t.Fatalf("Submit: %v", err)
		}
		now += 86_400
	}
	return now
}

func TestInternalStateBounded(t *testing.T) {
	s, err := NewSystem(perfConfig())
	if err != nil {
		t.Fatalf("NewSystem: %v", err)
	}
	// 10000 天（约 27 年）每天一笔待审核汇款，次日即逾期；
	// 返回的 now 已越过最后一笔的截止时刻，全部占用应已释放。
	now := fillHistory(t, s, "u", 10_000, 0)
	if _, err := s.QueryUsage("u", now); err != nil {
		t.Fatalf("QueryUsage: %v", err)
	}
	l := s.ledgers["u"]
	if got := len(l.days); got > 366 {
		t.Fatalf("ledger days unbounded: %d entries after 10000 days", got)
	}
	if got := len(l.order) - l.head; got > 366 {
		t.Fatalf("ledger order unbounded: %d live entries", got)
	}
	// 过期队列：全部出队。
	if got := len(s.expiry.items) - s.expiry.head; got > 1 {
		t.Fatalf("expiry queue unbounded: %d live items", got)
	}
	// 全部占用均已释放（每日一笔，R=10s，次日必逾期）。
	if l.rollingUsed() != 0 {
		t.Fatalf("want rolling 0, got %d", l.rollingUsed())
	}
}

func TestConstantWorkPerOp(t *testing.T) {
	small, _ := NewSystem(perfConfig())
	nowSmall := fillHistory(t, small, "u", 10, 0)

	huge, _ := NewSystem(perfConfig())
	nowHuge := fillHistory(t, huge, "u", 200_000, 0)
	// 其他汇款人不影响：再填充 50 个汇款人的历史。
	for r := 0; r < 50; r++ {
		nowHuge = fillHistory(t, huge, fmt.Sprintf("other-%d", r), 100, nowHuge)
	}

	// 查询：小历史与大历史的单操作分配次数应一致（允许窗口压缩的微小差）。
	a := testing.AllocsPerRun(200, func() {
		nowSmall += 86_400
		if _, err := small.QueryUsage("u", nowSmall); err != nil {
			t.Fatalf("query small: %v", err)
		}
	})
	b := testing.AllocsPerRun(200, func() {
		nowHuge += 86_400
		if _, err := huge.QueryUsage("u", nowHuge); err != nil {
			t.Fatalf("query huge: %v", err)
		}
	})
	if b > a+3 {
		t.Fatalf("query allocs grow with history: small=%v huge=%v", a, b)
	}
	t.Logf("allocs per query: small-history=%v huge-history=%v", a, b)

	// 提交：同样与历史规模无关。
	c := testing.AllocsPerRun(100, func() {
		nowSmall++
		q, err := small.RequestQuote("u", 10, 1_000_000, nowSmall)
		if err != nil {
			t.Fatalf("quote small: %v", err)
		}
		if _, err := small.Submit("u", fmt.Sprintf("ka-%d", nowSmall), q, "p", nowSmall); err != nil {
			t.Fatalf("submit small: %v", err)
		}
	})
	d := testing.AllocsPerRun(100, func() {
		nowHuge++
		q, err := huge.RequestQuote("u", 10, 1_000_000, nowHuge)
		if err != nil {
			t.Fatalf("quote huge: %v", err)
		}
		if _, err := huge.Submit("u", fmt.Sprintf("kb-%d", nowHuge), q, "p", nowHuge); err != nil {
			t.Fatalf("submit huge: %v", err)
		}
	})
	if d > c+3 {
		t.Fatalf("submit allocs grow with history: small=%v huge=%v", c, d)
	}
	t.Logf("allocs per quote+submit: small-history=%v huge-history=%v", c, d)
}

func BenchmarkQuerySmallHistory(b *testing.B) {
	s, _ := NewSystem(perfConfig())
	now := fillHistory(b, s, "u", 10, 0)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		now++
		if _, err := s.QueryUsage("u", now); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkQueryHugeHistory(b *testing.B) {
	s, _ := NewSystem(perfConfig())
	now := fillHistory(b, s, "u", 200_000, 0)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		now++
		if _, err := s.QueryUsage("u", now); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkSubmit(b *testing.B) {
	s, _ := NewSystem(perfConfig())
	now := fillHistory(b, s, "u", 1000, 0)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		now++
		q, err := s.RequestQuote("u", 10, 1_000_000, now)
		if err != nil {
			b.Fatal(err)
		}
		if _, err := s.Submit("u", fmt.Sprintf("kb-%d", i), q, "p", now); err != nil {
			b.Fatal(err)
		}
	}
}

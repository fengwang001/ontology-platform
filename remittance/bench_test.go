package remittance

import "testing"

// BenchmarkSubmitAfterLongHistory 证明提交开销不随该汇款人历史汇款总数增长。
// 同一账户先制造 N 笔历史汇款（多日、多数已出款），再测一次提交。
func BenchmarkSubmitAfterLongHistory(b *testing.B) {
	for _, n := range []int{1_000, 10_000, 100_000} {
		b.Run(historySize(n), func(b *testing.B) {
			e := New(Config{QuoteTTLSeconds: 1_000_000_000, ReviewSeconds: 10,
				ReviewThreshold: 1 << 60})
			e.AddSender("s1", Limits{Single: 1 << 60, Daily: 1 << 60, Annual: 1 << 60})
			var now int64
			for i := 0; i < n; i++ {
				now += secondsPerDay // 每天一笔，历史桶大多滚出窗口
				q, err := e.ApplyQuote(QuoteRequest{
					Sender: "s1", SourceCCY: "USD", TargetCCY: "CNY",
					Amount: 1, RatePPM: 1000000, Now: now,
				})
				if err != nil {
					b.Fatal(err)
				}
				if _, err := e.Submit(SubmitRequest{
					Sender: "s1", QuoteID: q, Payee: "p",
					IdemKey: "k" + itoa(i), Now: now,
				}); err != nil {
					b.Fatal(err)
				}
			}
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				now++
				q, err := e.ApplyQuote(QuoteRequest{
					Sender: "s1", SourceCCY: "USD", TargetCCY: "CNY",
					Amount: 1, RatePPM: 1000000, Now: now,
				})
				if err != nil {
					b.Fatal(err)
				}
				if _, err := e.Submit(SubmitRequest{
					Sender: "s1", QuoteID: q, Payee: "p",
					IdemKey: "fresh-" + itoa(n+i), Now: now,
				}); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// BenchmarkUsageManySenders 证明查询开销不随汇款人总数增长。
func BenchmarkUsageManySenders(b *testing.B) {
	e := New(Config{QuoteTTLSeconds: 10, ReviewSeconds: 10, ReviewThreshold: 1 << 60})
	const senders = 10_000
	for i := 0; i < senders; i++ {
		id := "s" + itoa(i)
		e.AddSender(id, Limits{Single: 10, Daily: 10, Annual: 10})
	}
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			id := "s" + itoa(i%10) // 只查固定少数账户
			if _, err := e.Usage(id, int64(i)); err != nil && CodeOf(err) != ErrCodeClockBackward {
				b.Fatal(err)
			}
			i++
		}
	})
}

func historySize(n int) string {
	switch n {
	case 1_000:
		return "1k"
	case 10_000:
		return "10k"
	case 100_000:
		return "100k"
	}
	return itoa(n)
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var buf [20]byte
	p := len(buf)
	for i > 0 {
		p--
		buf[p] = byte('0' + i%10)
		i /= 10
	}
	return string(buf[p:])
}

package channel_test

import (
	"fmt"
	"testing"

	"ontology/channel"
)

// benchScale 构造一个有 n 条历史消息的频道：
//   - alice 为管理员；bob、carol 为成员；
//   - 每条消息由 alice 发出，约 20% 提及 bob；
//   - 约 10% 由 alice 撤回（使各类 Fenwick 都有 -1 路径）；
//   - bob 的读水位停在 n/2，使其 (watermark, latest] 区间有大量消息。
func benchScale(b *testing.B, n int64) *channel.Channel {
	b.Helper()
	c, err := channel.New(channel.Config{EditWindow: 86400, RecallWindow: 86400, MaxEdits: 10})
	if err != nil {
		b.Fatal(err)
	}
	if err := c.Join("alice", 0); err != nil {
		b.Fatal(err)
	}
	if err := c.Join("bob", 0); err != nil {
		b.Fatal(err)
	}
	if err := c.Join("carol", 0); err != nil {
		b.Fatal(err)
	}
	var mentions []string
	for seq := int64(1); seq <= n; seq++ {
		now := seq
		if seq%5 == 0 {
			mentions = []string{"bob"}
		} else {
			mentions = nil
		}
		s, err := c.Send("alice", fmt.Sprintf("body-%d", seq), mentions, now)
		if err != nil {
			b.Fatalf("send %d: %v", seq, err)
		}
		if seq%10 == 0 {
			if err := c.Recall("alice", s, now); err != nil {
				b.Fatalf("recall %d: %v", seq, err)
			}
		}
	}
	if err := c.MarkRead("bob", n/2, n+1); err != nil {
		b.Fatal(err)
	}
	return c
}

func benchmarkUnreadAt(n int64) func(b *testing.B) {
	return func(b *testing.B) {
		c := benchScale(b, n)
		b.ResetTimer()
		var sink int64
		for i := 0; i < b.N; i++ {
			v, err := c.Unread("bob")
			if err != nil {
				b.Fatal(err)
			}
			sink += v
		}
		b.StopTimer()
		if sink == 0 {
			b.Fatal("sanity: expected nonzero unread")
		}
	}
}

func benchmarkUnreadMentionsAt(n int64) func(b *testing.B) {
	return func(b *testing.B) {
		c := benchScale(b, n)
		b.ResetTimer()
		var sink int64
		for i := 0; i < b.N; i++ {
			v, err := c.UnreadMentions("bob")
			if err != nil {
				b.Fatal(err)
			}
			sink += v
		}
		b.StopTimer()
		if sink == 0 {
			b.Fatal("sanity: expected nonzero mention unread")
		}
	}
}

// MarkRead 在水位附近反复“推进到同一值/推进一格”，验证其不扫描历史。
func benchmarkMarkReadAt(n int64) func(b *testing.B) {
	return func(b *testing.B) {
		c := benchScale(b, n)
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			// 只在恰等于当前水位上反复确认成功（无变化），这是合法且无副作用的。
			if err := c.MarkRead("bob", n/2, n+1+int64(i)); err != nil {
				b.Fatal(err)
			}
		}
	}
}

func BenchmarkUnread_10k(b *testing.B)         { benchmarkUnreadAt(10_000)(b) }
func BenchmarkUnread_1m(b *testing.B)          { benchmarkUnreadAt(1_000_000)(b) }
func BenchmarkUnreadMentions_10k(b *testing.B) { benchmarkUnreadMentionsAt(10_000)(b) }
func BenchmarkUnreadMentions_1m(b *testing.B)  { benchmarkUnreadMentionsAt(1_000_000)(b) }
func BenchmarkMarkRead_10k(b *testing.B)       { benchmarkMarkReadAt(10_000)(b) }
func BenchmarkMarkRead_1m(b *testing.B)        { benchmarkMarkReadAt(1_000_000)(b) }

// Send：大历史下的发送成本。Fenwick 为倍增容量，1M 后不再重建，
// 发送只做 O(log n) 的索引更新，且仅按提及人数循环。
func BenchmarkSend_10k(b *testing.B) { benchmarkSend(b, 10_000) }
func BenchmarkSend_1m(b *testing.B)  { benchmarkSend(b, 1_000_000) }

func benchmarkSend(b *testing.B, n int64) {
	c := benchScale(b, n)
	now := n + 1
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := c.Send("alice", "fresh", []string{"bob"}, now); err != nil {
			b.Fatal(err)
		}
		now++
	}
}

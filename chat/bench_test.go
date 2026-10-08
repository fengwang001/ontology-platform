package chat

import (
	"fmt"
	"sync"
	"testing"
	"time"
)

// buildBenchChannel builds a channel with n messages from "alice", a third
// of them mentioning "bob" and "reader". 25 members exist so that Send can
// use the maximum of 20 mentions.
func buildBenchChannel(n int) *Channel {
	c, err := NewChannel(MaxWindow, MaxWindow, MaxEdits)
	if err != nil {
		panic(err)
	}
	users := []string{"alice", "bob", "reader"}
	for i := 0; i < 25; i++ {
		users = append(users, fmt.Sprintf("u%02d", i))
	}
	for _, u := range users {
		if err := c.Join(u, 0); err != nil {
			panic(err)
		}
	}
	for i := 1; i <= n; i++ {
		var mentions []string
		if i%3 == 0 {
			mentions = []string{"bob", "reader"}
		}
		if _, err := c.Send("alice", "hello world", mentions, int64(i)); err != nil {
			panic(err)
		}
	}
	return c
}

var (
	channel10K   = sync.OnceValue(func() *Channel { return buildBenchChannel(10_000) })
	channel1M    = sync.OnceValue(func() *Channel { return buildBenchChannel(1_000_000) })
	benchMention = []string{
		"u00", "u01", "u02", "u03", "u04", "u05", "u06", "u07", "u08", "u09",
		"u10", "u11", "u12", "u13", "u14", "u15", "u16", "u17", "u18", "u19",
	}
)

func benchUnread(b *testing.B, c *Channel) {
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := c.Unread("reader"); err != nil {
			b.Fatal(err)
		}
	}
}

func benchUnreadMentions(b *testing.B, c *Channel) {
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := c.UnreadMentions("reader"); err != nil {
			b.Fatal(err)
		}
	}
}

func benchMarkRead(b *testing.B, c *Channel) {
	latest := c.Latest()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// now=MaxNow keeps the clock check passing on every iteration.
		if err := c.MarkRead("reader", latest, MaxNow); err != nil {
			b.Fatal(err)
		}
	}
}

func benchSend(b *testing.B, c *Channel) {
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := c.Send("alice", "bench", benchMention, MaxNow); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkUnreadHistory10K(b *testing.B)         { benchUnread(b, channel10K()) }
func BenchmarkUnreadHistory1M(b *testing.B)          { benchUnread(b, channel1M()) }
func BenchmarkUnreadMentionsHistory10K(b *testing.B) { benchUnreadMentions(b, channel10K()) }
func BenchmarkUnreadMentionsHistory1M(b *testing.B)  { benchUnreadMentions(b, channel1M()) }
func BenchmarkMarkReadHistory10K(b *testing.B)       { benchMarkRead(b, channel10K()) }
func BenchmarkMarkReadHistory1M(b *testing.B)        { benchMarkRead(b, channel1M()) }
func BenchmarkSendHistory10K(b *testing.B)           { benchSend(b, channel10K()) }
func BenchmarkSendHistory1M(b *testing.B)            { benchSend(b, channel1M()) }

// TestScaleComparison measures Unread / UnreadMentions / MarkRead / Send at
// two history scales differing by two orders of magnitude (1e4 vs 1e6
// messages) and prints per-op costs. Sub-linear growth is observable
// directly from the output; it also compares Send across member counts.
func TestScaleComparison(t *testing.T) {
	if testing.Short() {
		t.Skip("scale comparison skipped in -short mode")
	}
	measure := func(name string, reps int, f func()) float64 {
		start := time.Now()
		for i := 0; i < reps; i++ {
			f()
		}
		return float64(time.Since(start).Nanoseconds()) / float64(reps)
	}

	t.Log("=== history scale: 1e4 vs 1e6 messages ===")
	for _, tc := range []struct {
		name string
		c    *Channel
	}{
		{"1e4", channel10K()},
		{"1e6", channel1M()},
	} {
		latest := tc.c.Latest()
		unread := measure("unread", 20000, func() { _, _ = tc.c.Unread("reader") })
		mentions := measure("unreadmentions", 20000, func() { _, _ = tc.c.UnreadMentions("reader") })
		mark := measure("markread", 20000, func() { _ = tc.c.MarkRead("reader", latest, MaxNow) })
		send := measure("send", 2000, func() { _, _ = tc.c.Send("alice", "scale", benchMention, MaxNow) })
		t.Logf("history=%s msgs=%d: Unread=%.0fns UnreadMentions=%.0fns MarkRead=%.0fns Send(20 mentions)=%.0fns",
			tc.name, latest, unread, mentions, mark, send)
	}

	t.Log("=== member scale: 1e2 vs 1e5 members, same 1e4 messages ===")
	small := buildBenchChannel(10_000)
	large := buildBenchChannel(10_000)
	for i := 0; i < 100_000; i++ {
		if err := large.Join(fmt.Sprintf("member-%d", i), 10_000); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct {
		name string
		c    *Channel
	}{
		{"28 members", small},
		{"100028 members", large},
	} {
		send := measure("send", 2000, func() { _, _ = tc.c.Send("alice", "scale", benchMention, MaxNow) })
		t.Logf("members=%s: Send(20 mentions)=%.0fns", tc.name, send)
	}
}

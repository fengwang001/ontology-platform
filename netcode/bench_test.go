package netcode

import (
	"fmt"
	"testing"
)

// BenchmarkTickIdlePlayers proves Tick cost does not depend on the number
// of idle players: with a constant one processed input per tick, the
// ns/op stays flat as the registered player count grows.
//
//	go test -bench=TickIdlePlayers -benchtime=1000x ./netcode/
func BenchmarkTickIdlePlayers(b *testing.B) {
	for _, idle := range []int{0, 1_000, 100_000} {
		b.Run(fmt.Sprintf("idle=%d", idle), func(b *testing.B) {
			cfg := Config{W: 1_000_000, K: 1, M: 10, P: 2}
			s, err := NewServer(cfg)
			if err != nil {
				b.Fatal(err)
			}
			if err := s.Register("hot"); err != nil {
				b.Fatal(err)
			}
			for i := 0; i < idle; i++ {
				if err := s.Register(fmt.Sprintf("idle-%d", i)); err != nil {
					b.Fatal(err)
				}
			}
			var seq, now int64
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				seq++
				if _, err := s.Receive("hot", Move{Seq: seq, Delta: 1}); err != nil {
					b.Fatal(err)
				}
				now++
				if _, err := s.Tick(now); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// BenchmarkTickBatch measures Tick throughput as a function of the number
// of processed inputs (K=50 fully consumed every tick).
func BenchmarkTickBatch(b *testing.B) {
	cfg := Config{W: 1_000_000, K: 50, M: 10, P: 50}
	s, err := NewServer(cfg)
	if err != nil {
		b.Fatal(err)
	}
	if err := s.Register("hot"); err != nil {
		b.Fatal(err)
	}
	var seq, now int64
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for j := 0; j < 50; j++ {
			seq++
			if _, err := s.Receive("hot", Move{Seq: seq, Delta: 1}); err != nil {
				b.Fatal(err)
			}
		}
		now++
		if _, err := s.Tick(now); err != nil {
			b.Fatal(err)
		}
	}
	b.ReportMetric(50, "processed/op")
}

// BenchmarkApplyAck proves reconciliation cost is proportional to the
// number of unacked inputs, not to history: each iteration keeps the
// unacked window constant (one submit in, one ack out) while total
// history grows without bound.
//
//	go test -bench=ApplyAck ./netcode/
func BenchmarkApplyAck(b *testing.B) {
	for _, window := range []int{10, 100, 1_000} {
		b.Run(fmt.Sprintf("unacked=%d", window), func(b *testing.B) {
			cfg := Config{W: MaxW, K: 50, M: 10, P: 10_000}
			c, err := NewClient(cfg, "p")
			if err != nil {
				b.Fatal(err)
			}
			for i := 0; i < window; i++ {
				if _, err := c.Submit(1); err != nil {
					b.Fatal(err)
				}
			}
			acked := int64(0)
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, err := c.Submit(1); err != nil {
					b.Fatal(err)
				}
				acked++
				pos := acked
				if pos > cfg.W {
					pos = cfg.W
				}
				c.ApplyAck(Ack{Player: "p", ProcessedSeq: acked, Position: pos})
			}
		})
	}
}

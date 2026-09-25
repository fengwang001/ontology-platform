package stat

import (
	"context"
	"errors"
	"math/rand"
	"sync"
	"testing"
	"time"

	"ontology/breaker"
	"ontology/classify"
)

type sharedClock struct{ t time.Time }

func (s *sharedClock) Now() time.Time { return s.t }

func breakerCfg(c breaker.Clock) breaker.Config {
	return breaker.Config{
		Fails: 5, MinSamples: 10, Rate: 0.5,
		Cooldown: time.Millisecond, MaxCooldown: 4 * time.Millisecond,
		Probes: 2, Window: 100, Clock: c,
	}
}

func TestStatTable(t *testing.T) {
	type tc struct {
		name string
		run  func(t *testing.T)
	}
	cases := []tc{
		{"invalid config and unknown downstream", func(t *testing.T) {
			p := New()
			bads := []Downstream{
				{Name: "a", Concurrency: 0, Queue: 1, CallTimeout: time.Second},
				{Name: "b", Concurrency: 1, Queue: 0, CallTimeout: time.Second},
				{Name: "c", Concurrency: 1, Queue: 1, CallTimeout: 0},
			}
			for _, b := range bads {
				b.Breaker = breakerCfg(nil)
				if err := p.Register(b); err == nil {
					t.Fatalf("expected error for %+v", b)
				}
			}
			if err := p.Do(context.Background(), "missing", nil); !errors.Is(err, ErrUnknownDownstream) {
				t.Fatalf("err=%v", err)
			}
		}},
		{"50k random calls keep every identity", func(t *testing.T) {
			p := New()
			clk := &sharedClock{t: time.Unix(0, 0)}
			if err := p.Register(Downstream{
				Name: "x", Concurrency: 4, Queue: 8,
				CallTimeout: 5 * time.Millisecond, Breaker: breakerCfg(clk),
			}); err != nil {
				t.Fatal(err)
			}
			const workers, per = 50, 1000
			var wg sync.WaitGroup
			for w := 0; w < workers; w++ {
				wg.Add(1)
				go func(seed int64) {
					defer wg.Done()
					r := rand.New(rand.NewSource(seed))
					for i := 0; i < per; i++ {
						k := r.Intn(6)
						_ = p.Do(context.Background(), "x", func(context.Context) error {
							switch k {
							case 0:
								return nil
							case 1:
								return errors.New("transient")
							case 2:
								return classify.Wrap(classify.NonRetryable, errors.New("400"))
							case 3:
								return classify.Wrap(classify.Timeout, context.DeadlineExceeded)
							case 4:
								panic("p")
							default:
								time.Sleep(8 * time.Millisecond)
								return nil
							}
						})
					}
				}(int64(w + 1))
			}
			wg.Wait()

			c := p.Snapshot()
			fail := c.Failure()
			if c.Total != workers*per {
				t.Fatalf("total=%d want %d", c.Total, workers*per)
			}
			if c.Total != c.Success+fail+c.BreakerRejected+c.BulkheadRejected {
				t.Fatalf("identity1: %d != %d+%d+%d+%d",
					c.Total, c.Success, fail, c.BreakerRejected, c.BulkheadRejected)
			}
			if c.Real != c.Total-c.BreakerRejected-c.BulkheadRejected {
				t.Fatalf("identity2: real=%d", c.Real)
			}
			catSum := c.FailRetryable + c.FailNonRetryable + c.FailTimeout + c.FailPanic
			if catSum != fail {
				t.Fatalf("identity3: %d != %d", catSum, fail)
			}
			t.Logf("total=%d real=%d success=%d fail=%d breakRej=%d bulkRej=%d",
				c.Total, c.Real, c.Success, fail, c.BreakerRejected, c.BulkheadRejected)
			t.Logf("categories retry=%d nonretry=%d timeout=%d panic=%d",
				c.FailRetryable, c.FailNonRetryable, c.FailTimeout, c.FailPanic)
		}},
	}
	for _, c := range cases {
		t.Run(c.name, c.run)
	}
}

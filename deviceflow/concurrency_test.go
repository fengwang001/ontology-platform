package deviceflow

import (
	"fmt"
	"math/rand"
	"sync"
	"sync/atomic"
	"testing"
)

// TestConcurrentPollSingleToken 同一设备码被并发轮询时恰有一个得到令牌。
func TestConcurrentPollSingleToken(t *testing.T) {
	s := mustNew(t, testConfig(uniqueGen()))
	st := mustStart(t, s, "c", 0)
	mustAuthorize(t, s, st.UserCode, true, 0)

	const n = 32
	var wg sync.WaitGroup
	var tokens, invalidGrants, other int64
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			res, err := s.Poll(st.DeviceCode, 1)
			if err != nil {
				atomic.AddInt64(&other, 1)
				return
			}
			switch res.Outcome {
			case OutcomeToken:
				atomic.AddInt64(&tokens, 1)
			case OutcomeInvalidGrant:
				atomic.AddInt64(&invalidGrants, 1)
			default:
				atomic.AddInt64(&other, 1)
			}
		}()
	}
	wg.Wait()
	if tokens != 1 || invalidGrants != n-1 || other != 0 {
		t.Fatalf("tokens=%d invalidGrants=%d other=%d, want 1/%d/0", tokens, invalidGrants, other, n-1)
	}
}

// TestConcurrentAuthorizeSingleWinner 同一用户码被并发批准与拒绝时
// 恰有一个成功。
func TestConcurrentAuthorizeSingleWinner(t *testing.T) {
	s := mustNew(t, testConfig(uniqueGen()))
	st := mustStart(t, s, "c", 0)

	const n = 32
	var wg sync.WaitGroup
	var successes, decided, other int64
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(approve bool) {
			defer wg.Done()
			err := s.Authorize(st.UserCode, approve, 0)
			if err == nil {
				atomic.AddInt64(&successes, 1)
				return
			}
			if k, _ := AsKind(err); k == KindAlreadyDecided {
				atomic.AddInt64(&decided, 1)
			} else {
				atomic.AddInt64(&other, 1)
			}
		}(i%2 == 0)
	}
	wg.Wait()
	if successes != 1 || decided != n-1 || other != 0 {
		t.Fatalf("successes=%d decided=%d other=%d, want 1/%d/0", successes, decided, other, n-1)
	}
	info := mustInterval(t, s, st.DeviceCode)
	if info.Status != StatusApproved && info.Status != StatusDenied {
		t.Fatalf("final status = %s, want approved or denied", info.Status)
	}
}

// TestConcurrentMixedStress 并发混合调用不 panic、无数据竞争（配合 -race）。
func TestConcurrentMixedStress(t *testing.T) {
	cfg := testConfig(uniqueGen())
	cfg.Cmax = 1000
	cfg.Z = 1000
	cfg.E = 100000
	s := mustNew(t, cfg)

	var clock atomic.Int64
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(seed int64) {
			defer wg.Done()
			r := rand.New(rand.NewSource(seed))
			var devices, codes []string
			for i := 0; i < 200; i++ {
				now := int64(clock.Add(1) - 1)
				client := fmt.Sprintf("c%d", r.Intn(4))
				switch r.Intn(5) {
				case 0:
					if res, err := s.Start(client, now); err == nil {
						devices = append(devices, res.DeviceCode)
						codes = append(codes, res.UserCode)
					}
				case 1:
					if len(devices) > 0 {
						_, _ = s.Poll(devices[r.Intn(len(devices))], now)
					}
				case 2:
					if len(codes) > 0 {
						_ = s.Authorize(codes[r.Intn(len(codes))], r.Intn(2) == 0, now)
					}
				case 3:
					if len(devices) > 0 {
						_, _ = s.Interval(devices[r.Intn(len(devices))])
					}
				case 4:
					_, _ = s.ClientInterval(client, now)
				}
			}
		}(int64(g) + 1)
	}
	wg.Wait()
}

package ontology

import (
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
)

func TestExpirationAndDecisionOrder(t *testing.T) {
	cfg := testConfig(nil)
	cfg.E = 10
	service, err := New(cfg)
	requireOK(t, err)
	start, err := service.Start([]byte("c"), 0)
	requireOK(t, err)
	requireOK(t, service.Authorize("ABCD1234", true, 1))
	result, err := service.Poll(start.DeviceCode, 12)
	requireOK(t, err)
	if result.Kind != PollExpired {
		t.Fatalf("approved expired kind = %s", result.Kind)
	}
	result, err = service.Poll(start.DeviceCode, 13)
	requireOK(t, err)
	if result.Kind != PollExpired {
		t.Fatalf("post-expiration kind = %s, want expired", result.Kind)
	}

	service, err = New(cfg)
	requireOK(t, err)
	start, err = service.Start([]byte("c"), 0)
	requireOK(t, err)
	requireError(t, service.Authorize("ABCD1234", true, 10), KindExpired)
	requireOK(t, service.Authorize("ABCD1234", true, 9))
	requireError(t, service.Authorize("ABCD1234", false, 9), KindAlreadyDecided)

	service, err = New(cfg)
	requireOK(t, err)
	start, err = service.Start([]byte("c"), 0)
	requireOK(t, err)
	_, err = service.Poll(start.DeviceCode, 0)
	requireOK(t, err)
	requireOK(t, service.Authorize("ABCD1234", false, 1))
	result, err = service.Poll(start.DeviceCode, 10)
	requireOK(t, err)
	if result.Kind != PollExpired {
		t.Fatalf("expired denied kind = %s, want expired", result.Kind)
	}
}

func TestRateLimitFormulaAndPrecedence(t *testing.T) {
	cfg := testConfig(nil)
	cfg.Z = 2
	service, err := New(cfg)
	requireOK(t, err)
	start, err := service.Start([]byte("c"), 0)
	requireOK(t, err)
	for _, now := range []int64{0, 3, 12, 20} {
		_, err = service.Poll(start.DeviceCode, now)
		requireOK(t, err)
	}
	detailed := requireError(t, func() error { _, e := service.Start([]byte("c"), 50); return e }(), KindRateLimited)
	if detailed.S != 3 || detailed.U != 112 {
		t.Fatalf("at 50 S=%d U=%d", detailed.S, detailed.U)
	}
	detailed = requireError(t, func() error { _, e := service.Start([]byte("c"), 105); return e }(), KindRateLimited)
	if detailed.S != 2 || detailed.U != 112 {
		t.Fatalf("at 105 S=%d U=%d", detailed.S, detailed.U)
	}
	service.gen = func() string { return "ZZZZ9999" }
	next, err := service.Start([]byte("c"), 112)
	requireOK(t, err)
	if next.Interval != 10 || next.DeviceCode != 2 {
		t.Fatalf("at 112 start = %+v", next)
	}
	service.gen = func() string { return "YYYY8888" }
	_, err = service.Start([]byte("c"), 113)
	requireOK(t, err)
}

func TestCapacityReleasesAndCounter(t *testing.T) {
	cfg := testConfig(codeGenerator("AAAA1111", "BBBB2222", "CCCC3333", "DDDD4444", "EEEE5555"))
	cfg.Cmax = 1
	cfg.E = 10
	service, err := New(cfg)
	requireOK(t, err)
	_, err = service.Start([]byte("c"), 0)
	requireOK(t, err)
	_, err = service.Start([]byte("c"), 1)
	requireError(t, err, KindCapacity)

	requireOK(t, service.Authorize("AAAA1111", false, 2))
	second, err := service.Start([]byte("c"), 3)
	requireOK(t, err)
	if second.DeviceCode != 2 {
		t.Fatalf("denied did not release slot: %+v", second)
	}

	third, err := service.Start([]byte("d"), 4)
	requireOK(t, err)
	if third.DeviceCode != 3 {
		t.Fatalf("different client got %+v", third)
	}
	requireOK(t, service.Authorize("CCCC3333", true, 5))
	result, err := service.Poll(third.DeviceCode, 5)
	requireOK(t, err)
	if result.Kind != PollToken || result.Token != 1 {
		t.Fatalf("token result = %+v", result)
	}
	fourth, err := service.Start([]byte("d"), 7)
	requireOK(t, err)
	if fourth.DeviceCode != 4 {
		t.Fatalf("consumed did not release: %+v", fourth)
	}
	_, err = service.Start([]byte("d"), 8)
	requireError(t, err, KindCapacity)
	fifth, err := service.Start([]byte("d"), 17)
	requireOK(t, err)
	if fifth.DeviceCode != 5 || fifth.Interval != 5 || service.expiryPops < 1 {
		t.Fatalf("expiry release result=%+v pops=%d", fifth, service.expiryPops)
	}

	service, err = New(cfg)
	requireOK(t, err)
	approved, err := service.Start([]byte("e"), 0)
	requireOK(t, err)
	requireOK(t, service.Authorize("AAAA1111", true, 1))
	_, err = service.Start([]byte("e"), 9)
	requireError(t, err, KindCapacity)
	_, err = service.Start([]byte("e"), 10)
	requireOK(t, err)
	result, err = service.Poll(approved.DeviceCode, 10)
	requireOK(t, err)
	if result.Kind != PollExpired {
		t.Fatalf("approved unclaimed expiration = %s", result.Kind)
	}
}

func TestDuplicateCodesUseOnlyUnexpiredRecords(t *testing.T) {
	cfg := testConfig(func() string { return "AAAA1111" })
	cfg.E = 10
	service, err := New(cfg)
	requireOK(t, err)
	_, err = service.Start([]byte("c"), 0)
	requireOK(t, err)
	requireOK(t, service.Authorize("aaaa-1111", false, 1))
	_, err = service.Start([]byte("c"), 2)
	requireError(t, err, KindGeneration)
	if service.genCalls != 100 {
		t.Fatalf("failed duplicate gen calls = %d", service.genCalls)
	}

	service, err = New(testConfig(codeGenerator("AAAA1111", "BBBB2222", "AAAA1111")))
	requireOK(t, err)
	service.cfg.E = 10
	first, err := service.Start([]byte("c"), 0)
	requireOK(t, err)
	requireOK(t, service.Authorize("AAAA1111", true, 1))
	if result, err := service.Poll(first.DeviceCode, 1); err != nil || result.Kind != PollToken {
		t.Fatalf("consume result=%+v err=%v", result, err)
	}
	second, err := service.Start([]byte("c"), 2)
	requireOK(t, err)
	if second.UserCode != "BBBB2222" || second.DeviceCode != 2 {
		t.Fatalf("consumed unexpired duplicate should force another code: %+v", second)
	}
	third, err := service.Start([]byte("c"), 10)
	requireOK(t, err)
	if third.UserCode != "AAAA1111" || third.DeviceCode != 3 {
		t.Fatalf("expired code reuse result = %+v", third)
	}
}

func TestRejectedOperationsDoNotAdvanceClock(t *testing.T) {
	service, err := New(testConfig(nil))
	requireOK(t, err)
	start, err := service.Start([]byte("c"), 10)
	requireOK(t, err)
	requireError(t, startError(service, nil, 11), KindInvalidArgument)
	requireError(t, startError(service, []byte("c"), 9), KindClockRewind)
	requireError(t, pollError(service, 999, 11), KindUnknownDevice)
	requireError(t, service.Authorize("ZZZZ9999", true, 11), KindNotFound)
	requireOK(t, service.Authorize("ABCD1234", true, 12))
	result, err := service.Poll(start.DeviceCode, 13)
	requireOK(t, err)
	if result.Kind != PollToken {
		t.Fatalf("rejected operations changed clock/state, kind=%s", result.Kind)
	}
}

func TestConcurrentPollAndAuthorize(t *testing.T) {
	service, err := New(testConfig(nil))
	requireOK(t, err)
	start, err := service.Start([]byte("c"), 0)
	requireOK(t, err)
	requireOK(t, service.Authorize("ABCD1234", true, 0))
	var wait sync.WaitGroup
	var tokens, invalid, waiting int64
	for range 64 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			result, pollErr := service.Poll(start.DeviceCode, 1)
			if pollErr != nil {
				t.Errorf("poll: %v", pollErr)
				return
			}
			switch result.Kind {
			case PollToken:
				atomic.AddInt64(&tokens, 1)
			case PollInvalid:
				atomic.AddInt64(&invalid, 1)
			case PollWaiting:
				atomic.AddInt64(&waiting, 1)
			}
		}()
	}
	wait.Wait()
	if tokens != 1 || tokens+invalid+waiting != 64 {
		t.Fatalf("tokens=%d invalid=%d waiting=%d", tokens, invalid, waiting)
	}

	service, err = New(testConfig(codeGenerator("AAAA1111", "BBBB2222")))
	requireOK(t, err)
	first, err := service.Start([]byte("c"), 0)
	requireOK(t, err)
	second, err := service.Start([]byte("c"), 1)
	requireOK(t, err)
	var approvals, denials int64
	for range 32 {
		wait.Add(2)
		go func() {
			defer wait.Done()
			if service.Authorize("AAAA1111", true, 2) == nil {
				atomic.AddInt64(&approvals, 1)
			}
		}()
		go func() {
			defer wait.Done()
			if service.Authorize("AAAA1111", false, 2) == nil {
				atomic.AddInt64(&denials, 1)
			}
		}()
	}
	wait.Wait()
	if approvals+denials != 1 {
		t.Fatalf("approvals=%d denials=%d", approvals, denials)
	}
	if _, err := service.Poll(first.DeviceCode, 3); err != nil {
		t.Fatalf("first poll: %v", err)
	}
	if _, err := service.Poll(second.DeviceCode, 3); err != nil {
		t.Fatalf("second poll: %v", err)
	}
}

func TestInvalidConfigs(t *testing.T) {
	valid := testConfig(nil)
	mutate := func(change func(*Config)) Config {
		cfg := valid
		change(&cfg)
		return cfg
	}
	for index, cfg := range []Config{
		valid,
		mutate(func(c *Config) { c.E = 0 }),
		mutate(func(c *Config) { c.I0 = 0 }),
		mutate(func(c *Config) { c.D = 0 }),
		mutate(func(c *Config) { c.Imax = 0 }),
		mutate(func(c *Config) { c.H = 0 }),
		mutate(func(c *Config) { c.Cmax = 0 }),
		mutate(func(c *Config) { c.Z = 0 }),
		mutate(func(c *Config) { c.Imax = c.I0 - 1 }),
		mutate(func(c *Config) { c.Gen = nil }),
	} {
		_, err := New(cfg)
		if index == 0 && err != nil {
			t.Fatalf("valid config rejected: %v", err)
		}
		if index > 0 && err == nil {
			t.Fatalf("invalid config %d accepted", index)
		}
	}
}

func TestDetailedErrorKinds(t *testing.T) {
	err := fmt.Errorf("wrapped: %w", &DetailedError{Kind: KindExpired})
	if !errors.Is(err, &DetailedError{Kind: KindExpired}) {
		t.Fatalf("errors.Is did not match wrapped detailed error")
	}
}

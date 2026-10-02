package ontology

import (
	"errors"
	"fmt"
	"testing"
)

func testConfig(generator func() string) Config {
	if generator == nil {
		generator = func() string { return "ABCD1234" }
	}
	return Config{E: 600, I0: 5, D: 5, Imax: 20, H: 100, Cmax: 5, Z: 3, Gen: generator}
}

func errorKind(err error) string {
	var detailed *DetailedError
	if errors.As(err, &detailed) {
		return detailed.Kind
	}
	return ""
}

func requireError(t *testing.T, err error, want string) *DetailedError {
	t.Helper()
	var detailed *DetailedError
	if !errors.As(err, &detailed) || detailed.Kind != want {
		t.Fatalf("error kind = %q, want %q (err=%v)", errorKind(err), want, err)
	}
	return detailed
}

func requireOK(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func startError(service *Service, client []byte, now int64) error {
	_, err := service.Start(client, now)
	return err
}

func pollError(service *Service, deviceCode uint64, now int64) error {
	_, err := service.Poll(deviceCode, now)
	return err
}

func codeGenerator(values ...string) func() string {
	index := 0
	return func() string {
		value := values[index]
		index++
		if index == len(values) {
			index = 0
		}
		return value
	}
}

func TestNormalizationAndGeneration(t *testing.T) {
	normalized, ok := NormalizeUserCode("ab-cd-ef-gh")
	if !ok || normalized != "ABCDEFGH" {
		t.Fatalf("NormalizeUserCode = %q, %v", normalized, ok)
	}
	for _, code := range []string{"", "ABC", "ABCDEFGHIJKLMNOPQ", "AB!D", "AB.CD"} {
		if _, ok := NormalizeUserCode(code); ok {
			t.Fatalf("NormalizeUserCode(%q) unexpectedly succeeded", code)
		}
	}

	service, err := New(testConfig(codeGenerator("ABCD-EFGH", "abcdefgh", "WXYZ-1234")))
	requireOK(t, err)
	first, err := service.Start([]byte("c"), 0)
	requireOK(t, err)
	if first.UserCode != "ABCD-EFGH" || first.DeviceCode != 1 || first.Interval != 5 || first.ExpiresAt != 600 {
		t.Fatalf("unexpected first start: %+v", first)
	}
	second, err := service.Start([]byte("c"), 1)
	requireOK(t, err)
	if second.UserCode != "WXYZ-1234" || second.DeviceCode != 2 {
		t.Fatalf("retry generation: %+v", second)
	}
	if service.genCalls != 2 {
		t.Fatalf("genCalls = %d, want 2", service.genCalls)
	}
	requireOK(t, service.Authorize("ab-cd-ef-gh", true, 2))

	service, err = New(testConfig(func() string { return "bad!" }))
	requireOK(t, err)
	_, err = service.Start([]byte("c"), 0)
	requireError(t, err, KindGeneration)
	if service.genCalls != 100 {
		t.Fatalf("genCalls = %d, want 100", service.genCalls)
	}
}

func TestPollingIntervalEscalation(t *testing.T) {
	service, err := New(testConfig(nil))
	requireOK(t, err)
	start, err := service.Start([]byte("c"), 0)
	requireOK(t, err)
	checks := []struct {
		now      int64
		kind     string
		interval int64
		next     int64
	}{
		{0, PollWaiting, 5, 5},
		{5, PollWaiting, 5, 10},
		{9, PollTooFast, 10, 19},
		{18, PollTooFast, 15, 33},
		{32, PollTooFast, 20, 52},
		{51, PollTooFast, 20, 71},
		{71, PollWaiting, 20, 91},
	}
	for _, check := range checks {
		result, err := service.Poll(start.DeviceCode, check.now)
		requireOK(t, err)
		if result.Kind != check.kind || result.Interval != check.interval || result.NextAllowed != check.next {
			t.Fatalf("at %d result=%+v, want kind=%s interval=%d next=%d", check.now, result, check.kind, check.interval, check.next)
		}
	}
	interval, err := service.ClientInterval([]byte("c"), 100)
	requireOK(t, err)
	if interval.S != 4 || interval.Interval != 20 || !interval.RateLimited || interval.U != 118 {
		t.Fatalf("window at 100 = %+v", interval)
	}
	interval, err = service.ClientInterval([]byte("c"), 108)
	requireOK(t, err)
	if interval.S != 4 || !interval.RateLimited || interval.U != 118 {
		t.Fatalf("window at 108 = %+v", interval)
	}
	interval, err = service.ClientInterval([]byte("c"), 109)
	requireOK(t, err)
	if interval.S != 3 || interval.Interval != 20 {
		t.Fatalf("window at 109 = %+v", interval)
	}
}

func TestTooFastPrecedesStatusAndOneShotResults(t *testing.T) {
	for _, approve := range []bool{true, false} {
		t.Run(fmt.Sprintf("approve=%v", approve), func(t *testing.T) {
			service, err := New(testConfig(nil))
			requireOK(t, err)
			start, err := service.Start([]byte("c"), 0)
			requireOK(t, err)
			if result, err := service.Poll(start.DeviceCode, 0); err != nil || result.Kind != PollWaiting {
				t.Fatalf("initial poll result=%+v err=%v", result, err)
			}
			requireOK(t, service.Authorize("ABCD1234", approve, 1))
			result, err := service.Poll(start.DeviceCode, 3)
			requireOK(t, err)
			if result.Kind != PollTooFast || result.Interval != 10 || result.NextAllowed != 13 {
				t.Fatalf("premature decided poll = %+v", result)
			}
			result, err = service.Poll(start.DeviceCode, 13)
			requireOK(t, err)
			want := PollAccessDenied
			if approve {
				want = PollToken
				if result.Token != 1 {
					t.Fatalf("token = %d", result.Token)
				}
			}
			if result.Kind != want {
				t.Fatalf("kind = %s, want %s", result.Kind, want)
			}
			result, err = service.Poll(start.DeviceCode, 14)
			requireOK(t, err)
			if result.Kind != PollInvalid {
				t.Fatalf("second result = %s, want invalid", result.Kind)
			}
		})
	}
}

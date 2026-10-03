package pushregistry

import (
	"errors"
	"strconv"
	"sync"
	"testing"
)

func newRegistryForTest(t *testing.T, deviceLimit, retainedTokens int, grace, block int64) *Registry {
	t.Helper()
	registry, err := NewRegistry(deviceLimit, retainedTokens, grace, block)
	if err != nil {
		t.Fatalf("NewRegistry() error = %v", err)
	}
	return registry
}

func mustRegister(t *testing.T, registry *Registry, user, device, token string, now int64) {
	t.Helper()
	if err := registry.Register([]byte(user), []byte(device), []byte(token), now); err != nil {
		t.Fatalf("Register(%q, %q, %q, %d) error = %v", user, device, token, now, err)
	}
}

func mustFeedback(t *testing.T, registry *Registry, token string, now int64) {
	t.Helper()
	if err := registry.Feedback([]byte(token), now); err != nil {
		t.Fatalf("Feedback(%q, %d) error = %v", token, now, err)
	}
}

func mustTouch(t *testing.T, registry *Registry, user, device string, now int64) {
	t.Helper()
	if err := registry.Touch([]byte(user), []byte(device), now); err != nil {
		t.Fatalf("Touch(%q, %q, %d) error = %v", user, device, now, err)
	}
}

func mustUnregister(t *testing.T, registry *Registry, user, device string, now int64) {
	t.Helper()
	if err := registry.Unregister([]byte(user), []byte(device), now); err != nil {
		t.Fatalf("Unregister(%q, %q, %d) error = %v", user, device, now, err)
	}
}

func assertErrorIs(t *testing.T, got, want error) {
	t.Helper()
	if !errors.Is(got, want) {
		t.Fatalf("error = %v, want %v", got, want)
	}
}

func assertTargets(t *testing.T, registry *Registry, user string, now int64, want ...string) {
	t.Helper()
	got, err := registry.Targets([]byte(user), now)
	if err != nil {
		t.Fatalf("Targets(%q, %d) error = %v", user, now, err)
	}
	gotStrings := bytesToStrings(got)
	if len(gotStrings) != len(want) {
		t.Fatalf("Targets(%q, %d) = %v, want %v", user, now, gotStrings, want)
	}
	for i := range want {
		if gotStrings[i] != want[i] {
			t.Fatalf("Targets(%q, %d) = %v, want %v", user, now, gotStrings, want)
		}
	}
}

func bytesToStrings(values [][]byte) []string {
	result := make([]string, len(values))
	for i, value := range values {
		result[i] = string(value)
	}
	return result
}

func TestSpecExample(t *testing.T) {
	registry := newRegistryForTest(t, 2, 2, 10, 20)

	mustRegister(t, registry, "u1", "d1", "t1", 1)
	mustRegister(t, registry, "u1", "d2", "t2", 2)
	mustRegister(t, registry, "u1", "d3", "t3", 3)
	assertTargets(t, registry, "u1", 3, "t3", "t2")

	mustRegister(t, registry, "u2", "d9", "t3", 4)
	assertTargets(t, registry, "u1", 4, "t2")
	assertTargets(t, registry, "u2", 4, "t3")

	mustRegister(t, registry, "u1", "d2", "t2b", 5)
	assertTargets(t, registry, "u1", 5, "t2b", "t2")
	assertTargets(t, registry, "u1", 14, "t2b", "t2")
	assertTargets(t, registry, "u1", 15, "t2b")

	mustFeedback(t, registry, "t2b", 16)
	assertErrorIs(t, registry.Register([]byte("u1"), []byte("d4"), []byte("t2b"), 30), ErrTokenBlocked)
	assertTargets(t, registry, "u1", 30)
	mustRegister(t, registry, "u1", "d4", "t2b", 36)
	assertTargets(t, registry, "u1", 36, "t2b")
}

func TestDuplicateCurrentRefreshesLastSeen(t *testing.T) {
	registry := newRegistryForTest(t, 2, 2, 10, 20)
	mustRegister(t, registry, "u", "d1", "t1", 1)
	mustRegister(t, registry, "u", "d2", "t2", 2)
	mustRegister(t, registry, "u", "d1", "t1", 3)

	bound := registry.users["u"].bindings["d1"]
	if bound.lastSeen != 3 {
		t.Fatalf("lastSeen = %d, want 3", bound.lastSeen)
	}
	if len(bound.old) != 0 {
		t.Fatalf("old tokens = %v, want empty", bound.old)
	}
	assertTargets(t, registry, "u", 3, "t1", "t2")
}

func TestCurrentTokenTransferDeletesSourceBinding(t *testing.T) {
	registry := newRegistryForTest(t, 3, 2, 10, 20)
	mustRegister(t, registry, "u1", "d1", "t1", 1)
	mustRegister(t, registry, "u1", "d2", "t2", 2)
	mustRegister(t, registry, "u2", "d3", "t3", 3)
	mustRegister(t, registry, "u2", "d4", "t2", 4)

	if registry.findBinding("u1", "d2") != nil {
		t.Fatal("source binding still exists")
	}
	if _, exists := registry.tokenOwners["t3"]; !exists {
		t.Fatal("unrelated token lost ownership")
	}
	assertTargets(t, registry, "u1", 4, "t1")
	assertTargets(t, registry, "u2", 4, "t2", "t3")
}

func TestCurrentTokenTransferSameUserCountsAfterRemoval(t *testing.T) {
	registry := newRegistryForTest(t, 2, 2, 10, 20)
	mustRegister(t, registry, "u", "d1", "t1", 1)
	mustRegister(t, registry, "u", "d2", "t2", 2)
	mustRegister(t, registry, "u", "d2", "t1", 3)

	if registry.findBinding("u", "d1") != nil {
		t.Fatal("source binding still exists")
	}
	if len(registry.users["u"].bindings) != 1 {
		t.Fatalf("device count = %d, want 1", len(registry.users["u"].bindings))
	}
	assertTargets(t, registry, "u", 3, "t1", "t2")
}

func TestOldTokenTransferOnlyRemovesOldToken(t *testing.T) {
	registry := newRegistryForTest(t, 3, 2, 10, 20)
	mustRegister(t, registry, "u1", "d1", "t1", 1)
	mustRegister(t, registry, "u1", "d1", "t1b", 2)
	mustRegister(t, registry, "u2", "d2", "t2", 3)
	mustRegister(t, registry, "u2", "d2", "t1", 4)

	source := registry.findBinding("u1", "d1")
	if source == nil {
		t.Fatal("source binding deleted")
	}
	if len(source.old) != 0 {
		t.Fatalf("source old tokens = %v, want empty", source.old)
	}
	if source.cur != "t1b" {
		t.Fatalf("source current = %q, want t1b", source.cur)
	}
	assertTargets(t, registry, "u2", 4, "t1", "t2")
}

func TestOldTokenReturnsToCurrentOnSameDevice(t *testing.T) {
	registry := newRegistryForTest(t, 1, 2, 10, 20)
	mustRegister(t, registry, "u", "d", "t1", 1)
	mustRegister(t, registry, "u", "d", "t2", 2)
	mustRegister(t, registry, "u", "d", "t1", 3)

	bound := registry.findBinding("u", "d")
	if bound.cur != "t1" {
		t.Fatalf("current = %q, want t1", bound.cur)
	}
	if len(bound.old) != 1 || bound.old[0].token != "t2" || bound.old[0].retiredAt != 3 {
		t.Fatalf("old tokens = %+v, want only t2 retired at 3", bound.old)
	}
	assertTargets(t, registry, "u", 3, "t1", "t2")
}

func TestZeroRetentionReleasesImmediately(t *testing.T) {
	registry := newRegistryForTest(t, 1, 0, 10, 20)
	mustRegister(t, registry, "u", "d", "t1", 1)
	mustRegister(t, registry, "u", "d", "t2", 2)

	if _, exists := registry.tokenOwners["t1"]; exists {
		t.Fatal("retired token retained with R=0")
	}
	assertTargets(t, registry, "u", 2, "t2")
	mustRegister(t, registry, "other", "d", "t1", 3)
	assertTargets(t, registry, "other", 3, "t1")
}

func TestRetentionReleasesOldest(t *testing.T) {
	registry := newRegistryForTest(t, 1, 2, 100, 20)
	mustRegister(t, registry, "u", "d", "t1", 1)
	mustRegister(t, registry, "u", "d", "t2", 2)
	mustRegister(t, registry, "u", "d", "t3", 3)
	mustRegister(t, registry, "u", "d", "t4", 4)

	bound := registry.findBinding("u", "d")
	if len(bound.old) != 2 || bound.old[0].token != "t3" || bound.old[1].token != "t2" {
		t.Fatalf("old tokens = %+v, want t3 then t2", bound.old)
	}
	if _, exists := registry.tokenOwners["t1"]; exists {
		t.Fatal("oldest token still owned")
	}
	assertTargets(t, registry, "u", 4, "t4", "t3", "t2")
}

func TestGraceBoundaryAndExpiredTokenStillOccupiesSlot(t *testing.T) {
	registry := newRegistryForTest(t, 1, 1, 10, 20)
	mustRegister(t, registry, "u", "d", "t1", 1)
	mustRegister(t, registry, "u", "d", "t2", 2)

	assertTargets(t, registry, "u", 11, "t2", "t1")
	assertTargets(t, registry, "u", 12, "t2")

	mustRegister(t, registry, "u", "d", "t3", 20)
	bound := registry.findBinding("u", "d")
	if len(bound.old) != 1 || bound.old[0].token != "t2" {
		t.Fatalf("old tokens = %+v, expired t1 should keep its R slot before rotation", bound.old)
	}

	mustRegister(t, registry, "other", "device", "t2", 21)
	if len(registry.findBinding("u", "d").old) != 0 {
		t.Fatal("expired old token transfer did not remove it")
	}
	assertTargets(t, registry, "u", 21, "t3")
	assertTargets(t, registry, "other", 21, "t2")
}

func TestEvictionUsesLastSeenThenDeviceName(t *testing.T) {
	registry := newRegistryForTest(t, 2, 2, 10, 20)
	mustRegister(t, registry, "u", "b", "tb", 1)
	mustRegister(t, registry, "u", "a", "ta", 2)
	mustTouch(t, registry, "u", "b", 2)

	mustRegister(t, registry, "u", "c", "tc", 3)

	if registry.findBinding("u", "a") != nil {
		t.Fatal("lexicographically smaller tied device was not evicted")
	}
	if registry.findBinding("u", "b") == nil || registry.findBinding("u", "c") == nil {
		t.Fatal("wrong devices retained")
	}

	registry2 := newRegistryForTest(t, 2, 0, 10, 20)
	mustRegister(t, registry2, "u", "a", "ta", 1)
	mustRegister(t, registry2, "u", "b", "tb", 2)
	mustRegister(t, registry2, "other", "device", "ta", 3)

	if _, exists := registry2.tokenOwners["ta"]; !exists {
		t.Fatal("evicted token was not released")
	}
	assertTargets(t, registry2, "other", 3, "ta")
}

func TestFeedbackAndBlockWindow(t *testing.T) {
	registry := newRegistryForTest(t, 2, 2, 10, 20)
	mustRegister(t, registry, "u", "d1", "t1", 1)
	mustRegister(t, registry, "u", "d1", "t1b", 2)
	mustRegister(t, registry, "u", "d2", "t2", 3)

	mustFeedback(t, registry, "t1", 4)
	if len(registry.findBinding("u", "d1").old) != 0 {
		t.Fatal("feedback on old token did not remove just that old token")
	}
	assertErrorIs(t, registry.Feedback([]byte("missing"), 5), ErrTokenNotFound)

	mustFeedback(t, registry, "t1b", 6)
	if registry.findBinding("u", "d1") != nil {
		t.Fatal("feedback on current token did not delete binding")
	}
	if _, exists := registry.tokenOwners["t1b"]; exists {
		t.Fatal("feedback token remains owned")
	}
	if registry.blocks["t1b"] != 26 {
		t.Fatalf("block until = %d, want 26", registry.blocks["t1b"])
	}
	assertErrorIs(t, registry.Register([]byte("u"), []byte("d3"), []byte("t1b"), 25), ErrTokenBlocked)
	mustRegister(t, registry, "u", "d3", "t1b", 26)
}

func TestZeroBlockAllowsImmediateReregistration(t *testing.T) {
	registry := newRegistryForTest(t, 1, 0, 0, 0)
	mustRegister(t, registry, "u", "d", "tok", 1)
	mustFeedback(t, registry, "tok", 2)
	mustRegister(t, registry, "u", "d", "tok", 3)
	assertTargets(t, registry, "u", 3, "tok")
}

func TestRejectedOperationsDoNotMutateState(t *testing.T) {
	registry := newRegistryForTest(t, 1, 2, 10, 20)
	mustRegister(t, registry, "u", "d", "tok", 5)
	mustRegister(t, registry, "u", "d", "new", 6)

	assertErrorIs(t, registry.Register([]byte("u"), []byte("d"), []byte("x"), 4), ErrClockRollback)
	assertErrorIs(t, registry.Register(nil, []byte("d"), []byte("x"), 7), ErrInvalidArgument)
	assertErrorIs(t, registry.Touch([]byte("u"), []byte("missing"), 7), ErrDeviceNotFound)
	assertErrorIs(t, registry.Unregister([]byte("u"), []byte("missing"), 7), ErrDeviceNotFound)

	mustFeedback(t, registry, "tok", 8)
	assertErrorIs(t, registry.Register([]byte("u"), []byte("other"), []byte("tok"), 9), ErrTokenBlocked)

	bound := registry.findBinding("u", "d")
	if bound.cur != "new" || bound.lastSeen != 6 || len(bound.old) != 0 {
		t.Fatalf("binding changed after rejections: %+v", bound)
	}
	if len(registry.users["u"].bindings) != 1 {
		t.Fatalf("device count changed: %d", len(registry.users["u"].bindings))
	}
	if registry.maxNow != 8 {
		t.Fatalf("maxNow = %d, want 8", registry.maxNow)
	}
}

func TestUnregisterReleasesAllTokensWithoutBlocking(t *testing.T) {
	registry := newRegistryForTest(t, 1, 2, 10, 20)
	mustRegister(t, registry, "u", "d", "t1", 1)
	mustRegister(t, registry, "u", "d", "t2", 2)
	mustUnregister(t, registry, "u", "d", 3)

	if _, exists := registry.users["u"]; exists {
		t.Fatal("empty user index retained")
	}
	if len(registry.tokenOwners) != 0 {
		t.Fatalf("owners remain: %v", registry.tokenOwners)
	}
	mustRegister(t, registry, "other", "d", "t1", 4)
	mustRegister(t, registry, "other", "d2", "t2", 5)
}

func TestRejectionPriority(t *testing.T) {
	registry := newRegistryForTest(t, 1, 0, 0, 0)
	assertErrorIs(t, registry.Register(nil, []byte("d"), []byte("tok"), -1), ErrInvalidArgument)
	assertErrorIs(t, registry.Touch(nil, []byte("d"), -1), ErrInvalidArgument)
	assertErrorIs(t, registry.Unregister(nil, []byte("d"), -1), ErrInvalidArgument)
	assertErrorIs(t, registry.Feedback(nil, -1), ErrInvalidArgument)
	_, err := registry.Targets(nil, -1)
	assertErrorIs(t, err, ErrInvalidArgument)

	mustRegister(t, registry, "u", "d", "tok", 5)
	assertErrorIs(t, registry.Touch([]byte("u"), []byte("missing"), 4), ErrClockRollback)
	assertErrorIs(t, registry.Unregister([]byte("u"), []byte("missing"), 4), ErrClockRollback)
	assertErrorIs(t, registry.Feedback([]byte("missing"), 4), ErrClockRollback)
}

func TestConcurrentOperations(t *testing.T) {
	registry := newRegistryForTest(t, 64, 8, 1_000_000_000, 1_000_000_000)
	var wait sync.WaitGroup
	for worker := 0; worker < 16; worker++ {
		wait.Add(1)
		go func(worker int) {
			defer wait.Done()
			for i := 0; i < 100; i++ {
				now := int64(1)
				user := "u"
				device := "d" + strconv.Itoa(worker*100+i)
				token := "t" + strconv.Itoa(worker*100+i)
				_ = registry.Register([]byte(user), []byte(device), []byte(token), now)
				_, _ = registry.Targets([]byte(user), 1_000_000_000_000)
			}
		}(worker)
	}
	wait.Wait()

	if len(registry.users["u"].bindings) > 64 {
		t.Fatalf("device count = %d", len(registry.users["u"].bindings))
	}
	seen := make(map[string]bool)
	for _, bound := range registry.users["u"].bindings {
		for _, token := range append([]string{bound.cur}, oldTokenNames(bound.old)...) {
			if seen[token] {
				t.Fatalf("token %q owned twice", token)
			}
			seen[token] = true
		}
	}
}

func TestTargetLookupCounterIsPerUser(t *testing.T) {
	registry := newRegistryForTest(t, 8, 2, 100, 0)
	mustRegister(t, registry, "a", "d1", "a1", 1)
	mustRegister(t, registry, "a", "d1", "a2", 2)
	mustRegister(t, registry, "b", "d1", "b1", 3)
	mustRegister(t, registry, "b", "d2", "b2", 4)
	mustRegister(t, registry, "c", "d1", "c1", 5)

	_, err := registry.Targets([]byte("a"), 6)
	if err != nil {
		t.Fatal(err)
	}
	if got := registry.targetLookups["a"]; got != 2 {
		t.Fatalf("targetLookups[a] = %d, want one current plus one old = 2", got)
	}
	if got := registry.targetLookups["b"]; got != 0 {
		t.Fatalf("targetLookups[b] = %d, want 0", got)
	}

	_, err = registry.Targets([]byte("missing"), 6)
	if err != nil {
		t.Fatal(err)
	}
	if got := registry.targetLookups["missing"]; got != 0 {
		t.Fatalf("targetLookups[missing] = %d, want 0", got)
	}
}

func oldTokenNames(items []oldToken) []string {
	names := make([]string, 0, len(items))
	for _, item := range items {
		names = append(names, item.token)
	}
	return names
}

func TestSkeleton(t *testing.T) {
	registry, err := NewRegistry(1, 0, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if registry == nil {
		t.Fatal("nil registry")
	}
}

func TestErrorsAreDistinguishable(t *testing.T) {
	known := []error{
		ErrInvalidArgument,
		ErrClockRollback,
		ErrTokenBlocked,
		ErrDeviceNotFound,
		ErrTokenNotFound,
	}
	for i, first := range known {
		for _, second := range known[i+1:] {
			if errors.Is(first, second) {
				t.Fatalf("%v unexpectedly matches %v", first, second)
			}
		}
	}
}

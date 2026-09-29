package ontology

import (
	"fmt"
	"strings"
	"sync"
	"testing"
)

func newLoggedSampler[V any](t *testing.T, rate int, opts ...Option[V]) *Sampler[V] {
	t.Helper()
	logf := func(format string, args ...any) { t.Logf(format, args...) }
	opts = append([]Option[V]{WithLogger[V](logf)}, opts...)
	s, rerr := NewSampler[V](rate, opts...)
	if rerr != nil {
		t.Fatalf("NewSampler(%d): unexpected error: %v", rate, rerr)
	}
	return s
}

func events(keys ...string) []Event[string] {
	evs := make([]Event[string], len(keys))
	for i, key := range keys {
		evs[i] = Event[string]{Key: key, Value: "v-" + key}
	}
	return evs
}

func intEvents(keys ...string) []Event[int] {
	evs := make([]Event[int], len(keys))
	for i, key := range keys {
		evs[i] = Event[int]{Key: key, Value: i}
	}
	return evs
}

func naiveExpected(evs []Event[string], rate int) []Event[string] {
	want := make([]Event[string], 0)
	for _, ev := range evs {
		if HashBucket(ev.Key) < rate {
			want = append(want, ev)
		}
	}
	return want
}

func assertEventsEqual(t *testing.T, got, want []Event[string]) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("kept length = %d, want %d (got=%v want=%v)", len(got), len(want), got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("kept[%d] = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func containsKey(list []string, target string) bool {
	for _, item := range list {
		if item == target {
			return true
		}
	}
	return false
}

// TestSameKeyAllOrNothing：同一键的全部事件要么全留、要么全丢，输出保持原顺序，
// 且与朴素判定 bucket < rate 完全一致。
func TestSameKeyAllOrNothing(t *testing.T) {
	s := newLoggedSampler[string](t, 100)
	src := events("alpha", "beta", "alpha", "gamma", "beta", "alpha")

	kept, rerr := s.Feed(src)
	if rerr != nil {
		t.Fatalf("Feed: unexpected error: %v", rerr)
	}
	assertEventsEqual(t, kept, naiveExpected(src, 100))

	total := map[string]int{}
	for _, ev := range src {
		total[ev.Key]++
	}
	keptCount := map[string]int{}
	for _, ev := range kept {
		keptCount[ev.Key]++
	}
	for key, n := range total {
		bucket := HashBucket(key)
		if bucket < 100 {
			if keptCount[key] != n {
				t.Fatalf("key %q bucket=%d should keep all %d events, kept %d",
					key, bucket, n, keptCount[key])
			}
		} else if keptCount[key] != 0 {
			t.Fatalf("key %q bucket=%d should drop all events, kept %d",
				key, bucket, keptCount[key])
		}
		got, err := s.ShouldSample(key)
		if err != nil {
			t.Fatalf("ShouldSample(%q): %v", key, err)
		}
		if got != (bucket < 100) {
			t.Fatalf("ShouldSample(%q) = %t, bucket=%d rate=100", key, got, bucket)
		}
	}

	idx := 0
	for _, ev := range src {
		if HashBucket(ev.Key) < 100 {
			if kept[idx] != ev {
				t.Fatalf("order broken at %d: got %+v want %+v", idx, kept[idx], ev)
			}
			idx++
		}
	}

	if s.KnownKeys() != 3 {
		t.Fatalf("KnownKeys = %d, want 3", s.KnownKeys())
	}
	if err := s.SelfCheck(); err != nil {
		t.Fatalf("SelfCheck: %v", err)
	}
}

// TestRejectedInputLeavesNoTrace：三类拒绝原因互不相同、可区分，且拒绝不改变状态。
func TestRejectedInputLeavesNoTrace(t *testing.T) {
	s := newLoggedSampler[int](t, 10, WithMaxKnownKeys[int](2))
	if _, err := s.Feed(intEvents("k1")); err != nil {
		t.Fatalf("seed Feed: %v", err)
	}

	rateBefore, knownBefore := s.Rate(), s.KnownKeys()

	// 空键：批中任一事件为空即整体拒绝，合法的新键也不得登记。
	_, errEmpty := s.Feed(intEvents("k9", "", "brand-new"))
	if errEmpty == nil || errEmpty.Reason != ReasonEmptyKey {
		t.Fatalf("empty-key rejection = %v, want %s", errEmpty, ReasonEmptyKey)
	}

	// 超限：容量 2，已有 1 个已知键，一批 2 个新键 => 1+2 > 2，整体拒绝。
	_, errCap := s.Feed(intEvents("n1", "n2"))
	if errCap == nil || errCap.Reason != ReasonTooManyKnownKeys {
		t.Fatalf("capacity rejection = %v, want %s", errCap, ReasonTooManyKnownKeys)
	}

	// 超限边界：恰好达到容量必须接受。
	if _, err := s.Feed(intEvents("n1")); err != nil {
		t.Fatalf("Feed up to capacity should succeed, got %v", err)
	}
	_, errCap2 := s.Feed(intEvents("n2"))
	if errCap2 == nil || errCap2.Reason != ReasonTooManyKnownKeys {
		t.Fatalf("over-capacity rejection = %v, want %s", errCap2, ReasonTooManyKnownKeys)
	}

	// 已知键重复出现不占新名额。
	if _, err := s.Feed(intEvents("k1", "k1", "n1")); err != nil {
		t.Fatalf("known-key repeats must not count against capacity, got %v", err)
	}

	// 采样率越界：负、超上限均拒绝，当前采样率不变。
	for _, bad := range []int{-1, MaxRate + 1, 1 << 30} {
		_, errAdj := s.AdjustRate(bad)
		if errAdj == nil || errAdj.Reason != ReasonRateOutOfRange {
			t.Fatalf("AdjustRate(%d) = %v, want %s", bad, errAdj, ReasonRateOutOfRange)
		}
	}

	// ShouldSample 空键拒绝且不登记。
	_, errQuery := s.ShouldSample("")
	if errQuery == nil || errQuery.Reason != ReasonEmptyKey {
		t.Fatalf("ShouldSample(\"\") = %v, want %s", errQuery, ReasonEmptyKey)
	}

	if s.Rate() != rateBefore {
		t.Fatalf("rate changed after rejections: %d -> %d", rateBefore, s.Rate())
	}
	if s.KnownKeys() != knownBefore+1 {
		t.Fatalf("known keys changed unexpectedly: now %d, want %d",
			s.KnownKeys(), knownBefore+1)
	}

	// 三类原因互不相同。
	reasons := []RejectReason{ReasonRateOutOfRange, ReasonEmptyKey, ReasonTooManyKnownKeys}
	for i := range reasons {
		for j := i + 1; j < len(reasons); j++ {
			if reasons[i] == reasons[j] || reasons[i].String() == reasons[j].String() {
				t.Fatalf("reject reasons must be distinct: %s vs %s", reasons[i], reasons[j])
			}
		}
	}

	// AsReject 能从普通 error 接口提取原因。
	var asError error = &RejectError{Reason: ReasonEmptyKey}
	if got := AsReject(asError); got == nil || got.Reason != ReasonEmptyKey {
		t.Fatalf("AsReject extraction failed: %v", got)
	}
	if AsReject(nil) != nil {
		t.Fatalf("AsReject(nil) must be nil")
	}

	// 构造期非法参数同样被拒绝。
	if _, err := NewSampler[int](-1); err == nil || err.Reason != ReasonRateOutOfRange {
		t.Fatalf("NewSampler(-1) = %v, want %s", err, ReasonRateOutOfRange)
	}
	if _, err := NewSampler[int](0, WithMaxKnownKeys[int](0)); err == nil ||
		err.Reason != ReasonTooManyKnownKeys {
		t.Fatalf("NewSampler max=0 = %v, want %s", err, ReasonTooManyKnownKeys)
	}
}

// TestStepLogging：日志必须包含每步输入、桶值与判定依据。
func TestStepLogging(t *testing.T) {
	var sb strings.Builder
	s, err := NewSampler[string](50, WithLogger[string](
		func(format string, args ...any) {
			fmt.Fprintf(&sb, format+"\n", args...)
		}))
	if err != nil {
		t.Fatalf("NewSampler: %v", err)
	}
	if _, rerr := s.Feed(events("log-key")); rerr != nil {
		t.Fatalf("Feed: %v", rerr)
	}
	if _, rerr := s.AdjustRate(100); rerr != nil {
		t.Fatalf("AdjustRate: %v", rerr)
	}
	if _, rerr := s.ShouldSample("log-key"); rerr != nil {
		t.Fatalf("ShouldSample: %v", rerr)
	}
	if rerr := s.SelfCheck(); rerr != nil {
		t.Fatalf("SelfCheck: %v", rerr)
	}

	log := sb.String()
	for _, want := range []string{
		"feed key=\"log-key\"",
		fmt.Sprintf("bucket=%d", HashBucket("log-key")),
		"rate=50",
		"bucket < rate",
		"sampled=",
		"adjust rate 50 -> 100",
		"should-sample",
		"self-check ok",
	} {
		if !strings.Contains(log, want) {
			t.Fatalf("log missing %q\nfull log:\n%s", want, log)
		}
	}
}

// TestConcurrentAccess：多个执行体并发喂入、调率、判定与自检，竞态下结果仍自洽。
func TestConcurrentAccess(t *testing.T) {
	s := newLoggedSampler[string](t, 100, WithMaxKnownKeys[string](10_000))

	const workers = 16
	const rounds = 200
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for r := 0; r < rounds; r++ {
				key := fmt.Sprintf("w%d-r%d", id, r)
				kept, ferr := s.Feed(events(key))
				if ferr != nil {
					t.Errorf("Feed(%q): %v", key, ferr)
					return
				}
				// 调率协程可能在两次调用之间改率，因此以判定时刻的当前率为准，
				// 但朴素规则 bucket < rate 必须始终成立。
				sampled, qerr := s.ShouldSample(key)
				if qerr != nil {
					t.Errorf("ShouldSample(%q): %v", key, qerr)
					return
				}
				// Feed 的采出数只能是 0 或 1（同一键判定唯一）。
				if len(kept) > 1 {
					t.Errorf("Feed returned %d events for a single-key batch", len(kept))
				}
				// 判定结果必须是布尔合法值且与固定桶值相容：
				// 桶 9999 在任何 rate<=9999 时都不可见等不变量由 SelfCheck 覆盖；
				// 这里只确认调用在竞态下安全返回。
				_ = sampled
				if cerr := s.SelfCheck(); cerr != nil {
					t.Errorf("SelfCheck: %v", cerr)
					return
				}
			}
		}(w)
	}

	wg.Add(1)
	go func() {
		defer wg.Done()
		rates := []int{0, 1, 50, 5000, MaxRate - 1, MaxRate, 3333, 7777}
		for r := 0; r < 40; r++ {
			newRate := rates[r%len(rates)]
			chg, err := s.AdjustRate(newRate)
			if err != nil {
				t.Errorf("AdjustRate(%d): %v", newRate, err)
				return
			}
			if newRate > chg.OldRate && len(chg.Removed) != 0 {
				t.Errorf("raise %d -> %d removed keys: %v",
					chg.OldRate, newRate, chg.Removed)
			}
			if newRate < chg.OldRate && len(chg.Added) != 0 {
				t.Errorf("lower %d -> %d added keys: %v",
					chg.OldRate, newRate, chg.Added)
			}
		}
	}()

	wg.Wait()

	if s.KnownKeys() != workers*rounds {
		t.Fatalf("KnownKeys = %d, want %d", s.KnownKeys(), workers*rounds)
	}

	// 并发结束后做确定性校验：每个已知键的判定与朴素规则一致。
	finalRate := s.Rate()
	for w := 0; w < workers; w++ {
		for r := 0; r < rounds; r++ {
			key := fmt.Sprintf("w%d-r%d", w, r)
			got, err := s.ShouldSample(key)
			if err != nil {
				t.Fatalf("ShouldSample(%q): %v", key, err)
			}
			if got != (HashBucket(key) < finalRate) {
				t.Fatalf("final judgement mismatch for %q: got %t bucket=%d rate=%d",
					key, got, HashBucket(key), finalRate)
			}
		}
	}
	if err := s.SelfCheck(); err != nil {
		t.Fatalf("final SelfCheck: %v", err)
	}
}

// TestRateBoundariesAccepted：合法边界 0 与 MaxRate 可正常构造与调整。
func TestRateBoundariesAccepted(t *testing.T) {
	for _, rate := range []int{0, MaxRate} {
		s := newLoggedSampler[string](t, rate)
		if s.Rate() != rate {
			t.Fatalf("Rate = %d, want %d", s.Rate(), rate)
		}
		chg, err := s.AdjustRate(rate)
		if err != nil {
			t.Fatalf("AdjustRate(%d): %v", rate, err)
		}
		if len(chg.Added) != 0 || len(chg.Removed) != 0 {
			t.Fatalf("same-rate adjust = %+v, want empty", chg)
		}
	}
}

// TestCrossInstanceConsistency：两个独立实例对同一组键给出相同桶值、相同判定与相同输出。
func TestCrossInstanceConsistency(t *testing.T) {
	s1 := newLoggedSampler[string](t, 1234)
	s2 := newLoggedSampler[string](t, 1234)
	src := events("k1", "k2", "k3", "k4", "k5")

	kept1, err1 := s1.Feed(src)
	kept2, err2 := s2.Feed(src)
	if err1 != nil || err2 != nil {
		t.Fatalf("Feed errors: %v %v", err1, err2)
	}
	assertEventsEqual(t, kept1, kept2)
	assertEventsEqual(t, kept1, naiveExpected(src, 1234))

	for _, key := range []string{"k1", "k2", "k3", "k4", "k5"} {
		b1, b2 := HashBucket(key), HashBucket(key)
		if b1 != b2 {
			t.Fatalf("bucket drift for %q: %d vs %d", key, b1, b2)
		}
		got1, _ := s1.ShouldSample(key)
		got2, _ := s2.ShouldSample(key)
		if got1 != got2 || got1 != (b1 < 1234) {
			t.Fatalf("judgement mismatch for %q: %t %t bucket=%d", key, got1, got2, b1)
		}
	}

	c1, _ := s1.AdjustRate(7777)
	c2, _ := s2.AdjustRate(7777)
	if fmt.Sprint(c1.Added) != fmt.Sprint(c2.Added) ||
		fmt.Sprint(c1.Removed) != fmt.Sprint(c2.Removed) {
		t.Fatalf("adjust results differ across instances:\n%v\n%v", c1, c2)
	}
}

// findKeyAtBucket 在指定桶值上找一个键（边界桶 0 与 MaxRate-1 必需）。
func findKeyAtBucket(t *testing.T, target int) string {
	t.Helper()
	for i := 0; ; i++ {
		key := fmt.Sprintf("boundary-%d", i)
		if HashBucket(key) == target {
			return key
		}
		if i > 5_000_000 {
			t.Fatalf("no key hashes to bucket %d", target)
		}
	}
}

// TestBoundaryBuckets：严格小于语义；桶 0 与桶 MaxRate-1 的边界行为；升降率单调。
func TestBoundaryBuckets(t *testing.T) {
	if b := HashBucket(""); b < 0 || b >= MaxRate {
		t.Fatalf("empty-string bucket %d out of range", b)
	}

	zeroKey := findKeyAtBucket(t, 0)
	edgeKey := findKeyAtBucket(t, MaxRate-1)

	s := newLoggedSampler[string](t, 0)
	kept, err := s.Feed(events(zeroKey, edgeKey, "plain"))
	if err != nil {
		t.Fatalf("Feed: %v", err)
	}
	if len(kept) != 0 {
		t.Fatalf("rate=0 should sample nothing, got %v", kept)
	}

	chg, rerr := s.AdjustRate(1)
	if rerr != nil {
		t.Fatalf("AdjustRate(1): %v", rerr)
	}
	if len(chg.Added) != 1 || chg.Added[0] != zeroKey {
		t.Fatalf("Added = %v, want only %q", chg.Added, zeroKey)
	}
	if len(chg.Removed) != 0 {
		t.Fatalf("raising rate must remove nothing, got %v", chg.Removed)
	}

	chg, _ = s.AdjustRate(MaxRate - 1)
	if containsKey(chg.Added, edgeKey) {
		t.Fatalf("%q bucket=%d must not be sampled at rate=%d",
			edgeKey, MaxRate-1, MaxRate-1)
	}
	if sampled, _ := s.ShouldSample(edgeKey); sampled {
		t.Fatalf("bucket %d must be hidden at rate %d", MaxRate-1, MaxRate-1)
	}

	chg, _ = s.AdjustRate(MaxRate)
	if !containsKey(chg.Added, edgeKey) {
		t.Fatalf("%q should be added when reaching rate=%d", edgeKey, MaxRate)
	}
	if len(chg.Removed) != 0 {
		t.Fatalf("raising to max must remove nothing, got %v", chg.Removed)
	}
	if sampled, _ := s.ShouldSample(edgeKey); !sampled {
		t.Fatalf("bucket %d must be visible at rate %d", MaxRate-1, MaxRate)
	}

	chg, _ = s.AdjustRate(0)
	if len(chg.Added) != 0 {
		t.Fatalf("lowering rate must add nothing, got %v", chg.Added)
	}
	if len(chg.Removed) != s.KnownKeys() {
		t.Fatalf("lowering to 0 must remove all %d known keys, got %d",
			s.KnownKeys(), len(chg.Removed))
	}
}

// TestAdjustRateSortingAndMonotonicity：升降率的 Added/Removed 单调且按桶值、键排序。
func TestAdjustRateSortingAndMonotonicity(t *testing.T) {
	s := newLoggedSampler[string](t, 0)
	src := events("zebra", "apple", "mango", "banana", "cherry")
	if _, err := s.Feed(src); err != nil {
		t.Fatalf("Feed: %v", err)
	}

	up, err := s.AdjustRate(5000)
	if err != nil {
		t.Fatalf("AdjustRate: %v", err)
	}
	if len(up.Removed) != 0 {
		t.Fatalf("raise: Removed must be empty, got %v", up.Removed)
	}
	for i := 1; i < len(up.Added); i++ {
		pa, pb := HashBucket(up.Added[i-1]), HashBucket(up.Added[i])
		if pa > pb || (pa == pb && up.Added[i-1] > up.Added[i]) {
			t.Fatalf("Added not sorted by (bucket,key): %v", up.Added)
		}
	}
	wasSampled := map[string]bool{}
	for _, key := range up.Added {
		wasSampled[key] = true
		if HashBucket(key) >= 5000 {
			t.Fatalf("%q bucket=%d wrongly added at rate=5000", key, HashBucket(key))
		}
	}

	down, _ := s.AdjustRate(1000)
	if len(down.Added) != 0 {
		t.Fatalf("lower: Added must be empty, got %v", down.Added)
	}
	for _, key := range down.Removed {
		b := HashBucket(key)
		if !wasSampled[key] {
			t.Fatalf("removed key %q was never sampled", key)
		}
		if b < 1000 {
			t.Fatalf("%q bucket=%d must remain sampled at rate=1000", key, b)
		}
	}
	for i := 1; i < len(down.Removed); i++ {
		pa, pb := HashBucket(down.Removed[i-1]), HashBucket(down.Removed[i])
		if pa > pb || (pa == pb && down.Removed[i-1] > down.Removed[i]) {
			t.Fatalf("Removed not sorted by (bucket,key): %v", down.Removed)
		}
	}

	up2, _ := s.AdjustRate(5000)
	for _, key := range src {
		b := HashBucket(key.Key)
		if b < 1000 && containsKey(up2.Added, key.Key) {
			t.Fatalf("still-sampled key %q bucket=%d must not be re-added", key.Key, b)
		}
		if b >= 1000 && b < 5000 && !containsKey(up2.Added, key.Key) {
			t.Fatalf("key %q bucket=%d should be re-added at rate=5000", key.Key, b)
		}
	}

	same, _ := s.AdjustRate(5000)
	if len(same.Added) != 0 || len(same.Removed) != 0 ||
		same.OldRate != 5000 || same.NewRate != 5000 {
		t.Fatalf("no-op adjust = %+v, want empty lists", same)
	}

	if err := s.SelfCheck(); err != nil {
		t.Fatalf("SelfCheck: %v", err)
	}
}

package sampling

import (
	"errors"
	"fmt"
	"hash/fnv"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

// testLogger 用 testing.T 记录每步输入、桶值与判定依据（go test -v 可见）。
type testLogger struct{ t *testing.T }

func (l testLogger) Printf(format string, args ...any) {
	l.t.Logf(format, args...)
}

func newTestSampler(t *testing.T, rate, max int) *Sampler {
	t.Helper()
	s, err := New(rate, max, WithLogger(testLogger{t}))
	if err != nil {
		t.Fatalf("New(%d,%d) unexpected error: %v", rate, max, err)
	}
	return s
}

// naiveBucket 用标准库 hash/fnv 复现朴素判定，与实现相互独立对照。
func naiveBucket(t *testing.T, key string) uint32 {
	t.Helper()
	h := fnv.New32a()
	if _, err := h.Write([]byte(key)); err != nil {
		t.Fatalf("hash write: %v", err)
	}
	return h.Sum32() % BucketRange
}

// findKeyForBucket 在键空间内寻找恰好落在 bucketValue 的键。
func findKeyForBucket(t *testing.T, bucketValue uint32) string {
	t.Helper()
	for i := 0; i < 10_000_000; i++ {
		key := fmt.Sprintf("boundary-key-%d", i)
		if bucket(key) == bucketValue {
			return key
		}
	}
	t.Fatalf("no key found for bucket %d", bucketValue)
	return ""
}

func byBucketThenKey(keys []string) bool {
	return sort.SliceIsSorted(keys, func(i, j int) bool {
		bi, bj := bucket(keys[i]), bucket(keys[j])
		if bi != bj {
			return bi < bj
		}
		return keys[i] < keys[j]
	})
}

func TestSameKeyAllOrNothing(t *testing.T) {
	s := newTestSampler(t, 5000, 0)

	const key = "order-1001"
	events := []Event{
		{Key: key, Payload: 1},
		{Key: "other-a", Payload: 2},
		{Key: key, Payload: 3},
		{Key: key, Payload: 4},
	}
	out, err := s.Feed(events)
	if err != nil {
		t.Fatalf("Feed: %v", err)
	}

	want, err := s.Sampled(key)
	if err != nil {
		t.Fatalf("Sampled: %v", err)
	}
	gotCount := 0
	for _, e := range out {
		if e.Key == key {
			gotCount++
		}
	}
	if want && gotCount != 3 {
		t.Fatalf("key should be fully visible: want 3 events, got %d", gotCount)
	}
	if !want && gotCount != 0 {
		t.Fatalf("key should be fully hidden: want 0 events, got %d", gotCount)
	}

	// 输出必须保持输入中的相对顺序。
	next := 0
	for _, got := range out {
		idx := -1
		for j := next; j < len(events); j++ {
			if events[j] == got {
				idx = j
				break
			}
		}
		if idx < 0 {
			t.Fatalf("output event %v out of order after index %d", got, next)
		}
		next = idx + 1
	}

	if err := s.SelfCheck(); err != nil {
		t.Fatalf("self-check: %v", err)
	}
}

func TestCrossInstanceConsistent(t *testing.T) {
	keys := []string{"k-0", "k-1", "k-2", "k-3", "user:42", "设备/A", "中文键"}
	a := newTestSampler(t, 3333, 0)
	b := newTestSampler(t, 3333, 0)

	var evsA, evsB []Event
	for _, k := range keys {
		evsA = append(evsA, Event{Key: k})
		evsB = append(evsB, Event{Key: k})
	}
	outA, err := a.Feed(evsA)
	if err != nil {
		t.Fatalf("feed a: %v", err)
	}
	outB, err := b.Feed(evsB)
	if err != nil {
		t.Fatalf("feed b: %v", err)
	}
	if len(outA) != len(outB) {
		t.Fatalf("instance output lengths differ: %d vs %d", len(outA), len(outB))
	}
	for i := range outA {
		if outA[i].Key != outB[i].Key {
			t.Fatalf("instance outputs differ at %d: %q vs %q", i, outA[i].Key, outB[i].Key)
		}
	}

	for _, k := range keys {
		got, err := a.Sampled(k)
		if err != nil {
			t.Fatalf("sampled a: %v", err)
		}
		want := naiveBucket(t, k) < 3333
		if got != want {
			t.Fatalf("key %q: instance=%v naive=%v", k, got, want)
		}
	}

	addA, remA, err := a.SetRate(7777)
	if err != nil {
		t.Fatalf("setrate a: %v", err)
	}
	addB, remB, err := b.SetRate(7777)
	if err != nil {
		t.Fatalf("setrate b: %v", err)
	}
	if strings.Join(addA, ",") != strings.Join(addB, ",") ||
		strings.Join(remA, ",") != strings.Join(remB, ",") {
		t.Fatalf("rate-change diffs differ across instances: %+v/%+v vs %+v/%+v",
			addA, remA, addB, remB)
	}
}

func TestBoundaryBuckets(t *testing.T) {
	// 严格小于：桶值 == rate 不采，桶值 == rate-1 与 0 采。
	keyAtRate := findKeyForBucket(t, 1000)
	keyBelow := findKeyForBucket(t, 999)
	keyZero := findKeyForBucket(t, 0)

	s := newTestSampler(t, 1000, 0)
	out, err := s.Feed([]Event{{Key: keyAtRate}, {Key: keyBelow}, {Key: keyZero}})
	if err != nil {
		t.Fatalf("feed: %v", err)
	}
	got := map[string]bool{}
	for _, e := range out {
		got[e.Key] = true
	}
	if got[keyAtRate] {
		t.Fatalf("bucket == rate must be excluded (strict less-than)")
	}
	if !got[keyBelow] || !got[keyZero] {
		t.Fatalf("bucket < rate must be included: below=%v zero=%v", got[keyBelow], got[keyZero])
	}

	// rate=0：什么都不采；rate=10000：全部采出。
	s0 := newTestSampler(t, 0, 0)
	out0, err := s0.Feed([]Event{{Key: keyZero}, {Key: keyBelow}})
	if err != nil {
		t.Fatalf("feed rate0: %v", err)
	}
	if len(out0) != 0 {
		t.Fatalf("rate=0 must sample nothing, got %d", len(out0))
	}

	sFull := newTestSampler(t, BucketRange, 0)
	allOut, err := sFull.Feed([]Event{{Key: keyZero}, {Key: keyBelow}, {Key: keyAtRate}})
	if err != nil {
		t.Fatalf("feed rate10000: %v", err)
	}
	if len(allOut) != 3 {
		t.Fatalf("rate=10000 must sample everything, got %d/3", len(allOut))
	}

	// 1000 -> 999：仅桶 999 的键不再满足严格小于；桶 0 仍可见，桶 1000 本就不采。
	added, removed, err := s.SetRate(999)
	if err != nil {
		t.Fatalf("setrate 999: %v", err)
	}
	if len(added) != 0 || len(removed) != 1 || removed[0] != keyBelow {
		t.Fatalf("999 diffs unexpected: added=%v removed=%v", added, removed)
	}
	// 999 -> 1000：仅桶 999 的键重新纳入。
	added, removed, err = s.SetRate(1000)
	if err != nil {
		t.Fatalf("setrate 1000: %v", err)
	}
	if len(removed) != 0 || len(added) != 1 || added[0] != keyBelow {
		t.Fatalf("1000 diffs unexpected: added=%v removed=%v", added, removed)
	}
	// 采样率不变：两份都为空。
	added, removed, err = s.SetRate(1000)
	if err != nil {
		t.Fatalf("setrate same: %v", err)
	}
	if len(added) != 0 || len(removed) != 0 {
		t.Fatalf("unchanged rate must yield empty diffs: %v %v", added, removed)
	}
}

func TestDiffsSortedAndMonotonic(t *testing.T) {
	s := newTestSampler(t, 0, 0)

	// 登记一批桶值互不相同的已知键。
	var evs []Event
	seen := map[uint32]bool{}
	for i := 0; len(evs) < 40; i++ {
		key := fmt.Sprintf("mono-%d", i)
		b := bucket(key)
		if seen[b] {
			continue
		}
		seen[b] = true
		evs = append(evs, Event{Key: key})
	}
	if _, err := s.Feed(evs); err != nil {
		t.Fatalf("feed: %v", err)
	}

	visible := map[string]bool{}
	for _, r := range []int{1000, 2500, 5000, 7500, BucketRange} {
		added, removed, err := s.SetRate(r)
		if err != nil {
			t.Fatalf("setrate %d: %v", r, err)
		}
		if len(removed) != 0 {
			t.Fatalf("rate increase must remove nothing, got %v", removed)
		}
		if !byBucketThenKey(added) {
			t.Fatalf("added list not sorted by (bucket,key): %v", added)
		}
		for _, k := range added {
			if visible[k] {
				t.Fatalf("key %q reported added twice", k)
			}
			visible[k] = true
		}
		for _, e := range evs {
			got, err := s.Sampled(e.Key)
			if err != nil {
				t.Fatalf("sampled: %v", err)
			}
			if want := bucket(e.Key) < uint32(r); got != want {
				t.Fatalf("key %q at rate %d: got=%v want=%v", e.Key, r, got, want)
			}
		}
	}
	if len(visible) != len(evs) {
		t.Fatalf("at full rate all keys must become visible: %d/%d", len(visible), len(evs))
	}

	for _, r := range []int{7500, 5000, 2500, 0} {
		added, removed, err := s.SetRate(r)
		if err != nil {
			t.Fatalf("setrate %d: %v", r, err)
		}
		if len(added) != 0 {
			t.Fatalf("rate decrease must add nothing, got %v", added)
		}
		if !byBucketThenKey(removed) {
			t.Fatalf("removed list not sorted by (bucket,key): %v", removed)
		}
	}
}

func TestInvalidRateAtConstruction(t *testing.T) {
	for _, r := range []int{-1, BucketRange + 1, -10000} {
		if _, err := New(r, 0); !errors.Is(err, ErrRateOutOfRange) {
			t.Fatalf("New(%d) err=%v, want ErrRateOutOfRange", r, err)
		}
	}
	if _, err := New(0, 0); err != nil {
		t.Fatalf("New(0) should be valid: %v", err)
	}
	if _, err := New(BucketRange, 0); err != nil {
		t.Fatalf("New(10000) should be valid: %v", err)
	}
}

func TestRejectionLeavesNoTrace(t *testing.T) {
	s := newTestSampler(t, 1000, 2)

	if _, err := s.Feed([]Event{{Key: "a"}, {Key: "b"}}); err != nil {
		t.Fatalf("seed feed: %v", err)
	}

	// 超限：整批拒绝，批中尚未登记的新键一个都不得留下。
	_, err := s.Feed([]Event{{Key: "c"}, {Key: "a"}, {Key: "d"}})
	if !errors.Is(err, ErrTooManyKeys) {
		t.Fatalf("want ErrTooManyKeys, got %v", err)
	}
	if s.KnownKeys() != 2 || s.Rate() != 1000 {
		t.Fatalf("state after limit rejection changed: known=%d rate=%d", s.KnownKeys(), s.Rate())
	}

	// 空键：整批拒绝。
	_, err = s.Feed([]Event{{Key: "c"}, {Key: ""}})
	if !errors.Is(err, ErrEmptyKey) {
		t.Fatalf("want ErrEmptyKey, got %v", err)
	}
	if s.KnownKeys() != 2 {
		t.Fatalf("state after empty-key rejection changed: known=%d", s.KnownKeys())
	}

	// 空键在首项时同样拒绝，且不影响已有键可见性。
	_, err = s.Feed([]Event{{Key: ""}, {Key: "a"}})
	if !errors.Is(err, ErrEmptyKey) {
		t.Fatalf("want ErrEmptyKey, got %v", err)
	}

	// 非法调率：拒绝且采样率不变，返回的列表为 nil（非空集合）。
	added, removed, err := s.SetRate(BucketRange + 1)
	if !errors.Is(err, ErrRateOutOfRange) {
		t.Fatalf("want ErrRateOutOfRange, got %v", err)
	}
	if added != nil || removed != nil || s.Rate() != 1000 {
		t.Fatalf("state after rate rejection changed: added=%v removed=%v rate=%d",
			added, removed, s.Rate())
	}
	added, removed, err = s.SetRate(-1)
	if !errors.Is(err, ErrRateOutOfRange) || added != nil || removed != nil {
		t.Fatalf("negative rate rejection malformed: err=%v added=%v removed=%v",
			err, added, removed)
	}

	// Sampled 的空键拒绝。
	if _, err := s.Sampled(""); !errors.Is(err, ErrEmptyKey) {
		t.Fatalf("Sampled(\"\") want ErrEmptyKey, got %v", err)
	}

	// 三类错误必须互不相同、可区分。
	if errors.Is(ErrRateOutOfRange, ErrEmptyKey) ||
		errors.Is(ErrEmptyKey, ErrTooManyKeys) ||
		errors.Is(ErrRateOutOfRange, ErrTooManyKeys) {
		t.Fatalf("sentinel errors must be distinct")
	}

	if err := s.SelfCheck(); err != nil {
		t.Fatalf("self-check after rejections: %v", err)
	}

	// 拒绝后采样器仍可正常工作。
	out, err := s.Feed([]Event{{Key: "a"}})
	if err != nil {
		t.Fatalf("post-rejection feed broken: %v", err)
	}
	want, _ := s.Sampled("a")
	if (len(out) == 1) != want {
		t.Fatalf("post-rejection feed disagrees with Sampled: out=%v want=%v", out, want)
	}
}

func TestNaiveAgreement(t *testing.T) {
	s := newTestSampler(t, 0, 0)
	var evs []Event
	for i := 0; i < 500; i++ {
		key := fmt.Sprintf("naive-%d", i)
		evs = append(evs, Event{Key: key, Payload: i})
	}
	if _, err := s.Feed(evs); err != nil {
		t.Fatalf("feed: %v", err)
	}

	for _, r := range []int{0, 1, 17, 500, 3333, 5000, 9999, BucketRange} {
		if _, _, err := s.SetRate(r); err != nil {
			t.Fatalf("setrate %d: %v", r, err)
		}
		for _, e := range evs {
			got, err := s.Sampled(e.Key)
			if err != nil {
				t.Fatalf("sampled: %v", err)
			}
			want := naiveBucket(t, e.Key) < uint32(r)
			if got != want {
				t.Fatalf("key %q rate %d: got=%v naive=%v bucket=%d",
					e.Key, r, got, want, naiveBucket(t, e.Key))
			}
		}
	}
}

func TestConcurrentAccess(t *testing.T) {
	stable := newTestSampler(t, 5000, 0)
	churn := newTestSampler(t, 5000, 0)

	// 预登记一批固定键供读侧判定。
	var fixed []Event
	for i := 0; i < 200; i++ {
		fixed = append(fixed, Event{Key: fmt.Sprintf("fixed-%d", i)})
	}
	if _, err := stable.Feed(fixed); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if _, err := churn.Feed(fixed); err != nil {
		t.Fatalf("seed churn: %v", err)
	}

	var wg sync.WaitGroup
	var failures int64

	// 读侧：固定率实例上 Sampled / Feed / SelfCheck 并发，判定与朴素规则恒定一致。
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for n := 0; n < 500; n++ {
				key := fixed[(id+n)%len(fixed)].Key
				got, err := stable.Sampled(key)
				if err != nil {
					atomic.AddInt64(&failures, 1)
					return
				}
				if want := bucket(key) < 5000; got != want {
					t.Errorf("concurrent sampled mismatch for %q: got=%v want=%v", key, got, want)
					atomic.AddInt64(&failures, 1)
					return
				}
				if n%25 == 0 {
					if _, err := stable.Feed([]Event{{Key: key}}); err != nil {
						t.Errorf("concurrent feed: %v", err)
						atomic.AddInt64(&failures, 1)
						return
					}
				}
				if n%50 == 0 {
					if err := stable.SelfCheck(); err != nil {
						t.Errorf("concurrent self-check: %v", err)
						atomic.AddInt64(&failures, 1)
						return
					}
				}
			}
		}(w)
	}

	// 写侧：在另一个实例上 0..10000 来回调率；升率 removed 必空、降率 added 必空，
	// 且返回差集与新采样率下的朴素判定一致。
	wg.Add(1)
	go func() {
		defer wg.Done()
		r := 5000
		for n := 0; n < 300; n++ {
			prev := r
			if n%2 == 0 {
				r += 137
				if r > BucketRange {
					r = 0
				}
			} else {
				r -= 137
				if r < 0 {
					r = BucketRange
				}
			}
			added, removed, err := churn.SetRate(r)
			if err != nil {
				t.Errorf("concurrent setrate %d: %v", r, err)
				atomic.AddInt64(&failures, 1)
				return
			}
			if r > prev && len(removed) != 0 {
				t.Errorf("increase %d->%d removed non-empty: %v", prev, r, removed)
				atomic.AddInt64(&failures, 1)
			}
			if r < prev && len(added) != 0 {
				t.Errorf("decrease %d->%d added non-empty: %v", prev, r, added)
				atomic.AddInt64(&failures, 1)
			}
			for _, k := range added {
				if bucket(k) >= uint32(r) {
					t.Errorf("added key %q bucket %d not < rate %d", k, bucket(k), r)
					atomic.AddInt64(&failures, 1)
				}
			}
			for _, k := range removed {
				if bucket(k) < uint32(r) {
					t.Errorf("removed key %q bucket %d still < rate %d", k, bucket(k), r)
					atomic.AddInt64(&failures, 1)
				}
			}
			if n%20 == 0 {
				if _, err := churn.Feed([]Event{{Key: fixed[n%len(fixed)].Key}}); err != nil {
					t.Errorf("concurrent churn feed: %v", err)
					atomic.AddInt64(&failures, 1)
				}
			}
		}
	}()

	wg.Wait()
	if atomic.LoadInt64(&failures) != 0 {
		t.Fatalf("concurrent run had %d failures", failures)
	}
	if err := stable.SelfCheck(); err != nil {
		t.Fatalf("final stable self-check: %v", err)
	}
	if err := churn.SelfCheck(); err != nil {
		t.Fatalf("final churn self-check: %v", err)
	}
}

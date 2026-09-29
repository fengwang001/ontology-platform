package sampling

import (
	"bytes"
	"errors"
	"fmt"
	"log/slog"
	"math/rand"
	"sort"
	"strings"
	"sync"
	"testing"
)

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(&bytes.Buffer{}, &slog.HandlerOptions{Level: slog.LevelError}))
}

func mustNew(t *testing.T, rate, maxKeys int) *Sampler {
	t.Helper()
	s, err := New(rate, maxKeys, discardLogger())
	if err != nil {
		t.Fatalf("New(%d) unexpected error: %v", rate, err)
	}
	return s
}

func keyWithBucket(t *testing.T, target int) string {
	t.Helper()
	for i := 0; i < 2_000_000; i++ {
		key := fmt.Sprintf("seek-%d-%d", target, i)
		if Bucket(key) == target {
			return key
		}
	}
	t.Fatalf("could not find key with bucket %d", target)
	return ""
}

func TestAllOrNothingSameKey(t *testing.T) {
	for _, rate := range []int{0, 1, 7, 5000, 9999, 10000} {
		s := mustNew(t, rate, 0)
		key := fmt.Sprintf("tenant/obj/42@rate=%d", rate)
		events := []Event{
			{Key: key, Value: "create"},
			{Key: key, Value: "update-1"},
			{Key: key, Value: "update-2"},
			{Key: key, Value: "delete"},
		}
		got, err := s.Feed(events)
		if err != nil {
			t.Fatalf("rate=%d Feed: %v", rate, err)
		}
		wantKeep := Bucket(key) < rate
		if wantKeep && len(got) != len(events) {
			t.Fatalf("rate=%d: expected all events kept, got %d/%d", rate, len(got), len(events))
		}
		if !wantKeep && len(got) != 0 {
			t.Fatalf("rate=%d: expected all events dropped, got %d", rate, len(got))
		}
		got2, _ := s.Feed(events)
		if len(got2) != len(got) {
			t.Fatalf("rate=%d: non-reproducible decision %d vs %d", rate, len(got2), len(got))
		}
		if s.KnownKeys() != 1 {
			t.Fatalf("rate=%d: expected 1 known key, got %d", rate, s.KnownKeys())
		}
	}
}

func TestCrossInstanceConsistency(t *testing.T) {
	const rate = 3333
	a := mustNew(t, rate, 0)
	b := mustNew(t, rate, 0)
	var events []Event
	for i := 0; i < 500; i++ {
		events = append(events, Event{Key: fmt.Sprintf("k-%04d", i), Value: i})
	}
	oa, errA := a.Feed(events)
	ob, errB := b.Feed(events)
	if errA != nil || errB != nil {
		t.Fatalf("Feed errors: %v %v", errA, errB)
	}
	if len(oa) != len(ob) {
		t.Fatalf("instances disagree on count: %d vs %d", len(oa), len(ob))
	}
	for i := range oa {
		if oa[i].Key != ob[i].Key {
			t.Fatalf("instances disagree at position %d: %q vs %q", i, oa[i].Key, ob[i].Key)
		}
	}
	for i := 0; i < 500; i++ {
		key := fmt.Sprintf("k-%04d", i)
		sa, _ := a.Sample(key)
		sb, _ := b.Sample(key)
		if sa != sb || sa != (Bucket(key) < rate) {
			t.Fatalf("Sample disagreement for %q: %v %v", key, sa, sb)
		}
	}
}

func TestBoundaryBuckets(t *testing.T) {
	b0 := keyWithBucket(t, 0)
	b5000 := keyWithBucket(t, 5000)
	b9999 := keyWithBucket(t, 9999)

	if Bucket(b0) != 0 || Bucket(b5000) != 5000 || Bucket(b9999) != 9999 {
		t.Fatal("bucket helper returned wrong keys")
	}

	s := mustNew(t, 0, 0)
	seed := []Event{{Key: b0}, {Key: b5000}, {Key: b9999}}
	if out, _ := s.Feed(seed); len(out) != 0 {
		t.Fatalf("rate=0 must drop everything, kept %d", len(out))
	}

	ch, err := s.SetRate(1)
	if err != nil || len(ch.Added) != 1 || ch.Added[0] != b0 || len(ch.Removed) != 0 {
		t.Fatalf("rate 0->1: %+v, err=%v", ch, err)
	}

	if _, err := s.SetRate(5000); err != nil {
		t.Fatal(err)
	}
	if keep, _ := s.Sample(b5000); keep {
		t.Fatal("bucket == rate must be dropped (strict less-than)")
	}
	ch, err = s.SetRate(5001)
	if err != nil {
		t.Fatal(err)
	}
	if keep, _ := s.Sample(b5000); !keep {
		t.Fatal("bucket 5000 must be sampled at rate 5001")
	}
	found := false
	for _, k := range ch.Added {
		if k == b5000 {
			found = true
		}
	}
	if !found {
		t.Fatalf("boundary key missing from Added: %+v", ch.Added)
	}

	if keep, _ := s.Sample(b9999); keep {
		t.Fatal("bucket 9999 must be dropped at rate 9999")
	}
	ch, _ = s.SetRate(10000)
	if keep, _ := s.Sample(b9999); !keep {
		t.Fatal("bucket 9999 must be sampled at rate 10000")
	}
	if len(ch.Added) != 1 || ch.Added[0] != b9999 {
		t.Fatalf("rate 9999->10000 should add only bucket-9999 key, got %+v", ch.Added)
	}
	if err := s.SelfCheck(); err != nil {
		t.Fatal(err)
}
}

func errorKind(t *testing.T, err error) ErrorKind {
	t.Helper()
	var se *SamplingError
	if !errors.As(err, &se) {
		t.Fatalf("error %v is not *SamplingError", err)
	}
	return se.Kind
}

func TestInvalidInputsAndNoTrace(t *testing.T) {
	for _, bad := range []int{-1, 10001, -10000} {
		_, err := New(bad, 0, discardLogger())
		if err == nil || errorKind(t, err) != ErrRateOutOfRange {
			t.Fatalf("New(%d) must fail with ErrRateOutOfRange, got %v", bad, err)
		}
	}

	s := mustNew(t, 5000, 2)

	batch := []Event{{Key: "brand-new-1"}, {Key: ""}, {Key: "brand-new-2"}}
	_, err := s.Feed(batch)
	if err == nil || errorKind(t, err) != ErrEmptyKey {
		t.Fatalf("empty-key Feed error = %v, want ErrEmptyKey", err)
	}
	if s.KnownKeys() != 0 || s.Rate() != 5000 {
		t.Fatalf("rejected Feed left traces: known=%d rate=%d", s.KnownKeys(), s.Rate())
	}

	if _, err := s.Feed([]Event{{Key: "a"}, {Key: "b"}}); err != nil {
		t.Fatal(err)
	}
	_, err = s.Feed([]Event{{Key: "a"}, {Key: "c"}})
	if err == nil || errorKind(t, err) != ErrTooManyKnownKeys {
		t.Fatalf("over-limit Feed error = %v, want ErrTooManyKnownKeys", err)
	}
	if s.KnownKeys() != 2 {
		t.Fatalf("over-limit rejection left traces: known=%d", s.KnownKeys())
	}

	if _, err := s.Sample(""); err == nil || errorKind(t, err) != ErrEmptyKey {
		t.Fatalf("Sample(\"\") error = %v, want ErrEmptyKey", err)
	}

	_, err = s.SetRate(10001)
	if err == nil || errorKind(t, err) != ErrRateOutOfRange {
		t.Fatalf("SetRate(10001) error = %v", err)
	}
	if s.Rate() != 5000 || s.KnownKeys() != 2 {
		t.Fatalf("rejected SetRate left traces: rate=%d known=%d", s.Rate(), s.KnownKeys())
	}

	kinds := map[ErrorKind]bool{}
	rejections := []error{
		func() error { _, e := New(-1, 0, nil); return e }(),
		func() error { _, e := s.Feed([]Event{{Key: ""}}); return e }(),
		func() error { _, e := s.Feed([]Event{{Key: "x"}, {Key: "y"}}); return e }(),
	}
	for _, e := range rejections {
		k := errorKind(t, e)
		if kinds[k] {
			t.Fatalf("duplicate error kind %d among %v", k, rejections)
		}
		kinds[k] = true
	}
}

func sortedByBucketKey(list []string) bool {
	return sort.SliceIsSorted(list, func(i, j int) bool {
		bi, bj := Bucket(list[i]), Bucket(list[j])
		if bi != bj {
			return bi < bj
		}
		return list[i] < list[j]
	})
}

func naiveDiffs(known []string, oldRate, newRate int) (added, removed []string) {
	for _, k := range known {
		oldKeep := Bucket(k) < oldRate
		newKeep := Bucket(k) < newRate
		switch {
		case !oldKeep && newKeep:
			added = append(added, k)
		case oldKeep && !newKeep:
			removed = append(removed, k)
		}
	}
	sortKeyList := func(l []string) {
		sort.Slice(l, func(i, j int) bool {
			bi, bj := Bucket(l[i]), Bucket(l[j])
			return bi < bj || (bi == bj && l[i] < l[j])
		})
	}
	sortKeyList(added)
	sortKeyList(removed)
	return added, removed
}

func TestSetRateDiffsAndMonotonicity(t *testing.T) {
	s := mustNew(t, 0, 0)
	rng := rand.New(rand.NewSource(42))
	seen := map[string]bool{}
	var events []Event
	for i := 0; i < 1000; i++ {
		key := fmt.Sprintf("obj-%d", rng.Intn(400))
		if !seen[key] {
			seen[key] = true
			events = append(events, Event{Key: key})
		}
	}
	if _, err := s.Feed(events); err != nil {
		t.Fatal(err)
	}
	known := make([]string, 0, len(seen))
	for k := range seen {
		known = append(known, k)
	}

	prev := 0
	for _, rate := range []int{1, 2500, 5000, 9999, 10000, 5000, 2500, 0, 0} {
		ch, err := s.SetRate(rate)
		if err != nil {
			t.Fatalf("SetRate(%d): %v", rate, err)
		}
		if rate == prev {
			if ch.Added == nil || ch.Removed == nil ||
				len(ch.Added) != 0 || len(ch.Removed) != 0 {
				t.Fatalf("unchanged rate %d must yield two empty lists, got %+v", rate, ch)
			}
			continue
		}
		if rate > prev && len(ch.Removed) != 0 {
			t.Fatalf("rate increased %d->%d but Removed non-empty: %v", prev, rate, ch.Removed)
		}
		if rate < prev && len(ch.Added) != 0 {
			t.Fatalf("rate decreased %d->%d but Added non-empty: %v", prev, rate, ch.Added)
		}
		if !sortedByBucketKey(ch.Added) || !sortedByBucketKey(ch.Removed) {
			t.Fatalf("diffs not sorted by (bucket, key): %+v", ch)
		}
		wantAdded, wantRemoved := naiveDiffs(known, prev, rate)
		if strings.Join(ch.Added, ",") != strings.Join(wantAdded, ",") ||
			strings.Join(ch.Removed, ",") != strings.Join(wantRemoved, ",") {
			t.Fatalf("rate %d->%d diffs disagree with naive:\n got=%+v\nwant add=%v rem=%v",
				prev, rate, ch, wantAdded, wantRemoved)
		}

		// 与朴素判定一致：Feed 后输出集合恰好是当前率下桶值 < rate 的键。
		out, err := s.Feed(events)
		if err != nil {
			t.Fatal(err)
		}
		naiveKept := map[string]bool{}
		var naiveEvents []Event
		for _, e := range events {
			if Bucket(e.Key) < rate {
				naiveKept[e.Key] = true
				naiveEvents = append(naiveEvents, e)
			}
		}
		if len(out) != len(naiveEvents) {
			t.Fatalf("rate %d: Feed kept %d, naive expects %d", rate, len(out), len(naiveEvents))
		}
		for i := range out {
			if out[i] != naiveEvents[i] {
				t.Fatalf("rate %d: Feed output/order disagree with naive at %d", rate, i)
			}
		}
		for _, k := range known {
			keep, err := s.Sample(k)
			if err != nil {
				t.Fatal(err)
			}
			if keep != naiveKept[k] {
				t.Fatalf("rate %d key %q: Sample=%v naive=%v", rate, k, keep, naiveKept[k])
			}
		}

		if err := s.SelfCheck(); err != nil {
			t.Fatalf("SelfCheck at rate %d: %v", rate, err)
		}
		prev = rate
	}
}

func TestConcurrency(t *testing.T) {
	s := mustNew(t, 1000, 1_000_000)
	var wg sync.WaitGroup

	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				key := fmt.Sprintf("w%d-k%d", worker, i%150)
				if _, err := s.Feed([]Event{{Key: key, Value: i}}); err != nil {
					t.Errorf("Feed: %v", err)
					return
				}
				keep, err := s.Sample(key)
				if err != nil {
					t.Errorf("Sample: %v", err)
					return
				}
				if want := Bucket(key) < s.Rate(); keep != want {
					t.Errorf("key %q sampled=%v want %v", key, keep, want)
				}
			}
		}(w)
	}

	rates := []int{100, 5000, 9000, 3000, 1000}
	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			for i := 0; i < 100; i++ {
				r := rates[(worker*100+i)%len(rates)]
				if _, err := s.SetRate(r); err != nil {
					t.Errorf("SetRate: %v", err)
					return
				}
			}
		}(w)
	}

	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := s.SelfCheck(); err != nil {
				t.Errorf("SelfCheck: %v", err)
			}
		}()
	}

	wg.Wait()
	if err := s.SelfCheck(); err != nil {
		t.Fatal(err)
	}

	// 同一键在当前率下，跨时间、跨 Sample 调用判定恒定一致。
	for w := 0; w < 8; w++ {
		for i := 0; i < 150; i++ {
			key := fmt.Sprintf("w%d-k%d", w, i)
			first, _ := s.Sample(key)
			for j := 0; j < 5; j++ {
				got, _ := s.Sample(key)
				if got != first || got != (Bucket(key) < s.Rate()) {
					t.Fatalf("inconsistent sampling for %q", key)
				}
			}
		}
	}
}

func TestLogging(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo}))
	s, err := New(1234, 0, logger)
	if err != nil {
		t.Fatal(err)
	}
	key := keyWithBucket(t, 42)
	if _, err := s.Feed([]Event{{Key: key, Value: "v"}}); err != nil {
		t.Fatal(err)
	}
	log := buf.String()
	for _, want := range []string{
		"sampling.Feed begin",
		"key=" + key,
		"bucket=42",
		"rate=1234",
		"sampled=true",
		"bucket < rate => sampled",
	} {
		if !strings.Contains(log, want) {
			t.Fatalf("log missing %q:\n%s", want, log)
		}
	}

	dropKey := keyWithBucket(t, 5000)
	buf.Reset()
	if keep, _ := s.Sample(dropKey); keep {
		t.Fatal("bucket 5000 must drop at rate 1234")
	}
	log = buf.String()
	for _, want := range []string{
		"sampling.Sample",
		"bucket=5000",
		"sampled=false",
		"bucket >= rate => dropped",
	} {
		if !strings.Contains(log, want) {
			t.Fatalf("log missing %q:\n%s", want, log)
		}
	}
}

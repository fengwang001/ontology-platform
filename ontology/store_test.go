package ontology

import (
	"bytes"
	"fmt"
	"math/rand"
	"strings"
	"sync"
	"testing"
)

func newLoggedStore(t *testing.T, cfg Config) (*Store, *bytes.Buffer) {
	t.Helper()
	var buf bytes.Buffer
	return New(cfg).WithLogger(&buf), &buf
}

func TestConcurrentReadsDuringWrites(t *testing.T) {
	s, _ := newLoggedStore(t, Config{RetentionVersions: 5, MaxEntries: 0})

	var writers sync.WaitGroup
	var readers sync.WaitGroup
	stop := make(chan struct{})

	// Readers: queries and self-check must always see a consistent
	// snapshot while writers are committing.
	for r := 0; r < 4; r++ {
		readers.Add(1)
		go func(id int) {
			defer readers.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				_ = s.Watermark()
				_ = s.Ignored()
				_ = s.CurrentVersion("k0")
				if _, ok := s.Get("k1"); ok {
					if rows := s.Rows(); len(rows) == 0 {
						t.Errorf("Get reported a row that Rows omitted")
					}
				}
				_ = s.Verify()
				_ = s.Tombstones()
			}
		}(r)
	}

	// Writers: random valid batches on a small key/version space.
	for w := 0; w < 4; w++ {
		writers.Add(1)
		go func(id int) {
			defer writers.Done()
			rng := rand.New(rand.NewSource(int64(100 + id)))
			for i := 0; i < 200; i++ {
				ev := Event{
					Key:     fmt.Sprintf("k%d", rng.Intn(5)),
					Version: int64(1 + rng.Intn(30)),
					Op:      OpWrite,
					Value:   "v",
				}
				if rng.Intn(3) == 0 {
					ev.Op = OpDelete
				}
				if err := s.Apply([]Event{ev}); err != nil {
					t.Errorf("writer %d: %v", id, err)
					return
				}
			}
		}(w)
	}

	// Also hammer with invalid batches concurrently; rejection must
	// never corrupt state.
	writers.Add(1)
	go func() {
		defer writers.Done()
		bad := [][]Event{
			{},
			{{Key: "", Version: 1, Op: OpWrite}},
			{{Key: "x", Version: -1, Op: OpWrite}},
		}
		for i := 0; i < 200; i++ {
			if err := s.Apply(bad[i%len(bad)]); err == nil {
				t.Errorf("invalid batch unexpectedly accepted")
			}
		}
	}()

	writers.Wait()
	close(stop)
	readers.Wait()

	if err := s.Verify(); err != nil {
		t.Fatalf("final self-check: %v", err)
	}
}

func TestNewRejectsIllegalConfig(t *testing.T) {
	for _, cfg := range []Config{
		{RetentionVersions: -1},
		{MaxEntries: -1},
	} {
		func() {
			defer func() {
				if recover() == nil {
					t.Fatalf("New(%+v) must panic", cfg)
				}
			}()
			New(cfg)
		}()
	}
}

func TestMatchesNaiveReferenceWithInfiniteRetention(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	ref := newReferenceStore()
	// Retention larger than any emitted version means no tombstone is
	// ever evicted: the real store must match the naive never-expire
	// oracle state-for-state, including counters.
	s, _ := newLoggedStore(t, Config{RetentionVersions: 1 << 40})

	for b := 0; b < 40; b++ {
		n := 1 + rng.Intn(5)
		events := make([]Event, n)
		for i := range events {
			key := fmt.Sprintf("k%d", rng.Intn(6))
			op := OpWrite
			if rng.Intn(3) == 0 {
				op = OpDelete
			}
			events[i] = Event{Key: key, Version: int64(1 + rng.Intn(12)), Op: op, Value: "v"}
		}
		t.Logf("batch %d input: %+v", b, events)
		if err := s.Apply(events); err != nil {
			t.Fatal(err)
		}
		ref.apply(events)
		if fmt.Sprint(s.Rows()) != fmt.Sprint(ref.liveRows()) {
			t.Fatalf("batch %d rows differ:\nstore=%v\nref  =%v", b, s.Rows(), ref.liveRows())
		}
		if fmt.Sprint(s.Tombstones()) != fmt.Sprint(ref.liveStones()) {
			t.Fatalf("batch %d tombstones differ:\nstore=%v\nref  =%v", b, s.Tombstones(), ref.liveStones())
		}
		if s.Watermark() != ref.watermark || s.Ignored() != ref.ignored {
			t.Fatalf("batch %d counters differ: wm %d/%d ignored %d/%d",
				b, s.Watermark(), ref.watermark, s.Ignored(), ref.ignored)
		}
	}
}

func TestFiniteRetentionConsistencyUnderPrecondition(t *testing.T) {
	// Precondition: once a tombstone could expire, no event at or below
	// its version arrives for that key. Under that precondition expiry is
	// observationally invisible and the store still matches the naive
	// never-expire oracle.
	s, _ := newLoggedStore(t, Config{RetentionVersions: 3})
	ref := newReferenceStore()

	batches := [][]Event{
		{{Key: "a", Version: 1, Op: OpDelete}, {Key: "b", Version: 1, Op: OpWrite, Value: "b1"}},
		{{Key: "b", Version: 5, Op: OpWrite, Value: "b5"}}, // a@1 expires
		// a is unknown again; only strictly-new versions may now be used.
		{{Key: "a", Version: 6, Op: OpWrite, Value: "a6"}},
		{{Key: "a", Version: 7, Op: OpDelete}},             // new tombstone a@7
		{{Key: "c", Version: 10, Op: OpWrite, Value: "c"}}, // 10-7=3 -> a expires
		{{Key: "a", Version: 11, Op: OpWrite, Value: "a11"}},
	}
	for i, batch := range batches {
		t.Logf("batch %d input: %+v", i, batch)
		if err := s.Apply(batch); err != nil {
			t.Fatal(err)
		}
		ref.apply(batch)
		if fmt.Sprint(s.Rows()) != fmt.Sprint(ref.liveRows()) {
			t.Fatalf("batch %d rows differ:\nstore=%v\nref  =%v", i, s.Rows(), ref.liveRows())
		}
	}
}

func TestRejectedBatchLeavesNoTrace(t *testing.T) {
	cases := []struct {
		name   string
		cfg    Config
		events []Event
		reason Reason
	}{
		{
			name:   "empty batch",
			cfg:    Config{RetentionVersions: 100},
			events: nil,
			reason: ReasonInvalidArgument,
		},
		{
			name:   "empty key",
			cfg:    Config{RetentionVersions: 100},
			events: []Event{{Key: "", Version: 1, Op: OpWrite}},
			reason: ReasonInvalidEvent,
		},
		{
			name:   "non-positive version",
			cfg:    Config{RetentionVersions: 100},
			events: []Event{{Key: "k", Version: 0, Op: OpWrite}},
			reason: ReasonInvalidEvent,
		},
		{
			name:   "unknown op",
			cfg:    Config{RetentionVersions: 100},
			events: []Event{{Key: "k", Version: 1, Op: Op(99)}},
			reason: ReasonInvalidEvent,
		},
		{
			name: "entry limit exceeded",
			cfg:  Config{RetentionVersions: 100, MaxEntries: 1},
			events: []Event{
				{Key: "a", Version: 1, Op: OpWrite, Value: "1"},
				{Key: "b", Version: 1, Op: OpWrite, Value: "2"},
			},
			reason: ReasonLimitExceeded,
		},
	}

	seenReasons := map[Reason]bool{}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, _ := newLoggedStore(t, tc.cfg)
			beforeRows := s.Rows()
			beforeStones := s.Tombstones()
			beforeWM := s.Watermark()
			beforeIgnored := s.Ignored()

			err := applyLogged(t, s, tc.name, tc.events...)
			be, ok := AsBatchError(err)
			if !ok || be.Reason != tc.reason {
				t.Fatalf("want *BatchError reason %q, got %v", tc.reason, err)
			}
			seenReasons[be.Reason] = true

			if fmt.Sprint(s.Rows()) != fmt.Sprint(beforeRows) {
				t.Fatalf("rows changed after rejection: before=%v after=%v", beforeRows, s.Rows())
			}
			if fmt.Sprint(s.Tombstones()) != fmt.Sprint(beforeStones) {
				t.Fatalf("tombstones changed after rejection: before=%v after=%v", beforeStones, s.Tombstones())
			}
			if s.Watermark() != beforeWM {
				t.Fatalf("watermark changed after rejection: %d -> %d", beforeWM, s.Watermark())
			}
			if s.Ignored() != beforeIgnored {
				t.Fatalf("ignored changed after rejection: %d -> %d", beforeIgnored, s.Ignored())
			}
		})
	}
	if len(seenReasons) != 3 {
		t.Fatalf("rejection reasons must be three distinct kinds, got %v", seenReasons)
	}
}

func TestLimitExceededAfterExistingRows(t *testing.T) {
	s, _ := newLoggedStore(t, Config{RetentionVersions: 100, MaxEntries: 1})
	if err := s.Apply([]Event{{Key: "a", Version: 1, Op: OpWrite, Value: "1"}}); err != nil {
		t.Fatal(err)
	}
	if err := s.Apply([]Event{{Key: "a", Version: 2, Op: OpWrite, Value: "2"}}); err != nil {
		t.Fatalf("in-place overwrite must stay within the limit: %v", err)
	}
	err := s.Apply([]Event{{Key: "b", Version: 3, Op: OpWrite, Value: "3"}})
	if be, ok := AsBatchError(err); !ok || be.Reason != ReasonLimitExceeded {
		t.Fatalf("want limit_exceeded, got %v", err)
	}
	err = s.Apply([]Event{
		{Key: "a", Version: 4, Op: OpDelete},
		{Key: "b", Version: 5, Op: OpWrite, Value: "5"},
	})
	if err != nil {
		t.Fatalf("delete+rewrite within one batch must be allowed: %v", err)
	}
	if rows := s.Rows(); len(rows) != 1 || rows[0].Key != "b" {
		t.Fatalf("unexpected rows: %v", rows)
	}
}

func TestTombstoneExpiry(t *testing.T) {
	t.Run("delta reaches retention", func(t *testing.T) {
		s, buf := newLoggedStore(t, Config{RetentionVersions: 3})
		if err := applyLogged(t, s, "delete k v2; write other v2",
			Event{Key: "k", Version: 2, Op: OpDelete},
			Event{Key: "other", Version: 2, Op: OpWrite, Value: "o"},
		); err != nil {
			t.Fatal(err)
		}
		if len(s.Tombstones()) != 1 {
			t.Fatal("tombstone should be retained initially")
		}
		// Watermark 5: 5-2 = 3 >= retention 3 -> evict.
		if err := applyLogged(t, s, "advance watermark to 5",
			Event{Key: "other", Version: 5, Op: OpWrite, Value: "o2"},
		); err != nil {
			t.Fatal(err)
		}
		if len(s.Tombstones()) != 0 {
			t.Fatalf("expired tombstone must be evicted, got %v", s.Tombstones())
		}
		if s.CurrentVersion("k") != 0 {
			t.Fatal("evicted key must return to the unknown state (version 0)")
		}
		if _, ok := s.Get("k"); ok {
			t.Fatal("evicted key must not appear as a live row")
		}
		if !strings.Contains(buf.String(), "tombstone expired") {
			t.Fatalf("log must record expiry, got:\n%s", buf.String())
		}
	})

	t.Run("retention zero evicts immediately", func(t *testing.T) {
		s, _ := newLoggedStore(t, Config{RetentionVersions: 0})
		if err := applyLogged(t, s, "delete k v7",
			Event{Key: "k", Version: 7, Op: OpDelete},
		); err != nil {
			t.Fatal(err)
		}
		if len(s.Tombstones()) != 0 || s.CurrentVersion("k") != 0 {
			t.Fatalf("retention=0 must evict within the same batch, got %v", s.Tombstones())
		}
	})

	t.Run("below threshold stays", func(t *testing.T) {
		s, _ := newLoggedStore(t, Config{RetentionVersions: 10})
		if err := applyLogged(t, s, "delete k v4 then watermark 5",
			Event{Key: "k", Version: 4, Op: OpDelete},
			Event{Key: "g", Version: 5, Op: OpWrite, Value: "g"},
		); err != nil {
			t.Fatal(err)
		}
		if len(s.Tombstones()) != 1 || s.CurrentVersion("k") != 4 {
			t.Fatalf("delta 1 < retention 10 must retain the tombstone, got %v", s.Tombstones())
		}
	})
}

func TestTombstoneBlocksLateOldWrite(t *testing.T) {
	s, buf := newLoggedStore(t, Config{RetentionVersions: 100})

	// Delete v5 for a key that never existed: a tombstone is still built.
	if err := applyLogged(t, s, "delete v5 unknown key", Event{Key: "k", Version: 5, Op: OpDelete}); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.Get("k"); ok {
		t.Fatal("deleted key must not have a live row")
	}
	if got := s.CurrentVersion("k"); got != 5 {
		t.Fatalf("current version = %d, want tombstone version 5", got)
	}

	if err := applyLogged(t, s, "late write v3", Event{Key: "k", Version: 3, Op: OpWrite, Value: "stale"}); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.Get("k"); ok {
		t.Fatal("write older than the tombstone must not create a live row")
	}
	if s.CurrentVersion("k") != 5 || s.Ignored() != 1 {
		t.Fatalf("want tombstone v5 and 1 ignored, got v=%d ignored=%d",
			s.CurrentVersion("k"), s.Ignored())
	}

	if err := applyLogged(t, s, "write v5 equal", Event{Key: "k", Version: 5, Op: OpWrite, Value: "x"}); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.Get("k"); ok {
		t.Fatal("equal-version write must stay blocked by the tombstone")
	}
	if err := applyLogged(t, s, "write v6", Event{Key: "k", Version: 6, Op: OpWrite, Value: "new"}); err != nil {
		t.Fatal(err)
	}
	row, ok := s.Get("k")
	if !ok || row.Value != "new" || row.Version != 6 {
		t.Fatalf("newer write must win, got %+v ok=%v", row, ok)
	}
	if len(s.Tombstones()) != 0 {
		t.Fatal("winning write must clear the tombstone")
	}
	if !strings.Contains(buf.String(), "ignored: version not newer than tombstone") {
		t.Fatalf("log must explain the tombstone block, got:\n%s", buf.String())
	}
}

func TestEqualVersionIgnored(t *testing.T) {
	s, buf := newLoggedStore(t, Config{RetentionVersions: 100})

	if err := applyLogged(t, s, "write v10", Event{Key: "k", Version: 10, Op: OpWrite, Value: "a"}); err != nil {
		t.Fatal(err)
	}
	if err := applyLogged(t, s, "rewrite v10 equal", Event{Key: "k", Version: 10, Op: OpWrite, Value: "b"}); err != nil {
		t.Fatal(err)
	}
	row, ok := s.Get("k")
	if !ok || row.Value != "a" || row.Version != 10 {
		t.Fatalf("equal-version write must be ignored, got %+v ok=%v", row, ok)
	}
	if s.Ignored() != 1 {
		t.Fatalf("ignored count = %d, want 1", s.Ignored())
	}

	if err := applyLogged(t, s, "delete v10 equal", Event{Key: "k", Version: 10, Op: OpDelete}); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.Get("k"); !ok {
		t.Fatal("equal-version delete must not remove the live row")
	}
	if s.Ignored() != 2 {
		t.Fatalf("ignored count = %d, want 2", s.Ignored())
	}
	if !strings.Contains(buf.String(), "ignored: version not newer than live row") {
		t.Fatalf("log must state the decision basis, got:\n%s", buf.String())
	}
}

func dumpState(t *testing.T, s *Store, tag string) {
	t.Helper()
	t.Logf("%s: live=%v tombstones=%v watermark=%d ignored=%d",
		tag, s.Rows(), s.Tombstones(), s.Watermark(), s.Ignored())
}

func applyLogged(t *testing.T, s *Store, tag string, events ...Event) error {
	t.Helper()
	for _, ev := range events {
		t.Logf("%s input: key=%q version=%d op=%s value=%q current_version=%d",
			tag, ev.Key, ev.Version, ev.Op, ev.Value, s.CurrentVersion(ev.Key))
	}
	err := s.Apply(events)
	if err != nil {
		t.Logf("%s -> REJECTED: %v", tag, err)
	}
	dumpState(t, s, tag+" result")
	if err := s.Verify(); err != nil {
		t.Fatalf("%s self-check: %v", tag, err)
	}
	return err
}

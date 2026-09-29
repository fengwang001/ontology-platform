package ontology

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

func TestNewManagerRejectsInvalidConfigAndSnapshot(t *testing.T) {
	if _, err := NewManager(nil, Config{MaxChanges: -1}); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("want ErrInvalidConfig, got %v", err)
	}
	if _, err := NewManager(entries("b", "a"), Config{}); !errors.Is(err, ErrUnsorted) {
		t.Fatalf("want ErrUnsorted, got %v", err)
	}
	if _, err := NewManager([]Entry{{Key: "x"}, {Key: "x"}}, Config{}); !errors.Is(err, ErrDuplicateKey) {
		t.Fatalf("want ErrDuplicateKey, got %v", err)
	}
}

func TestDiffAndApplyCommitAndReplay(t *testing.T) {
	old := []Entry{{Key: "a", Value: "1"}, {Key: "b", Value: "2"}}
	m, err := NewManager(old, Config{})
	if err != nil {
		t.Fatal(err)
	}
	next := []Entry{{Key: "a", Value: "1"}, {Key: "b", Value: "22"}, {Key: "c", Value: "3"}}
	v, changes, err := m.DiffAndApply(context.Background(), next)
	if err != nil {
		t.Fatal(err)
	}
	if v != 1 {
		t.Fatalf("version after first commit = %d, want 1", v)
	}
	if got := m.Snapshot(); !equalEntries(got, next) {
		t.Fatalf("current snapshot = %v, want %v", got, next)
	}
	// 以旧快照重放日志，必须与新快照一致（可复现）
	replayed, err := Replay(old, changes)
	if err != nil {
		t.Fatal(err)
	}
	if !equalEntries(replayed, next) {
		t.Fatalf("replay = %v, want %v", replayed, next)
	}
	st := m.Stats()
	if st.Commits != 1 || st.Inserts != 1 || st.Updates != 1 || st.Deletes != 0 || st.ChangesTotal != 2 {
		t.Fatalf("unexpected stats: %+v", st)
	}
	if err := m.SelfCheck(); err != nil {
		t.Fatalf("self check: %v", err)
	}
}

func TestRejectionLeavesNoTrace(t *testing.T) {
	good := []Entry{{Key: "a", Value: "1"}, {Key: "m", Value: "2"}}
	m, err := NewManager(good, Config{MaxChanges: 2})
	if err != nil {
		t.Fatal(err)
	}

	type badCase struct {
		name string
		snap []Entry
		want error
	}
	cases := []badCase{
		{"empty key", []Entry{{Key: ""}}, ErrEmptyKey},
		{"duplicate key", []Entry{{Key: "a"}, {Key: "a"}}, ErrDuplicateKey},
		{"unsorted swapped input", entries("b", "a"), ErrUnsorted},
		{"unsorted after valid prefix",
			[]Entry{{Key: "a"}, {Key: "z"}, {Key: "m"}}, ErrUnsorted},
		{"too many changes", entries("x", "y", "z"), ErrTooManyChanges},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			beforeSnap := m.Snapshot()
			beforeVer := m.Version()
			beforeStats := m.Stats()

			v, changes, err := m.DiffAndApply(context.Background(), c.snap)
			if !errors.Is(err, c.want) {
				t.Fatalf("want %v, got %v", c.want, err)
			}
			if changes != nil || v != 0 {
				t.Fatalf("rejected call must return no changes and zero version, got %v %d", changes, v)
			}
			if !equalEntries(m.Snapshot(), beforeSnap) {
				t.Fatal("snapshot changed after rejection")
			}
			if m.Version() != beforeVer {
				t.Fatal("version changed after rejection")
			}
			if m.Stats() != beforeStats {
				t.Fatalf("stats changed after rejection: before=%+v after=%+v", beforeStats, m.Stats())
			}
			if err := m.SelfCheck(); err != nil {
				t.Fatalf("self check after rejection: %v", err)
			}
		})
	}
}

func TestSwappedInputsReportedAsUnsorted(t *testing.T) {
	asc := []Entry{{Key: "a"}, {Key: "b"}, {Key: "c"}}
	desc := []Entry{{Key: "c"}, {Key: "b"}, {Key: "a"}}
	// 正向合法；对调后第一处违规（c > b）报未排序，而不是重复键
	if err := Validate(asc); err != nil {
		t.Fatalf("ascending must be valid: %v", err)
	}
	err := Validate(desc)
	if !errors.Is(err, ErrUnsorted) {
		t.Fatalf("swapped input: want ErrUnsorted, got %v", err)
	}
	if errors.Is(err, ErrDuplicateKey) {
		t.Fatalf("swapped input must not be reported as duplicate: %v", err)
	}

	m, _ := NewManager(asc, Config{})
	_, _, err = m.DiffAndApply(context.Background(), desc)
	if !errors.Is(err, ErrUnsorted) {
		t.Fatalf("manager diff with swapped input: want ErrUnsorted, got %v", err)
	}
	if got := m.Snapshot(); !equalEntries(got, asc) {
		t.Fatalf("snapshot must remain ascending, got %v", got)
	}
}

func TestSnapshotIsolationFromCallerMutation(t *testing.T) {
	original := []Entry{{Key: "k", Value: "v"}}
	m, err := NewManager(original, Config{})
	if err != nil {
		t.Fatal(err)
	}
	original[0].Value = "mutated-input"
	if got := m.Snapshot(); got[0].Value != "v" {
		t.Fatalf("manager snapshot aliased input: %+v", got)
	}
	out := m.Snapshot()
	out[0].Value = "mutated-output"
	if got := m.Snapshot(); got[0].Value != "v" {
		t.Fatalf("manager snapshot aliased returned copy: %+v", got)
	}
}

func TestConcurrentReadsSeeWholeVersions(t *testing.T) {
	initial := make([]Entry, 50)
	for i := range initial {
		k := fmt.Sprintf("key-%03d", i)
		initial[i] = Entry{Key: k, Value: "v0"}
	}
	m, err := NewManager(initial, Config{})
	if err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	var writers sync.WaitGroup
	var readers sync.WaitGroup
	stop := atomic.Bool{}

	// 读者：反复取整份快照，读到的内容必须整份等于某个已提交版本
	// （版本 r 中每个键的值都必须是同一个 vN，不允许新旧混合）。
	for r := 0; r < 8; r++ {
		readers.Add(1)
		go func() {
			defer readers.Done()
			for !stop.Load() {
				snap := m.Snapshot()
				if err := Validate(snap); err != nil {
					t.Errorf("reader observed invalid snapshot: %v", err)
					return
				}
				val := snap[0].Value
				for _, e := range snap {
					if e.Value != val {
						t.Errorf("reader observed mixed versions: key %s=%s but first=%s", e.Key, e.Value, val)
						return
					}
				}
				_ = m.Stats()
				if err := m.SelfCheck(); err != nil {
					t.Errorf("self check failed mid-flight: %v", err)
					return
				}
			}
		}()
	}

	// 写者：每个新版本更新全部键的值；同时混入必然被拒的请求。
	for w := 1; w <= 20; w++ {
		writers.Add(1)
		go func(n int) {
			defer writers.Done()
			next := make([]Entry, 50)
			val := fmt.Sprintf("v%d", n)
			for i := range next {
				next[i] = Entry{Key: fmt.Sprintf("key-%03d", i), Value: val}
			}
			if _, _, err := m.DiffAndApply(ctx, next); err != nil {
				t.Errorf("commit %d: %v", n, err)
			}
			if _, _, err := m.DiffAndApply(ctx, entries("z", "a")); err == nil {
				t.Errorf("commit %d: invalid snapshot unexpectedly accepted", n)
			}
		}(w)
	}
	writers.Wait()
	stop.Store(true)
	readers.Wait()

	if m.Version() != 20 {
		t.Fatalf("final version = %d, want 20", m.Version())
	}
	finalVal := m.Snapshot()[0].Value
	if !strings.HasPrefix(finalVal, "v") {
		t.Fatalf("unexpected final value %q", finalVal)
	}
	st := m.Stats()
	if st.Commits != 20 {
		t.Fatalf("commits = %d, want 20 (rejections must not count)", st.Commits)
	}
	if st.ChangesTotal != st.Inserts+st.Deletes+st.Updates {
		t.Fatalf("stats invariant broken: %+v", st)
	}
	// 每版本更新全部 50 个键
	if st.Updates != 20*50 || st.ChangesTotal != 20*50 {
		t.Fatalf("unexpected change stats: %+v", st)
	}
}

func TestLoggedRejectionReason(t *testing.T) {
	var sb strings.Builder
	m, err := NewManager(entries("a"), Config{MaxChanges: 1})
	if err != nil {
		t.Fatal(err)
	}
	m.WithLogger(funcLogger(func(_ context.Context, format string, args ...any) {
		fmt.Fprintf(&sb, format+"\n", args...)
	}))
	if _, _, err := m.DiffAndApply(context.Background(), entries("x", "y")); !errors.Is(err, ErrTooManyChanges) {
		t.Fatalf("want ErrTooManyChanges, got %v", err)
	}
	if !strings.Contains(sb.String(), "reject kind=too many changes") {
		t.Fatalf("rejection log missing reason:\n%s", sb.String())
	}
	if !strings.Contains(sb.String(), "unchanged") {
		t.Fatalf("rejection log must note no-trace:\n%s", sb.String())
	}
}

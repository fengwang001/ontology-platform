package snapshot

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log"
	"math/rand"
	"strings"
	"sync"
	"testing"
)

// 对调输入报未排序；重复键与未排序由第一处违规决定，且四类错误互不相同。
func TestDiffSwappedInputReportedUnsorted(t *testing.T) {
	good := entries("a", "1", "b", "2", "c", "3")
	swapped := entries("a", "1", "c", "3", "b", "2")

	_, err := Diff(context.Background(), Config{}, good, swapped)
	assertReject(t, err, ErrUnsorted)

	_, err = Diff(context.Background(), Config{}, good, entries("a", "1", "b", "2", "b", "3"))
	assertReject(t, err, ErrDuplicateKey)

	_, err = Diff(context.Background(), Config{}, swapped, good)
	assertReject(t, err, ErrUnsorted)

	categories := []error{ErrInvalidConfig, ErrDuplicateKey, ErrUnsorted, ErrTooManyChanges}
	for i := range categories {
		for j := i + 1; j < len(categories); j++ {
			if errors.Is(categories[i], categories[j]) {
				t.Fatalf("error categories %d and %d are not distinguishable", i, j)
			}
		}
	}
}

func TestDiffInvalidConfigAndLimit(t *testing.T) {
	oldSnap := entries("a", "1", "b", "2")
	newSnap := entries("a", "2", "b", "3")

	_, err := Diff(context.Background(), Config{MaxChanges: -1}, oldSnap, newSnap)
	assertReject(t, err, ErrInvalidConfig)

	_, err = Diff(context.Background(), Config{MaxChanges: 1}, oldSnap, newSnap)
	assertReject(t, err, ErrTooManyChanges)

	changes, err := Diff(context.Background(), Config{MaxChanges: 2}, oldSnap, newSnap)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(changes) != 2 {
		t.Fatalf("expected 2 changes at limit, got %d", len(changes))
	}
}

// 任何一次拒绝都不得改变当前快照或累计统计（失败不留痕）。
func TestStoreRejectionLeavesNoTrace(t *testing.T) {
	store := NewStore(Config{})
	initial := entries("a", "1", "b", "2")
	if _, err := store.ApplyDiff(context.Background(), initial); err != nil {
		t.Fatalf("seed apply failed: %v", err)
	}
	wantStats := store.Stats()
	store.cfg = Config{MaxChanges: 1}

	bad := []struct {
		name   string
		snap   []Entry
		target error
	}{
		{"unsorted", entries("b", "2", "a", "1"), ErrUnsorted},
		{"duplicate", entries("a", "1", "a", "2"), ErrDuplicateKey},
		{"too many", entries("a", "9", "b", "8"), ErrTooManyChanges},
	}
	for _, tc := range bad {
		t.Run(tc.name, func(t *testing.T) {
			changes, err := store.ApplyDiff(context.Background(), tc.snap)
			assertReject(t, err, tc.target)
			if changes != nil {
				t.Fatalf("rejected diff must not return changes: %v", changes)
			}
			if got := store.Snapshot(); fmt.Sprint(got) != fmt.Sprint(initial) {
				t.Fatalf("snapshot changed after rejection %s: %v", tc.name, got)
			}
			if got := store.Stats(); got != wantStats {
				t.Fatalf("stats changed after rejection %s: got %+v want %+v", tc.name, got, wantStats)
			}
		})
	}

	badCfgStore := NewStore(Config{MaxChanges: -1})
	_, err := badCfgStore.ApplyDiff(context.Background(), entries("a", "1"))
	assertReject(t, err, ErrInvalidConfig)
	if got := badCfgStore.Snapshot(); len(got) != 0 {
		t.Fatalf("invalid-config rejection must leave empty snapshot, got %v", got)
	}
	if got := badCfgStore.Stats(); got != (Stats{}) {
		t.Fatalf("invalid-config rejection must leave zero stats, got %+v", got)
	}
}

func TestStoreApplyAndStats(t *testing.T) {
	store := NewStore(Config{})
	oldSnap := entries("a", "1", "b", "2", "c", "3")
	if _, err := store.ApplyDiff(context.Background(), oldSnap); err != nil {
		t.Fatal(err)
	}
	newSnap := entries("b", "2", "c", "30", "d", "4")
	changes, err := store.ApplyDiff(context.Background(), newSnap)
	if err != nil {
		t.Fatal(err)
	}
	if got := store.Snapshot(); fmt.Sprint(got) != fmt.Sprint(newSnap) {
		t.Fatalf("current snapshot mismatch:\n got %v\nwant %v", got, newSnap)
	}
	wantStats := Stats{Applied: 2, Inserted: 4, Deleted: 1, Updated: 1}
	if got := store.Stats(); got != wantStats {
		t.Fatalf("stats mismatch: got %+v want %+v", got, wantStats)
	}
	if err := store.Verify(); err != nil {
		t.Fatalf("self-check failed: %v", err)
	}
	if got := Replay(oldSnap, changes); fmt.Sprint(got) != fmt.Sprint(newSnap) {
		t.Fatalf("changes are not reproducible: %v", got)
	}

	snap := store.Snapshot()
	snap[0] = Entry{Key: "zzz", Value: "hack"}
	if got := store.Snapshot(); got[0].Key != "b" {
		t.Fatalf("internal snapshot mutated by caller: %v", got)
	}
}

// 日志打印每步输入、变更与判定依据。
func TestDiffLogging(t *testing.T) {
	var buf bytes.Buffer
	ctx := WithLogger(context.Background(), log.New(&buf, "", 0))

	if _, err := Diff(ctx, Config{}, entries("a", "1", "b", "2"), entries("b", "20", "c", "3")); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{"diff start", `old-only key="a"`, "=> delete", `shared key="b"`, "=> update", `new-only key="c"`, "tail:"} {
		if !strings.Contains(out, want) {
			t.Fatalf("log missing %q:\n%s", want, out)
		}
	}

	buf.Reset()
	if _, err := Diff(ctx, Config{}, entries("a", "1"), entries("a", "1")); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "values equal") || !strings.Contains(buf.String(), "no change") {
		t.Fatalf("equal-value path must log the reason:\n%s", buf.String())
	}

	buf.Reset()
	_, err := Diff(ctx, Config{MaxChanges: 1}, entries("a", "1", "b", "2"), entries("a", "2", "b", "3"))
	assertReject(t, err, ErrTooManyChanges)
	if !strings.Contains(buf.String(), "reject:") {
		t.Fatalf("rejection reason must be logged:\n%s", buf.String())
	}
}

// 差分与查询/自检并发：读者每次读到的都是某一时刻的完整版本，不能新旧混合。
func TestStoreConcurrentReaders(t *testing.T) {
	store := NewStore(Config{})
	base := make([]Entry, 100)
	for i := range base {
		base[i] = Entry{Key: fmt.Sprintf("k%03d", i), Value: "v0"}
	}
	if _, err := store.ApplyDiff(context.Background(), base); err != nil {
		t.Fatal(err)
	}

	stop := make(chan struct{})
	var wg sync.WaitGroup

	// 写者：持续把全部键更新为新版本号。
	wg.Add(1)
	go func() {
		defer wg.Done()
		for v := 1; ; v++ {
			select {
			case <-stop:
				return
			default:
			}
			next := make([]Entry, len(base))
			for i := range base {
				next[i] = Entry{Key: base[i].Key, Value: fmt.Sprintf("v%d", v)}
			}
			if _, err := store.ApplyDiff(context.Background(), next); err != nil {
				t.Errorf("writer apply failed: %v", err)
				return
			}
		}
	}()

	// 读者：反复取整份快照，校验整份必须属于同一个版本（无新旧混合），并严格有序。
	for r := 0; r < 4; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for k := 0; k < 200; k++ {
				snap := store.Snapshot()
				if len(snap) != len(base) {
					t.Errorf("snapshot length changed: %d", len(snap))
					return
				}
				version := snap[0].Value
				for i, e := range snap {
					if e.Value != version {
						t.Errorf("mixed-version snapshot at index %d: %q vs %q", i, e.Value, version)
						return
					}
					if e.Key != base[i].Key {
						t.Errorf("unexpected key at index %d: %q", i, e.Key)
						return
					}
				}
				if err := Validate(snap); err != nil {
					t.Errorf("self-check read invalid snapshot: %v", err)
					return
				}
				_ = store.Stats()
				if err := store.Verify(); err != nil {
					t.Errorf("verify failed concurrently: %v", err)
					return
				}
			}
		}()
	}

	close(stop)
	wg.Wait()
}

// 随机模糊：双指针结果始终等于朴素参照，且重放后等于新快照。
func TestDiffRandomMatchesNaive(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	for iter := 0; iter < 200; iter++ {
		oldSnap := randomSortedEntries(rng, rng.Intn(30))
		newSnap := randomSortedEntries(rng, rng.Intn(30))

		changes, err := Diff(context.Background(), Config{MaxChanges: 1000}, oldSnap, newSnap)
		if err != nil {
			t.Fatalf("iter %d: unexpected error: %v", iter, err)
		}
		if ref := naiveDiff(oldSnap, newSnap); fmt.Sprint(changes) != fmt.Sprint(ref) {
			t.Fatalf("iter %d: naive mismatch\n old=%v\n new=%v\n got=%v\nwant=%v", iter, oldSnap, newSnap, changes, ref)
		}
		if got := Replay(oldSnap, changes); fmt.Sprint(got) != fmt.Sprint(newSnap) {
			t.Fatalf("iter %d: replay mismatch\n got=%v\nwant=%v", iter, got, newSnap)
		}
		if err := Validate(changesToEntries(changes)); err != nil {
			t.Fatalf("iter %d: change log not strictly key-sorted or duplicate key: %v", iter, err)
		}
		seen := map[string]bool{}
		for _, ch := range changes {
			if seen[ch.Key] {
				t.Fatalf("iter %d: key %s appears twice in change log", iter, ch.Key)
			}
			seen[ch.Key] = true
		}
	}
}

func randomSortedEntries(rng *rand.Rand, n int) []Entry {
	keys := rng.Perm(60)[:n]
	out := make([]Entry, 0, n)
	for _, k := range keys {
		out = append(out, Entry{Key: fmt.Sprintf("k%02d", k), Value: fmt.Sprintf("v%d", rng.Intn(5))})
	}
	sortEntries(out)
	return out
}

func sortEntries(out []Entry) {
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j-1].Key > out[j].Key; j-- {
			out[j-1], out[j] = out[j], out[j-1]
		}
	}
}

func changesToEntries(changes []Change) []Entry {
	out := make([]Entry, len(changes))
	for i, ch := range changes {
		out[i] = Entry{Key: ch.Key}
	}
	return out
}

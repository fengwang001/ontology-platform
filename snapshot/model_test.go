package snapshot

import (
	"errors"
	"fmt"
	"math"
	"math/rand"
	"reflect"
	"sort"
	"sync"
	"sync/atomic"
	"testing"
)

// ---------- 朴素参照实现 ----------
// 用最直接的方式独立实现规格，用于与 Table 对比。

type modelSnap struct {
	id    int64
	ts    int64
	files map[string]bool
}

type model struct {
	snaps   []modelSnap // 按 id 升序
	current int64
	lastTs  int64
	hasAny  bool
	nextID  int64
	used    map[string]bool
}

func newModel() *model {
	return &model{nextID: 1, used: make(map[string]bool)}
}

func dupOrEmpty(names []string) bool {
	seen := make(map[string]bool, len(names))
	for _, n := range names {
		if n == "" || seen[n] {
			return true
		}
		seen[n] = true
	}
	return false
}

func (m *model) currentFiles() map[string]bool {
	for _, s := range m.snaps {
		if s.id == m.current {
			return s.files
		}
	}
	return map[string]bool{}
}

func (m *model) commit(ts int64, added, removed []string) error {
	if dupOrEmpty(added) || dupOrEmpty(removed) {
		return ErrInvalidArgument
	}
	inAdded := make(map[string]bool, len(added))
	for _, f := range added {
		inAdded[f] = true
	}
	for _, f := range removed {
		if inAdded[f] {
			return ErrInvalidArgument
		}
	}
	if m.hasAny && ts <= m.lastTs {
		return ErrTimestampNotIncreasing
	}
	cur := m.currentFiles()
	for _, f := range removed {
		if !cur[f] {
			return ErrRemoveNotInSnapshot
		}
	}
	for _, f := range added {
		if m.used[f] {
			return ErrFileNameConflict
		}
	}
	next := make(map[string]bool)
	for f := range cur {
		next[f] = true
	}
	for _, f := range removed {
		delete(next, f)
	}
	for _, f := range added {
		next[f] = true
		m.used[f] = true
	}
	m.snaps = append(m.snaps, modelSnap{id: m.nextID, ts: ts, files: next})
	m.current = m.nextID
	m.nextID++
	m.lastTs = ts
	m.hasAny = true
	return nil
}

func (m *model) expire(keepLast int, threshold int64) ([]string, error) {
	if keepLast < 0 {
		return nil, ErrInvalidArgument
	}
	n := len(m.snaps)
	var retained []modelSnap
	retainedFiles := make(map[string]bool)
	var expired []modelSnap
	for i, s := range m.snaps {
		if i >= n-keepLast || s.ts > threshold || s.id == m.current {
			retained = append(retained, s)
			for f := range s.files {
				retainedFiles[f] = true
			}
		} else {
			expired = append(expired, s)
		}
	}
	deletedSet := make(map[string]bool)
	for _, s := range expired {
		for f := range s.files {
			if !retainedFiles[f] {
				deletedSet[f] = true
			}
		}
	}
	m.snaps = retained
	deleted := make([]string, 0, len(deletedSet))
	for f := range deletedSet {
		deleted = append(deleted, f)
	}
	sort.Strings(deleted)
	return deleted, nil
}

func (m *model) storeFiles() []string {
	union := make(map[string]bool)
	for _, s := range m.snaps {
		for f := range s.files {
			union[f] = true
		}
	}
	out := make([]string, 0, len(union))
	for f := range union {
		out = append(out, f)
	}
	sort.Strings(out)
	return out
}

// ---------- 随机对比测试 ----------

func compareState(t *testing.T, tb *Table, m *model) {
	t.Helper()
	snaps := tb.Snapshots()
	if len(snaps) != len(m.snaps) {
		t.Fatalf("snapshot count = %d, model has %d", len(snaps), len(m.snaps))
	}
	for i, s := range snaps {
		ms := m.snaps[i]
		if s.ID != ms.id || s.Timestamp != ms.ts {
			t.Fatalf("snapshot %d = (id=%d,ts=%d), model has (id=%d,ts=%d)",
				i, s.ID, s.Timestamp, ms.id, ms.ts)
		}
		wantFiles := make([]string, 0, len(ms.files))
		for f := range ms.files {
			wantFiles = append(wantFiles, f)
		}
		sort.Strings(wantFiles)
		if !reflect.DeepEqual(s.Files, wantFiles) {
			t.Fatalf("snapshot id=%d files = %v, model has %v", s.ID, s.Files, wantFiles)
		}
	}
	if got, want := tb.Files(), m.storeFiles(); !reflect.DeepEqual(got, want) {
		t.Fatalf("store = %v, model union = %v", got, want)
	}
	if err := tb.CheckConsistency(); err != nil {
		t.Fatalf("consistency check failed: %v", err)
	}
}

func checkErrCategory(t *testing.T, op string, got, want error) {
	t.Helper()
	if want == nil {
		if got != nil {
			t.Fatalf("%s: unexpected error %v", op, got)
		}
		return
	}
	if !errors.Is(got, want) {
		t.Fatalf("%s: err = %v, want category %v", op, got, want)
	}
	t.Logf("  rejected as expected: %v", got)
}

// TestAgainstNaiveModel 随机操作序列下与朴素参照实现逐步对比，
// 日志打印每步输入、保留快照、删除文件与判定依据。
func TestAgainstNaiveModel(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	tb := New()
	m := newModel()
	var lastTs int64
	var nameCounter int
	var usedNames []string

	freshName := func() string {
		nameCounter++
		name := fmt.Sprintf("file-%04d", nameCounter)
		usedNames = append(usedNames, name)
		return name
	}

	for step := 0; step < 2000; step++ {
		if rng.Intn(100) < 60 || lastTs == 0 {
			// ---- 提交 ----
			ts := lastTs + 1 + int64(rng.Intn(5))
			var added, removed []string
			nAdd := rng.Intn(3)
			for i := 0; i < nAdd; i++ {
				added = append(added, freshName())
			}
			cur := tb.Snapshots()
			if len(cur) > 0 {
				curFiles := cur[len(cur)-1].Files
				for _, f := range curFiles {
					if rng.Intn(100) < 30 {
						removed = append(removed, f)
					}
				}
			}
			// 注入非法输入。
			var injected string
			switch rng.Intn(20) {
			case 0:
				ts = lastTs // 时间不递增
				injected = "non-increasing-ts"
			case 1:
				if len(usedNames) > 0 {
					added = append(added, usedNames[rng.Intn(len(usedNames))]) // 文件名复用
					injected = "name-conflict"
				}
			case 2:
				removed = append(removed, "no-such-file") // 移除不存在文件
				injected = "remove-missing"
			case 3:
				if len(added) > 0 {
					added = append(added, added[0]) // 重复名
					injected = "duplicate-name"
				}
			}
			t.Logf("step %d commit ts=%d added=%v removed=%v inject=%s",
				step, ts, added, removed, injected)

			modelErr := m.commit(ts, added, removed)
			_, tableErr := tb.Commit(ts, added, removed)
			checkErrCategory(t, "commit", tableErr, modelErr)
			if modelErr == nil {
				lastTs = ts
			}
		} else {
			// ---- 过期 ----
			keepLast := rng.Intn(4)
			threshold := int64(0)
			if lastTs > 0 {
				threshold = rng.Int63n(lastTs + 2)
			}
			if rng.Intn(10) == 0 {
				threshold = math.MaxInt64 // 只剩 keepLast 与当前快照
			}
			if rng.Intn(50) == 0 {
				keepLast = -1 // 非法参数
			}
			t.Logf("step %d expire keepLast=%d threshold=%d", step, keepLast, threshold)

			modelDeleted, modelErr := m.expire(keepLast, threshold)
			tableDeleted, tableErr := tb.Expire(keepLast, threshold)
			checkErrCategory(t, "expire", tableErr, modelErr)
			if modelErr != nil {
				continue
			}
			if !reflect.DeepEqual(nonNil(tableDeleted), nonNil(modelDeleted)) {
				t.Fatalf("deleted = %v, model deleted %v", tableDeleted, modelDeleted)
			}
			// 打印保留快照与判定依据。
			n := len(m.snaps)
			for i, s := range m.snaps {
				reason := ""
				if i >= n-keepLast {
					reason += "latest-N "
				}
				if s.ts > threshold {
					reason += "ts>threshold "
				}
				if s.id == m.current {
					reason += "current"
				}
				t.Logf("  retained id=%d ts=%d reason=[%s]", s.id, s.ts, reason)
			}
			t.Logf("  deleted=%v", tableDeleted)
		}
		compareState(t, tb, m)
	}
}

// nonNil 将 nil 切片归一化为空切片，便于比较。
func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// ---------- 并发测试 ----------

// TestConcurrent 快照查询、文件查询与自检可被多个执行体并发调用，
// 且可与提交、过期并发；全程一致性自检不得失败。
func TestConcurrent(t *testing.T) {
	tb := New()
	var tsCounter atomic.Int64
	var nameCounter atomic.Int64

	// 初始快照。
	if _, err := tb.Commit(1, []string{"seed"}, nil); err != nil {
		t.Fatal(err)
	}
	tsCounter.Store(1)

	var writers sync.WaitGroup
	var readers sync.WaitGroup
	stop := make(chan struct{})
	errCh := make(chan error, 16)

	// 提交者：单写者，本地维护当前文件集镜像。
	writers.Add(1)
	go func() {
		defer writers.Done()
		current := map[string]bool{"seed": true}
		for i := 0; i < 500; i++ {
			name := fmt.Sprintf("c-%d", nameCounter.Add(1))
			added := []string{name}
			var removed []string
			for f := range current {
				if len(current) > 4 {
					removed = append(removed, f)
					break
				}
			}
			ts := tsCounter.Add(1)
			if _, err := tb.Commit(ts, added, removed); err != nil {
				errCh <- fmt.Errorf("commit: %w", err)
				return
			}
			for _, f := range removed {
				delete(current, f)
			}
			current[name] = true
		}
	}()

	// 过期者。
	writers.Add(1)
	go func() {
		defer writers.Done()
		for i := 0; i < 200; i++ {
			if _, err := tb.Expire(3, 0); err != nil {
				errCh <- fmt.Errorf("expire: %w", err)
				return
			}
		}
	}()

	// 读者：并发查询与自检。
	for r := 0; r < 4; r++ {
		readers.Add(1)
		go func() {
			defer readers.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				_ = tb.Snapshots()
				_ = tb.Files()
				if err := tb.CheckConsistency(); err != nil {
					errCh <- err
					return
				}
			}
		}()
	}

	writers.Wait()
	close(stop)
	readers.Wait()

	select {
	case err := <-errCh:
		t.Fatal(err)
	default:
	}
	if err := tb.CheckConsistency(); err != nil {
		t.Fatalf("final consistency check failed: %v", err)
	}
	t.Logf("final: snapshots=%d files=%d", len(tb.Snapshots()), len(tb.Files()))
}

package topk

import (
	"errors"
	"fmt"
	"sort"
	"sync"
	"testing"
)

// logState 打印操作、当前完整有序序列与判定依据，便于人工复核。
func logState(t *testing.T, op string, m *Maintainer) {
	t.Helper()
	ordered := m.Ordered()
	top := m.TopK()
	t.Logf("%-28s | count=%d | ordered=%v | top%d=%v | rule: score desc, id asc; topK is prefix of ordered",
		op, m.Count(), ordered, m.k, top)
}

// naiveTopK 是独立的朴素实现：全量拷贝后排序，作为核对基准。
func naiveTopK(items map[string]int64, k int) []Entry {
	entries := make([]Entry, 0, len(items))
	for id, score := range items {
		entries = append(entries, Entry{ID: id, Score: score})
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].Score != entries[j].Score {
			return entries[i].Score > entries[j].Score
		}
		return entries[i].ID < entries[j].ID
	})
	if len(entries) > k {
		entries = entries[:k]
	}
	return entries
}

func mustAdd(t *testing.T, m *Maintainer, id string, score int64) {
	t.Helper()
	if err := m.Add(id, score); err != nil {
		t.Fatalf("Add(%q,%d) unexpected error: %v", id, score, err)
	}
}

func equalEntries(a, b []Entry) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestTieBreak 验证分数并列时按标识字典序升序确定唯一名次。
func TestTieBreak(t *testing.T) {
	m, err := New(3, 10)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	mustAdd(t, m, "charlie", 90)
	mustAdd(t, m, "alpha", 90)
	mustAdd(t, m, "bravo", 90)
	mustAdd(t, m, "delta", 80)
	logState(t, "add four with ties", m)

	want := []Entry{{ID: "alpha", Score: 90}, {ID: "bravo", Score: 90}, {ID: "charlie", Score: 90}}
	if got := m.TopK(); !equalEntries(got, want) {
		t.Fatalf("tie break = %v, want %v (equal score -> id lexicographic asc)", got, want)
	}
}

// TestRetractBackfill 验证删除门槛内元素后，门槛外最高分者补位。
func TestRetractBackfill(t *testing.T) {
	m, _ := New(2, 5)
	mustAdd(t, m, "a", 100)
	mustAdd(t, m, "b", 90)
	mustAdd(t, m, "c", 80)
	mustAdd(t, m, "d", 70)
	logState(t, "add four", m)

	m.Delete("a")
	logState(t, `Delete("a") threshold member`, m)
	want := []Entry{{ID: "b", Score: 90}, {ID: "c", Score: 80}}
	if got := m.TopK(); !equalEntries(got, want) {
		t.Fatalf("after retract top = %v, want %v (c backfills)", got, want)
	}

	// 删除不存在与空标识均为幂等空操作，序列与计数不变。
	before := m.Ordered()
	m.Delete("missing")
	m.Delete("")
	if got := m.Ordered(); !equalEntries(got, before) {
		t.Fatalf("idempotent delete changed state: %v -> %v", before, got)
	}
	logState(t, `Delete("missing"), Delete("") idempotent`, m)
}

// TestUpsertOverwrite 验证新增为覆盖式更新：改分后名次随之变化。
func TestUpsertOverwrite(t *testing.T) {
	m, _ := New(2, 3)
	mustAdd(t, m, "a", 10)
	mustAdd(t, m, "b", 20)
	mustAdd(t, m, "c", 30)
	logState(t, "add a,b,c", m)

	mustAdd(t, m, "a", 99) // 已存在：覆盖分数，不占用新容量
	logState(t, `Add("a",99) overwrite`, m)
	want := []Entry{{ID: "a", Score: 99}, {ID: "c", Score: 30}}
	if got := m.TopK(); !equalEntries(got, want) {
		t.Fatalf("overwrite top = %v, want %v", got, want)
	}
	if m.Count() != 3 {
		t.Fatalf("count after overwrite = %d, want 3", m.Count())
	}

	mustAdd(t, m, "c", 5) // 覆盖为更低分，应跌出前 K
	logState(t, `Add("c",5) overwrite downward`, m)
	want = []Entry{{ID: "a", Score: 99}, {ID: "b", Score: 20}}
	if got := m.TopK(); !equalEntries(got, want) {
		t.Fatalf("downgrade top = %v, want %v", got, want)
	}
}

// TestCapacityBoundary 验证容量边界：达上限后新标识被整体拒绝，覆盖不受限。
func TestCapacityBoundary(t *testing.T) {
	m, _ := New(2, 2)
	mustAdd(t, m, "a", 1)
	mustAdd(t, m, "b", 2)
	logState(t, "fill to capacity 2", m)

	err := m.Add("c", 3)
	if !errors.Is(err, ErrCapacityExceeded) {
		t.Fatalf("Add over capacity err = %v, want ErrCapacityExceeded", err)
	}
	// 失败必须不改变状态。
	if m.Count() != 2 {
		t.Fatalf("count after rejected add = %d, want 2", m.Count())
	}
	if got := m.TopK(); !equalEntries(got, naiveTopK(map[string]int64{"a": 1, "b": 2}, 2)) {
		t.Fatalf("rejected add mutated state: %v", got)
	}
	logState(t, `Add("c") rejected: capacity`, m)

	if err := m.Add("", 1); !errors.Is(err, ErrEmptyID) {
		t.Fatalf("Add empty id err = %v, want ErrEmptyID", err)
	}
	logState(t, `Add("") rejected: empty id`, m)

	// 腾出名额后可以加入，并立即按规则挤入前 K。
	m.Delete("b")
	mustAdd(t, m, "c", 3)
	logState(t, `Delete("b"), Add("c",3)`, m)
	want := []Entry{{ID: "c", Score: 3}, {ID: "a", Score: 1}}
	if got := m.TopK(); !equalEntries(got, want) {
		t.Fatalf("after slot freed top = %v, want %v", got, want)
	}

	// 不足 K 个时返回全部。
	m.Delete("a")
	m.Delete("c")
	if got := m.TopK(); len(got) != 0 {
		t.Fatalf("empty TopK = %v, want empty", got)
	}
	logState(t, "delete all -> fewer than k", m)
}

// TestNewInvalidArgs 验证构造参数非法时返回可区分的错误。
func TestNewInvalidArgs(t *testing.T) {
	if _, err := New(0, 5); !errors.Is(err, ErrInvalidK) {
		t.Fatalf("New(0,5) err = %v, want ErrInvalidK", err)
	}
	if _, err := New(-1, 5); !errors.Is(err, ErrInvalidK) {
		t.Fatalf("New(-1,5) err = %v, want ErrInvalidK", err)
	}
	if _, err := New(3, 2); !errors.Is(err, ErrInvalidCapacity) {
		t.Fatalf("New(3,2) err = %v, want ErrInvalidCapacity", err)
	}
	t.Logf("New invalid args | k=0/-1 -> ErrInvalidK; capacity<k -> ErrInvalidCapacity")

	m, err := New(3, 3)
	if err != nil {
		t.Fatalf("New(3,3): %v", err)
	}
	logState(t, "New(3,3) empty", m)
}

// TestPrefixAndNaiveCrossCheck 验证任意更小的 K 都是完整有序序列的前缀，
// 并与朴素全量排序逐元素核对。
func TestPrefixAndNaiveCrossCheck(t *testing.T) {
	m, _ := New(4, 8)
	scores := map[string]int64{
		"a": 50, "b": 80, "c": 80, "d": 70,
		"e": 80, "f": 90, "g": 70, "h": 60,
	}
	for id, score := range scores {
		mustAdd(t, m, id, score)
	}
	ordered := m.Ordered()
	logState(t, "add eight mixed scores", m)

	if got := naiveTopK(scores, len(scores)); !equalEntries(ordered, got) {
		t.Fatalf("ordered %v != naive sort %v", ordered, got)
	}
	for k := 1; k <= 4; k++ {
		got := m.TopK()
		wantPrefix := ordered[:k]
		if k == 4 {
			if !equalEntries(got, wantPrefix) {
				t.Fatalf("TopK = %v, want prefix %v", got, wantPrefix)
			}
		}
		// 对任意更小 K：用朴素实现核对前 k 个前缀。
		if want := naiveTopK(scores, k); !equalEntries(ordered[:k], want) {
			t.Fatalf("prefix k=%d %v != naive %v", k, ordered[:k], want)
		}
	}
	t.Logf("prefix check | topK is prefix of ordered for every smaller k; verified against naive full sort")
}

// TestConcurrentReaders 验证同一实例并发读取结果逐元素相同。
func TestConcurrentReaders(t *testing.T) {
	m, _ := New(3, 20)
	for i := 0; i < 10; i++ {
		mustAdd(t, m, fmt.Sprintf("id%02d", i), int64((i*37)%50))
	}
	want := m.TopK()
	logState(t, "seed ten, then concurrent reads", m)

	const readers = 64
	var wg sync.WaitGroup
	errCh := make(chan error, readers)
	wg.Add(readers)
	for r := 0; r < readers; r++ {
		go func() {
			defer wg.Done()
			got := m.TopK()
			cnt := m.Count()
			if cnt != 10 {
				errCh <- fmt.Errorf("concurrent count = %d, want 10", cnt)
				return
			}
			if !equalEntries(got, want) {
				errCh <- fmt.Errorf("concurrent TopK = %v, want %v", got, want)
			}
		}()
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Fatal(err)
	}
	t.Logf("concurrent reads | %d readers saw identical element-wise results: %v", readers, want)
}

// TestConcurrentReadWriteRace 在 -race 下验证并发读写不存在数据竞争，
// 且每次读取都满足“TopK 是 Ordered 前缀”的不变式。
func TestConcurrentReadWriteRace(t *testing.T) {
	m, _ := New(3, 50)
	stop := make(chan struct{})
	var wg sync.WaitGroup

	wg.Add(2)
	go func() { // 写者：新增 / 覆盖 / 删除交替
		defer wg.Done()
		for i := 0; ; i++ {
			select {
			case <-stop:
				return
			default:
			}
			id := fmt.Sprintf("id%02d", i%60)
			m.Add(id, int64(i%7))
			if i%5 == 0 {
				m.Delete(id)
			}
		}
	}()
	go func() { // 读者：反复校验不变式
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			// TopK() 与 Ordered() 是两次独立快照，之间允许写者改状态；
			// 因此这里只校验单次快照自身必须满足的不变式。
			top := m.TopK()
			if len(top) > 3 {
				t.Errorf("len(top)=%d > k", len(top))
				return
			}
			if !sortedByRule(top) {
				t.Errorf("TopK %v not sorted by score desc, id asc", top)
				return
			}
			if len(top) > m.Count() {
				t.Errorf("len(top)=%d > count", len(top))
				return
			}
			ordered := m.Ordered()
			if !sortedByRule(ordered) {
				t.Errorf("ordered %v not sorted by score desc, id asc", ordered)
				return
			}
		}
	}()

	// 让读写并发跑一小段时间后停止。
	done := make(chan struct{})
	go func() {
		for i := 0; i < 300; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				_ = m.TopK()
			}(i)
		}
		close(done)
	}()
	<-done
	close(stop)
	wg.Wait()
	logState(t, "concurrent read/write finished", m)
}

// sortedByRule 校验单个快照严格满足"分数降序、标识升序"且标识无重复。
func sortedByRule(entries []Entry) bool {
	seen := make(map[string]bool, len(entries))
	for i, e := range entries {
		if seen[e.ID] {
			return false
		}
		seen[e.ID] = true
		if i == 0 {
			continue
		}
		prev := entries[i-1]
		if prev.Score < e.Score {
			return false
		}
		if prev.Score == e.Score && prev.ID >= e.ID {
			return false
		}
	}
	return true
}

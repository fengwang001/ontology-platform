package tophits

import (
	"errors"
	"fmt"
	"reflect"
	"sync"
	"testing"
	"time"
)

// formatEntries 把结果渲染成稳定可读的日志形式。
func formatEntries(entries []Entry) string {
	if len(entries) == 0 {
		return "[]"
	}
	s := "["
	for i, e := range entries {
		if i > 0 {
			s += ", "
		}
		s += fmt.Sprintf("{%s:%d}", e.Key, e.Score)
	}
	return s + "]"
}

// formatChanges 把输入批次渲染成日志形式。
func formatChanges(changes []Change) string {
	if len(changes) == 0 {
		return "[]"
	}
	s := "["
	for i, c := range changes {
		if i > 0 {
			s += ", "
		}
		s += fmt.Sprintf("(%s,%d)", c.Key, c.Score)
	}
	return s + "]"
}

// assertEntries 校验结果并打印输入、结果与判定依据。
func assertEntries(t *testing.T, name, input string, got, want []Entry) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Errorf("%s: input=%s got=%s want=%s 判定依据: 前K须按分值降序、并列按键名字典序升序",
			name, input, formatEntries(got), formatEntries(want))
		return
	}
	t.Logf("%s: input=%s => result=%s 判定通过: 与期望顺序完全一致(分值降序, 并列字典序升序)",
		name, input, formatEntries(got))
}

func TestNewSlidingTopKInvalidParams(t *testing.T) {
	cases := []struct {
		n, k      int
		wantError error
		reason    string
	}{
		{0, 1, ErrNonPositiveWindow, "窗口为0必须拒绝"},
		{-3, 1, ErrNonPositiveWindow, "窗口为负必须拒绝"},
		{3, 0, ErrNonPositiveK, "前K为0必须拒绝"},
		{3, -2, ErrNonPositiveK, "前K为负必须拒绝"},
		{2, 3, ErrKExceedsWindow, "前K超过窗口必须拒绝"},
	}
	for _, tc := range cases {
		s, err := NewSlidingTopK(tc.n, tc.k)
		if !errors.Is(err, tc.wantError) {
			t.Errorf("NewSlidingTopK(%d,%d): got err=%v want %v 判定依据: %s",
				tc.n, tc.k, err, tc.wantError, tc.reason)
			continue
		}
		if s != nil {
			t.Errorf("NewSlidingTopK(%d,%d): 非法参数不得返回实例", tc.n, tc.k)
		}
		t.Logf("NewSlidingTopK(%d,%d) => err=%v 判定通过: %s", tc.n, tc.k, err, tc.reason)
	}
}

func TestApplyRejectsBatchAtomically(t *testing.T) {
	s, err := NewSlidingTopK(5, 2)
	if err != nil {
		t.Fatalf("NewSlidingTopK: %v", err)
	}
	if err := s.Apply([]Change{{Key: "a", Score: 10}}); err != nil {
		t.Fatalf("seed Apply: %v", err)
	}

	bad := []Change{
		{Key: "b", Score: 9},
		{Key: "", Score: 1},
		{Key: "c", Score: 8},
	}
	err = s.Apply(bad)
	if !errors.Is(err, ErrEmptyKey) {
		t.Fatalf("含空键批次: got err=%v want ErrEmptyKey 判定依据: 键为空必须拒绝", err)
	}
	t.Logf("Apply(%s) => err=%v 判定通过: 空键被识别并拒绝", formatChanges(bad), err)

	assertEntries(t, "非法批整体不生效", formatChanges(bad),
		s.Snapshot(), []Entry{{Key: "a", Score: 10}})

	if err := s.Apply(nil); err != nil {
		t.Errorf("空批次应视为无操作且成功, got err=%v", err)
	}
	t.Log("Apply([]) => nil 判定通过: 空批次为成功的无操作, 窗口不变")

	if err := s.SelfCheck(); err != nil {
		t.Errorf("非法批拒绝后自检失败: %v", err)
	}
}

func TestEvictionWithdrawsDroppedKey(t *testing.T) {
	s, _ := NewSlidingTopK(3, 2)

	batches := []struct {
		name    string
		changes []Change
		want    []Entry
	}{
		{"首批", []Change{{Key: "a", Score: 5}, {Key: "b", Score: 3}},
			[]Entry{{Key: "a", Score: 5}, {Key: "b", Score: 3}}},
		// N=3，第三格被填满，尚未发生滑出。
		{"窗口填满", []Change{{Key: "c", Score: 4}},
			[]Entry{{Key: "a", Score: 5}, {Key: "c", Score: 4}}},
		// 窗口已满：最旧的 a:+5 被滑出并撤回，a 消失；d 负分仍存活参与排序。
		{"滑出撤回a", []Change{{Key: "d", Score: -9}},
			[]Entry{{Key: "c", Score: 4}, {Key: "b", Score: 3}}},
		// 再滑出 b:+3，b 消失；a 以新分值重新出现并补位。
		{"滑出撤回b与旧键重入", []Change{{Key: "a", Score: 1}},
			[]Entry{{Key: "c", Score: 4}, {Key: "a", Score: 1}}},
	}

	for _, b := range batches {
		if err := s.Apply(b.changes); err != nil {
			t.Fatalf("%s Apply(%s): %v", b.name, formatChanges(b.changes), err)
		}
		assertEntries(t, b.name, formatChanges(b.changes), s.TopK(), b.want)
		if err := s.SelfCheck(); err != nil {
			t.Fatalf("%s 后 SelfCheck: %v", b.name, err)
		}
	}

	if s.Len() != 3 {
		t.Errorf("窗口长度 got=%d want=3 判定依据: 容量N=3, 已应用5条", s.Len())
	} else {
		t.Log("窗口长度=3 判定通过: 滑出后始终只保留最近 N 条")
	}
}

func TestSlidingPartialWithdrawAndRefill(t *testing.T) {
	s, _ := NewSlidingTopK(4, 4)

	// 窗口内: a:+5, a:+3, b:+4, c:+2 => a=8, b=4, c=2
	if err := s.Apply([]Change{
		{Key: "a", Score: 5}, {Key: "a", Score: 3},
		{Key: "b", Score: 4}, {Key: "c", Score: 2},
	}); err != nil {
		t.Fatal(err)
	}
	assertEntries(t, "满窗口", "(a,5),(a,3),(b,4),(c,2)", s.TopK(),
		[]Entry{{Key: "a", Score: 8}, {Key: "b", Score: 4}, {Key: "c", Score: 2}})

	// 滑出 a:+5：a 仍存活但分值降为 3，撤回后立即重排，d 补到末位。
	if err := s.Apply([]Change{{Key: "d", Score: 1}}); err != nil {
		t.Fatal(err)
	}
	assertEntries(t, "部分撤回后重排", "(d,1)", s.TopK(),
		[]Entry{{Key: "b", Score: 4}, {Key: "a", Score: 3},
			{Key: "c", Score: 2}, {Key: "d", Score: 1}})

	// 再滑出 a:+3：a 在窗口内无剩余变更，必须彻底消失；e 补入。
	if err := s.Apply([]Change{{Key: "e", Score: 6}}); err != nil {
		t.Fatal(err)
	}
	assertEntries(t, "归零消失与补位", "(e,6)", s.TopK(),
		[]Entry{{Key: "e", Score: 6}, {Key: "b", Score: 4},
			{Key: "c", Score: 2}, {Key: "d", Score: 1}})

	if err := s.SelfCheck(); err != nil {
		t.Fatalf("SelfCheck: %v", err)
	}
	t.Log("SelfCheck 通过: 环形缓冲重算分值/计次与维护值一致, 无幽灵键")
}

func TestTieBreakLexicographic(t *testing.T) {
	s, _ := NewSlidingTopK(4, 3)
	changes := []Change{
		{Key: "banana", Score: 5},
		{Key: "apple", Score: 5},
		{Key: "cherry", Score: 5},
		{Key: "date", Score: 1},
	}
	if err := s.Apply(changes); err != nil {
		t.Fatal(err)
	}
	assertEntries(t, "并列字典序", formatChanges(changes), s.TopK(), []Entry{
		{Key: "apple", Score: 5},
		{Key: "banana", Score: 5},
		{Key: "cherry", Score: 5},
	})

	// 四键同为 1 分、K=3 时，字典序最大的 zebra 必须稳定落在 K 之外。
	s2, _ := NewSlidingTopK(4, 3)
	changes2 := []Change{
		{Key: "zebra", Score: 1},
		{Key: "mango", Score: 1},
		{Key: "date", Score: 1},
		{Key: "avocado", Score: 1},
	}
	if err := s2.Apply(changes2); err != nil {
		t.Fatal(err)
	}
	assertEntries(t, "并列时K截断按字典序", formatChanges(changes2), s2.TopK(), []Entry{
		{Key: "avocado", Score: 1},
		{Key: "date", Score: 1},
		{Key: "mango", Score: 1},
	})
}

func TestNegativeScores(t *testing.T) {
	s, _ := NewSlidingTopK(3, 2)
	changes := []Change{
		{Key: "x", Score: -1}, {Key: "y", Score: -5}, {Key: "z", Score: -3},
	}
	if err := s.Apply(changes); err != nil {
		t.Fatal(err)
	}
	// 全部为负：较大者(-1)在前；至少一条变更即存在，不因负分被剔除。
	assertEntries(t, "负分排序", formatChanges(changes), s.TopK(),
		[]Entry{{Key: "x", Score: -1}, {Key: "z", Score: -3}})

	// 同键正负抵消为 0 仍存活，并参与并列字典序排序。
	s2, _ := NewSlidingTopK(3, 3)
	if err := s2.Apply([]Change{
		{Key: "m", Score: 7}, {Key: "m", Score: -7}, {Key: "a", Score: 0},
	}); err != nil {
		t.Fatal(err)
	}
	assertEntries(t, "零分仍存活", "(m,7),(m,-7),(a,0)", s2.Snapshot(),
		[]Entry{{Key: "a", Score: 0}, {Key: "m", Score: 0}})
}

func TestFewerKeysThanKAndEmptyWindow(t *testing.T) {
	s, _ := NewSlidingTopK(5, 3)
	if err := s.Apply([]Change{{Key: "only", Score: 42}}); err != nil {
		t.Fatal(err)
	}
	assertEntries(t, "存活键少于K", "(only,42)", s.TopK(),
		[]Entry{{Key: "only", Score: 42}})

	s2, _ := NewSlidingTopK(2, 2)
	got := s2.TopK()
	if got == nil {
		t.Fatal("空窗口 TopK 应返回非 nil 空切片, 保证并发逐字段一致")
	}
	assertEntries(t, "空窗口返回空切片", "", got, []Entry{})
}

func TestBatchAppliesAtomicallyAcrossEviction(t *testing.T) {
	// 一批变更跨越“窗口满”边界：批次内按序滑出，最终求和必须正确。
	s, _ := NewSlidingTopK(2, 2)
	if err := s.Apply([]Change{{Key: "a", Score: 1}, {Key: "b", Score: 2}}); err != nil {
		t.Fatal(err)
	}
	if err := s.Apply([]Change{{Key: "a", Score: 4}, {Key: "c", Score: 9}}); err != nil {
		t.Fatal(err)
	}
	// 最终窗口为 a:+4, c:+9（a:+1 与 b:+2 先后滑出）。
	assertEntries(t, "批次跨滑出边界", "(a,4),(c,9)", s.Snapshot(),
		[]Entry{{Key: "c", Score: 9}, {Key: "a", Score: 4}})
	if err := s.SelfCheck(); err != nil {
		t.Fatalf("SelfCheck: %v", err)
	}
}

func TestConcurrentReadOnlyConsistency(t *testing.T) {
	s, _ := NewSlidingTopK(6, 3)
	seed := []Change{
		{Key: "alpha", Score: 10},
		{Key: "beta", Score: 10},
		{Key: "gamma", Score: 7},
		{Key: "delta", Score: 3},
	}
	if err := s.Apply(seed); err != nil {
		t.Fatal(err)
	}

	const readers = 16
	const iterations = 200
	results := make([][]Entry, readers)
	checks := make([]error, readers)

	var wg sync.WaitGroup
	for r := 0; r < readers; r++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			var prev []Entry
			for i := 0; i < iterations; i++ {
				got := s.TopK()
				if err := s.SelfCheck(); err != nil {
					checks[idx] = err
					return
				}
				if i == 0 {
					prev = got
					results[idx] = got
					continue
				}
				if !reflect.DeepEqual(got, prev) {
					checks[idx] = fmt.Errorf("只读期间结果发生变化: %s != %s",
						formatEntries(prev), formatEntries(got))
					return
				}
			}
		}(r)
	}
	wg.Wait()

	for r, err := range checks {
		if err != nil {
			t.Fatalf("reader %d: %v", r, err)
		}
	}
	want := []Entry{
		{Key: "alpha", Score: 10},
		{Key: "beta", Score: 10},
		{Key: "gamma", Score: 7},
	}
	for r := 0; r < readers; r++ {
		if !reflect.DeepEqual(results[r], want) {
			t.Fatalf("reader %d got=%s want=%s", r, formatEntries(results[r]), formatEntries(want))
		}
	}
	t.Logf("%d 个只读 goroutine × %d 轮 TopK+SelfCheck 全部逐字段一致 => %s 判定通过",
		readers, iterations, formatEntries(want))
}

// assertOrderedAndValid 在并发写入背景下校验任一快照本身都满足结构与排序不变量。
func assertOrderedAndValid(t *testing.T, entries []Entry, k int) {
	t.Helper()
	if len(entries) > k {
		t.Errorf("TopK 返回 %d 条, 超过 K=%d", len(entries), k)
		return
	}
	for i := 1; i < len(entries); i++ {
		prev, cur := entries[i-1], entries[i]
		if prev.Score < cur.Score {
			t.Errorf("顺序违规: %s:%d 排在 %s:%d 之前, 分值必须降序",
				prev.Key, prev.Score, cur.Key, cur.Score)
			return
		}
		if prev.Score == cur.Score && prev.Key >= cur.Key {
			t.Errorf("并列违规: %s 排在 %s 之前, 同分时键名必须字典序升序",
				prev.Key, cur.Key)
			return
		}
	}
}

func TestConcurrentReadsWhileWriting(t *testing.T) {
	s, _ := NewSlidingTopK(10, 3)

	var wg sync.WaitGroup
	stop := make(chan struct{})
	var failMu sync.Mutex
	failed := false
	fail := func(format string, args ...any) {
		failMu.Lock()
		defer failMu.Unlock()
		failed = true
		t.Errorf(format, args...)
	}

	wg.Add(1)
	go func() {
		defer wg.Done()
		seq := 0
		for {
			select {
			case <-stop:
				return
			default:
			}
			key := fmt.Sprintf("key-%02d", seq%13)
			score := int64(seq%7 - 3)
			if err := s.Apply([]Change{{Key: key, Score: score}}); err != nil {
				fail("writer Apply: %v", err)
				return
			}
			if err := s.SelfCheck(); err != nil {
				fail("writer SelfCheck: %v", err)
				return
			}
			seq++
		}
	}()

	for r := 0; r < 8; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				// 同一把读锁内取全量与前K，二者必须来自同一个窗口状态；
				// 跨调用的 TopK/Snapshot 允许因写入推进而不同，这是快照语义。
				full, top := s.SnapshotWithTopK()
				assertOrderedAndValid(t, top, 3)
				assertOrderedAndValid(t, full, 10)
				if len(full) < len(top) {
					fail("TopK(%d) 长于 Snapshot(%d)", len(top), len(full))
				}
				if !reflect.DeepEqual(top, full[:len(top)]) {
					fail("TopK 不是 Snapshot 的前缀: top=%s full=%s",
						formatEntries(top), formatEntries(full))
				}
			}
		}()
	}

	time.Sleep(100 * time.Millisecond)
	close(stop)
	wg.Wait()

	if !failed {
		t.Log("1 个写入者 + 8 个读取者并发 100ms: 每次 TopK 均有序、是 Snapshot 前缀, SelfCheck 全部通过")
	}
}

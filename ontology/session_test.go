package ontology

import (
	"errors"
	"fmt"
	"math/rand"
	"reflect"
	"sync"
	"testing"
	"time"
)

// logCase 打印输入事件、每个会话的区间与判定依据。
func logCase(t *testing.T, name string, gap int64, events []Event, sessions []Session) {
	t.Helper()
	t.Logf("用例 %q: gap=%d", name, gap)
	for i, e := range events {
		t.Logf("  输入事件[%d]: key=%q ts=%d", i, e.Key, e.Ts)
	}
	for i, sess := range sessions {
		basis := "首事件自成会话"
		links := ""
		for j := 1; j < len(sess.Events); j++ {
			d := sess.Events[j] - sess.Events[j-1]
			rel := "<"
			if d == gap {
				rel = "=="
			}
			links += fmt.Sprintf("%d%sgap(%d)相连; ", d, rel, gap)
		}
		if links != "" {
			basis = links
		}
		if i+1 < len(sessions) {
			g := sessions[i+1].Start - sess.End
			basis += fmt.Sprintf("与下一段间隔 %d > gap(%d)，断开", g, gap)
		}
		t.Logf("  会话[%d]: key=%q 区间=[%d,%d] 事件=%v 判定: %s",
			i, sess.Key, sess.Start, sess.End, sess.Events, basis)
	}
}

func addAll(t *testing.T, s *Splitter, events []Event, name string) {
	t.Helper()
	if err := s.AddEvents(events); err != nil {
		t.Fatalf("%s: AddEvents 失败: %v", name, err)
	}
}

func sessionRanges(sessions []Session) [][]int64 {
	out := make([][]int64, len(sessions))
	for i, sess := range sessions {
		out[i] = []int64{sess.Start, sess.End}
	}
	return out
}

// TestOutOfOrderMerge 乱序到达：迟到事件插入两个已有会话之间并把它们合并。
func TestOutOfOrderMerge(t *testing.T) {
	const gap int64 = 5
	s, err := NewSplitter(gap, 4)
	if err != nil {
		t.Fatal(err)
	}

	addAll(t, s, []Event{{Key: "k", Ts: 1}, {Key: "k", Ts: 3}, {Key: "k", Ts: 2}}, "首批")
	addAll(t, s, []Event{{Key: "k", Ts: 12}, {Key: "k", Ts: 11}}, "第二批")

	got := s.Sessions("k")
	if want := [][]int64{{1, 3}, {11, 12}}; !reflect.DeepEqual(sessionRanges(got), want) {
		t.Fatalf("合并前期望 %v, 实际 %v", want, sessionRanges(got))
	}

	// 迟到事件 7：与左段差 4<=5、与右段差 4<=5，跨会话合并为一段。
	addAll(t, s, []Event{{Key: "k", Ts: 7}}, "迟到合并事件")

	got = s.Sessions("k")
	if !reflect.DeepEqual(sessionRanges(got), [][]int64{{1, 12}}) {
		t.Fatalf("合并后期望 [[1 12]], 实际 %v", sessionRanges(got))
	}
	if wantEvents := []int64{1, 2, 3, 7, 11, 12}; !reflect.DeepEqual(got[0].Events, wantEvents) {
		t.Fatalf("合并后事件期望 %v, 实际 %v", wantEvents, got[0].Events)
	}
	logCase(t, "乱序跨会话合并", gap,
		[]Event{{Key: "k", Ts: 1}, {Key: "k", Ts: 3}, {Key: "k", Ts: 2},
			{Key: "k", Ts: 12}, {Key: "k", Ts: 11}, {Key: "k", Ts: 7}},
		got)

	if err := s.SelfCheck(); err != nil {
		t.Fatalf("SelfCheck 失败: %v", err)
	}
}

// TestGapBoundary 恰好等于 gap 仍属同一会话；gap+1 断开；边界点可合并两段。
func TestGapBoundary(t *testing.T) {
	const gap int64 = 10
	s, _ := NewSplitter(gap, 2)
	addAll(t, s, []Event{{Key: "a", Ts: 0}, {Key: "a", Ts: gap}, {Key: "a", Ts: 2 * gap}}, "恰好等于gap")

	got := s.Sessions("a")
	if len(got) != 1 || got[0].Start != 0 || got[0].End != 2*gap {
		t.Fatalf("|Δ|==gap 应相连, 实际: %+v", got)
	}
	logCase(t, "恰好等于间隙", gap,
		[]Event{{Key: "a", Ts: 0}, {Key: "a", Ts: gap}, {Key: "a", Ts: 2 * gap}}, got)

	addAll(t, s, []Event{{Key: "a", Ts: 3*gap + 1}}, "超出gap一个单位")
	got = s.Sessions("a")
	if !reflect.DeepEqual(sessionRanges(got), [][]int64{{0, 2 * gap}, {3*gap + 1, 3*gap + 1}}) {
		t.Fatalf("|Δ|==gap+1 应断开, 实际: %v", sessionRanges(got))
	}

	// 乱序插入恰好等于 gap 的边界点 30（距 20 为 10、距 31 为 1），把两段重新连上。
	addAll(t, s, []Event{{Key: "a", Ts: 3 * gap}}, "迟到边界点")
	got = s.Sessions("a")
	if len(got) != 1 || got[0].End != 3*gap+1 {
		t.Fatalf("边界点应合并两段, 实际: %+v", got)
	}

	if err := s.SelfCheck(); err != nil {
		t.Fatalf("SelfCheck 失败: %v", err)
	}
}

// TestSameTimestamp 相同时刻事件归入同一会话（且去重）。
func TestSameTimestamp(t *testing.T) {
	const gap int64 = 3
	s, _ := NewSplitter(gap, 2)
	addAll(t, s, []Event{
		{Key: "x", Ts: 5}, {Key: "x", Ts: 5}, {Key: "x", Ts: 5},
		{Key: "x", Ts: 5 + gap + 1},
		{Key: "x", Ts: 5},
	}, "相同时刻")

	got := s.Sessions("x")
	if !reflect.DeepEqual(sessionRanges(got), [][]int64{{5, 5}, {9, 9}}) {
		t.Fatalf("相同时刻会话划分错误: %v", sessionRanges(got))
	}
	if len(got[0].Events) != 1 {
		t.Fatalf("相同时刻应去重, 实际: %v", got[0].Events)
	}
	logCase(t, "相同时刻", gap,
		[]Event{{Key: "x", Ts: 5}, {Key: "x", Ts: 5}, {Key: "x", Ts: 9},
			{Key: "x", Ts: 5}}, got)

	if err := s.SelfCheck(); err != nil {
		t.Fatalf("SelfCheck 失败: %v", err)
	}
}

// TestOneSideAndIsolated 只连一侧时并入该侧；两侧都不连时自成新会话。
func TestOneSideAndIsolated(t *testing.T) {
	const gap int64 = 4
	s, _ := NewSplitter(gap, 2)
	addAll(t, s, []Event{{Key: "k", Ts: 0}, {Key: "k", Ts: 100}}, "初始两段")

	// 6 与两侧都不连：自成新会话。
	addAll(t, s, []Event{{Key: "k", Ts: 6}}, "孤立事件")
	got := s.Sessions("k")
	if !reflect.DeepEqual(sessionRanges(got), [][]int64{{0, 0}, {6, 6}, {100, 100}}) {
		t.Fatalf("孤立事件应自成会话: %v", sessionRanges(got))
	}

	// 96 仅与 100 差 4 == gap 相连：只并入右侧。
	addAll(t, s, []Event{{Key: "k", Ts: 96}}, "只并入右侧")
	got = s.Sessions("k")
	if !reflect.DeepEqual(sessionRanges(got), [][]int64{{0, 0}, {6, 6}, {96, 100}}) {
		t.Fatalf("单侧相连应并入该侧: %v", sessionRanges(got))
	}
	logCase(t, "单侧相连与孤立", gap,
		[]Event{{Key: "k", Ts: 0}, {Key: "k", Ts: 100}, {Key: "k", Ts: 6},
			{Key: "k", Ts: 96}}, got)

	if err := s.SelfCheck(); err != nil {
		t.Fatalf("SelfCheck 失败: %v", err)
	}
}

// TestInvalidRejectedAtomic 非法参数与容量超限必须整体拒绝且不改变状态。
func TestInvalidRejectedAtomic(t *testing.T) {
	if _, err := NewSplitter(0, 2); !errors.Is(err, ErrNonPositiveGap) {
		t.Fatalf("gap=0 应返回 ErrNonPositiveGap, 实际 %v", err)
	}
	if _, err := NewSplitter(-1, 2); !errors.Is(err, ErrNonPositiveGap) {
		t.Fatalf("gap<0 应返回 ErrNonPositiveGap, 实际 %v", err)
	}
	if _, err := NewSplitter(5, 0); !errors.Is(err, ErrKeyCapacityExceeded) {
		t.Fatalf("maxKeys=0 应返回 ErrKeyCapacityExceeded, 实际 %v", err)
	}

	s, _ := NewSplitter(5, 2)
	addAll(t, s, []Event{{Key: "a", Ts: 1}}, "已有数据")

	if err := s.AddEvents([]Event{{Key: "", Ts: 1}}); !errors.Is(err, ErrEmptyKey) {
		t.Fatalf("空键应返回 ErrEmptyKey, 实际 %v", err)
	}
	if err := s.AddEvents([]Event{{Key: "b", Ts: 1}, {Key: "c", Ts: 1}}); !errors.Is(err, ErrKeyCapacityExceeded) {
		t.Fatalf("超容量应返回 ErrKeyCapacityExceeded, 实际 %v", err)
	}
	if err := s.AddEvents([]Event{{Key: "b", Ts: 1}, {Key: "", Ts: 2}}); !errors.Is(err, ErrEmptyKey) {
		t.Fatalf("混合非法批次应返回 ErrEmptyKey, 实际 %v", err)
	}

	got := s.AllSessions()
	if len(got) != 1 || got[0].Key != "a" {
		t.Fatalf("失败批次不得改变状态, 实际: %+v", got)
	}

	if err := s.AddEvents([]Event{{Key: "b", Ts: 1}}); err != nil {
		t.Fatalf("边界容量内添加应成功: %v", err)
	}
	before := s.Sessions("b")
	if err := s.AddEvents([]Event{{Key: "a", Ts: 2}, {Key: "c", Ts: 3}}); !errors.Is(err, ErrKeyCapacityExceeded) {
		t.Fatalf("超限批次应拒绝, 实际 %v", err)
	}
	if !reflect.DeepEqual(s.Sessions("b"), before) {
		t.Fatalf("超限批次不得改变任何已有键的状态")
	}
	if len(s.Sessions("c")) != 0 {
		t.Fatalf("新键 c 不得被引入")
	}
}

// TestOrderIndependence 同一批事件以任意顺序到达（整批打乱 / 逐条乱序），
// 会话集合必须与排序后批量切分的参考结果一致。
func TestOrderIndependence(t *testing.T) {
	const gap int64 = 7
	base := []int64{0, 3, 11, 12, 50, 58, 59, 100, 107, 108, 109}

	want, err := SplitSorted(append([]int64(nil), base...), gap)
	if err != nil {
		t.Fatal(err)
	}
	for i := range want {
		want[i].Key = "k"
	}

	rng := rand.New(rand.NewSource(time.Now().UnixNano()))
	for trial := 0; trial < 200; trial++ {
		s, _ := NewSplitter(gap, 1)
		perm := rng.Perm(len(base))
		if trial%2 == 0 {
			batch := make([]Event, len(base))
			for i, p := range perm {
				batch[i] = Event{Key: "k", Ts: base[p]}
			}
			if err := s.AddEvents(batch); err != nil {
				t.Fatal(err)
			}
		} else {
			for _, p := range perm {
				if err := s.AddEvents([]Event{{Key: "k", Ts: base[p]}}); err != nil {
					t.Fatal(err)
				}
			}
		}
		got := s.Sessions("k")
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("trial %d 顺序 %v: 结果不一致\n got=%+v\nwant=%+v",
				trial, perm, got, want)
		}
		if err := s.SelfCheck(); err != nil {
			t.Fatalf("trial %d SelfCheck: %v", trial, err)
		}
	}

	all, _ := NewSplitter(gap, 1)
	for _, ts := range base {
		if err := all.AddEvents([]Event{{Key: "k", Ts: ts}}); err != nil {
			t.Fatal(err)
		}
	}
	logCase(t, "乱序到达对照（升序展示）", gap,
		[]Event{{Key: "k", Ts: 0}, {Key: "k", Ts: 3}, {Key: "k", Ts: 11}, {Key: "k", Ts: 12},
			{Key: "k", Ts: 50}, {Key: "k", Ts: 58}, {Key: "k", Ts: 59}, {Key: "k", Ts: 100},
			{Key: "k", Ts: 107}, {Key: "k", Ts: 108}, {Key: "k", Ts: 109}},
		all.Sessions("k"))
}

// TestMultiKeyIsolation 不同键之间互不影响。
func TestMultiKeyIsolation(t *testing.T) {
	s, _ := NewSplitter(3, 3)
	if err := s.AddEvents([]Event{
		{Key: "a", Ts: 0}, {Key: "a", Ts: 10},
		{Key: "b", Ts: 0}, {Key: "b", Ts: 2}, {Key: "b", Ts: 4},
	}); err != nil {
		t.Fatal(err)
	}
	if ranges := sessionRanges(s.Sessions("a")); !reflect.DeepEqual(ranges, [][]int64{{0, 0}, {10, 10}}) {
		t.Fatalf("键 a 切分错误: %v", ranges)
	}
	if ranges := sessionRanges(s.Sessions("b")); !reflect.DeepEqual(ranges, [][]int64{{0, 4}}) {
		t.Fatalf("键 b 切分错误: %v", ranges)
	}
	if len(s.Sessions("missing")) != 0 {
		t.Fatalf("未知键应返回空结果")
	}
	if err := s.SelfCheck(); err != nil {
		t.Fatalf("SelfCheck 失败: %v", err)
	}
}

// TestConcurrentReadsAndWrites 并发写入、查询与自检；
// 同一稳定快照下并发读取的结果必须逐字段相同，且通过 -race 检测。
func TestConcurrentReadsAndWrites(t *testing.T) {
	s, _ := NewSplitter(5, 2)

	var wg sync.WaitGroup
	stop := make(chan struct{})

	// 持续并发写入（数据空间小，含大量乱序/迟到/重复）。
	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func(seed int64) {
			defer wg.Done()
			local := rand.New(rand.NewSource(seed))
			for {
				select {
				case <-stop:
					return
				default:
					ts := local.Int63n(200)
					key := []string{"a", "b"}[local.Intn(2)]
					_ = s.AddEvents([]Event{{Key: key, Ts: ts}})
				}
			}
		}(int64(w + 100))
	}

	// 持续并发查询与自检。
	for r := 0; r < 4; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 500; i++ {
				if err := s.SelfCheck(); err != nil {
					t.Errorf("并发 SelfCheck 失败: %v", err)
					return
				}
				_ = s.AllSessions()
			}
		}()
	}

	// 停止写入，等待所有 goroutine 收敛。
	close(stop)
	wg.Wait()

	snapshot := s.AllSessions()
	if err := s.SelfCheck(); err != nil {
		t.Fatalf("最终 SelfCheck 失败: %v", err)
	}
	var wg2 sync.WaitGroup
	results := make([][]Session, 8)
	for i := range results {
		wg2.Add(1)
		go func(idx int) {
			defer wg2.Done()
			results[idx] = s.AllSessions()
		}(i)
	}
	wg2.Wait()
	for i := 1; i < len(results); i++ {
		if !reflect.DeepEqual(results[0], results[i]) {
			t.Fatalf("并发读取结果不一致: %+v vs %+v", results[0], results[i])
		}
		if !reflect.DeepEqual(results[i], snapshot) {
			t.Fatalf("读取结果与稳定快照不一致")
		}
	}
}

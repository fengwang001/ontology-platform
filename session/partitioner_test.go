package session_test

import (
	"errors"
	"math/rand"
	"reflect"
	"sort"
	"sync"
	"testing"

	"ontology/session"
)

// refSpan 是参考实现输出的会话区间。
type refSpan struct {
	start, end int64
	count      int
}

// referenceSplit 是本地对照参考实现：把同一键的事件时间排序后一趟批量切分。
// 排序后相同时刻相邻（差为 0 <= gap），自然归入同一会话。
func referenceSplit(times []int64, gap int64) []refSpan {
	if len(times) == 0 {
		return nil
	}
	ts := append([]int64(nil), times...)
	sort.Slice(ts, func(i, j int) bool { return ts[i] < ts[j] })
	out := make([]refSpan, 0, len(ts))
	start, end, count := ts[0], ts[0], 1
	for _, v := range ts[1:] {
		if v-end <= gap {
			end, count = v, count+1
		} else {
			out = append(out, refSpan{start, end, count})
			start, end, count = v, v, 1
		}
	}
	return append(out, refSpan{start, end, count})
}

// logDecision 在摄入单条事件前打印左右邻会话与相连判定依据。
func logDecision(t *testing.T, spans []session.Session, ts, gap int64) {
	t.Helper()
	if len(spans) == 0 {
		t.Logf("  判定: 该键暂无会话 => 自成新会话 [%d,%d]", ts, ts)
		return
	}
	var left, right *session.Session
	for i := range spans {
		if spans[i].Start <= ts {
			left = &spans[i]
		}
		if spans[i].Start > ts {
			right = &spans[i]
			break
		}
	}
	leftOK, rightOK := false, false
	if left != nil {
		leftOK = ts-left.End <= gap
		t.Logf("  左邻 [%d,%d]: %d-(%d)=%d <= gap=%d ? %v",
			left.Start, left.End, ts, left.End, ts-left.End, gap, leftOK)
	} else {
		t.Logf("  左邻: 无")
	}
	if right != nil {
		rightOK = right.Start-ts <= gap
		t.Logf("  右邻 [%d,%d]: %d-(%d)=%d <= gap=%d ? %v",
			right.Start, right.End, right.Start, ts, right.Start-ts, gap, rightOK)
	} else {
		t.Logf("  右邻: 无")
	}
	switch {
	case leftOK && rightOK:
		t.Logf("  判定: 与左右两侧都相连 => 合并两侧会话")
	case leftOK:
		t.Logf("  判定: 仅与左侧相连 => 并入左侧会话")
	case rightOK:
		t.Logf("  判定: 仅与右侧相连 => 并入右侧会话")
	default:
		t.Logf("  判定: 两侧都不相连 => 自成新会话")
	}
}

// replayWithLog 逐条摄入事件，打印每条输入事件与逐步判定依据。
func replayWithLog(t *testing.T, p *session.Partitioner, gap int64, events []session.Event) {
	t.Helper()
	for i, e := range events {
		t.Logf("步骤 %d: 输入事件 key=%q time=%d", i+1, e.Key, e.Time)
		logDecision(t, p.Sessions(e.Key), e.Time, gap)
		if err := p.Add(e); err != nil {
			t.Fatalf("Add(%+v) 失败: %v", e, err)
		}
	}
}

func mustNew(t *testing.T, gap int64, capacity int) *session.Partitioner {
	t.Helper()
	p, err := session.NewPartitioner(gap, capacity)
	if err != nil {
		t.Fatalf("NewPartitioner(%d, %d) 失败: %v", gap, capacity, err)
	}
	return p
}

// 乱序到达的事件与左右两侧会话都相连时，应把两侧合并为一个会话。
func TestPartitioner_OutOfOrderMergeBothSides(t *testing.T) {
	const gap = 15
	p := mustNew(t, gap, 10)
	events := []session.Event{
		{Key: "u1", Time: 0},
		{Key: "u1", Time: 30},
		{Key: "u1", Time: 15}, // 乱序迟到：15-0=15<=15 且 30-15=15<=15，合并两侧
	}
	replayWithLog(t, p, gap, events)
	t.Log("最终会话:")
	logSessions(t, p, gap)

	got := p.Sessions("u1")
	if len(got) != 1 || got[0].Start != 0 || got[0].End != 30 || got[0].Count != 3 {
		t.Fatalf("期望单会话 [0,30]x3，实际 %+v", got)
	}
	requireMatchRef(t, got, []int64{0, 30, 15}, gap)
	if err := p.Check(); err != nil {
		t.Fatalf("自检失败: %v", err)
	}
}

// 链式乱序：后到的中间事件逐段把多个独立会话桥接合并。
func TestPartitioner_MergeChain(t *testing.T) {
	const gap = 10
	p := mustNew(t, gap, 10)
	events := []session.Event{
		{Key: "u1", Time: 0},
		{Key: "u1", Time: 30},
		{Key: "u1", Time: 10}, // 仅连左侧 => [0,10] [30,30]
		{Key: "u1", Time: 20}, // 两侧都连 => 合并为 [0,30]
	}
	replayWithLog(t, p, gap, events)
	t.Log("最终会话:")
	logSessions(t, p, gap)

	got := p.Sessions("u1")
	if len(got) != 1 || got[0].Start != 0 || got[0].End != 30 || got[0].Count != 4 {
		t.Fatalf("期望单会话 [0,30]x4，实际 %+v", got)
	}
	requireMatchRef(t, got, []int64{0, 30, 10, 20}, gap)
}

// 恰好等于间隙时仍属同一会话（闭区间），超过间隙则切分。
func TestPartitioner_ExactGapBoundary(t *testing.T) {
	const gap = 10
	cases := []struct {
		name  string
		times []int64
		want  []refSpan
	}{
		{"正序恰好等于间隙", []int64{0, 10}, []refSpan{{0, 10, 2}}},
		{"乱序恰好等于间隙", []int64{10, 0}, []refSpan{{0, 10, 2}}},
		{"超过间隙1则切分", []int64{0, 11}, []refSpan{{0, 0, 1}, {11, 11, 1}}},
		{"边界链", []int64{0, 10, 21}, []refSpan{{0, 10, 2}, {21, 21, 1}}},
		{"乱序边界链", []int64{21, 0, 10}, []refSpan{{0, 10, 2}, {21, 21, 1}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := mustNew(t, gap, 10)
			events := make([]session.Event, len(tc.times))
			for i, ts := range tc.times {
				events[i] = session.Event{Key: "u1", Time: ts}
			}
			t.Logf("输入事件时间: %v (gap=%d)", tc.times, gap)
			replayWithLog(t, p, gap, events)
			t.Log("最终会话:")
			logSessions(t, p, gap)

			got := p.Sessions("u1")
			if len(got) != len(tc.want) {
				t.Fatalf("会话数: got %d, want %d", len(got), len(tc.want))
			}
			for i, w := range tc.want {
				if got[i].Start != w.start || got[i].End != w.end || got[i].Count != w.count {
					t.Fatalf("会话 %d: got [%d,%d]x%d, want [%d,%d]x%d",
						i+1, got[i].Start, got[i].End, got[i].Count, w.start, w.end, w.count)
				}
			}
			requireMatchRef(t, got, tc.times, gap)
		})
	}
}

// 相同时刻的事件必须归入同一会话，且计数正确。
func TestPartitioner_SameTimestamp(t *testing.T) {
	const gap = 10
	t.Run("全部相同时刻", func(t *testing.T) {
		p := mustNew(t, gap, 10)
		events := []session.Event{
			{Key: "u1", Time: 5},
			{Key: "u1", Time: 5},
			{Key: "u1", Time: 5},
		}
		replayWithLog(t, p, gap, events)
		t.Log("最终会话:")
		logSessions(t, p, gap)

		got := p.Sessions("u1")
		if len(got) != 1 || got[0].Start != 5 || got[0].End != 5 || got[0].Count != 3 {
			t.Fatalf("期望单会话 [5,5]x3，实际 %+v", got)
		}
		requireMatchRef(t, got, []int64{5, 5, 5}, gap)
	})
	t.Run("相同时刻后再扩展", func(t *testing.T) {
		p := mustNew(t, gap, 10)
		events := []session.Event{
			{Key: "u1", Time: 5},
			{Key: "u1", Time: 5},
			{Key: "u1", Time: 15}, // 15-5=10<=gap，并入
		}
		replayWithLog(t, p, gap, events)
		t.Log("最终会话:")
		logSessions(t, p, gap)

		got := p.Sessions("u1")
		if len(got) != 1 || got[0].Start != 5 || got[0].End != 15 || got[0].Count != 3 {
			t.Fatalf("期望单会话 [5,15]x3，实际 %+v", got)
		}
		requireMatchRef(t, got, []int64{5, 5, 15}, gap)
	})
	t.Run("相同时刻不跨越间隙", func(t *testing.T) {
		p := mustNew(t, gap, 10)
		events := []session.Event{
			{Key: "u1", Time: 5},
			{Key: "u1", Time: 5},
			{Key: "u1", Time: 16}, // 16-5=11>gap，自成新会话
		}
		replayWithLog(t, p, gap, events)
		t.Log("最终会话:")
		logSessions(t, p, gap)

		got := p.Sessions("u1")
		if len(got) != 2 || got[0].Count != 2 || got[1].Start != 16 || got[1].Count != 1 {
			t.Fatalf("期望 [5,5]x2 与 [16,16]x1，实际 %+v", got)
		}
		requireMatchRef(t, got, []int64{5, 5, 16}, gap)
	})
}

// permutations 生成同一批事件的多种到达顺序：正序、逆序与若干随机打乱。
func permutations(events []session.Event, seed int64) [][]session.Event {
	sorted := append([]session.Event(nil), events...)
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].Time != sorted[j].Time {
			return sorted[i].Time < sorted[j].Time
		}
		return sorted[i].Key < sorted[j].Key
	})
	reversed := make([]session.Event, len(sorted))
	for i, e := range sorted {
		reversed[len(sorted)-1-i] = e
	}
	perms := [][]session.Event{sorted, reversed}
	rng := rand.New(rand.NewSource(seed))
	for k := 0; k < 8; k++ {
		shuffled := append([]session.Event(nil), events...)
		rng.Shuffle(len(shuffled), func(i, j int) { shuffled[i], shuffled[j] = shuffled[j], shuffled[i] })
		perms = append(perms, shuffled)
	}
	return perms
}

// 同一批事件按任意顺序到达，会话集合必须一致，且与
// “按事件时间排序后批量切分”的参考实现逐字段相同。
func TestPartitioner_OrderIndependence(t *testing.T) {
	const gap = 10
	keys := []string{"alpha", "beta", "gamma"}
	rng := rand.New(rand.NewSource(42))
	events := make([]session.Event, 0, 64)
	for _, k := range keys {
		for i := 0; i < 20; i++ {
			events = append(events, session.Event{Key: k, Time: int64(rng.Intn(120))})
		}
	}
	// 注入相同时刻与恰好等于间隙的边界事件。
	events = append(events,
		session.Event{Key: "alpha", Time: 7},
		session.Event{Key: "alpha", Time: 7},
		session.Event{Key: "beta", Time: 50},
		session.Event{Key: "beta", Time: 60},
	)

	perms := permutations(events, 7)
	t.Logf("共 %d 条事件、%d 种到达顺序", len(events), len(perms))

	var baseline map[string][]session.Session
	for pi, perm := range perms {
		p := mustNew(t, gap, 10)
		if err := p.Add(perm...); err != nil {
			t.Fatalf("排列 %d 摄入失败: %v", pi, err)
		}
		if err := p.Check(); err != nil {
			t.Fatalf("排列 %d 自检失败: %v", pi, err)
		}
		got := p.AllSessions()
		if pi == 0 {
			baseline = got
			continue
		}
		if !reflect.DeepEqual(baseline, got) {
			t.Fatalf("排列 %d 与基准结果不一致:\n基准 %+v\n实际 %+v", pi, baseline, got)
		}
	}

	// 与参考实现逐字段对照。
	for _, k := range keys {
		var times []int64
		for _, e := range events {
			if e.Key == k {
				times = append(times, e.Time)
			}
		}
		requireMatchRef(t, baseline[k], times, gap)
	}

	t.Log("最终会话（任意到达顺序下一致）:")
	p := mustNew(t, gap, 10)
	if err := p.Add(events...); err != nil {
		t.Fatal(err)
	}
	logSessions(t, p, gap)
}

// 非法参数、空键、超容量新键必须整体拒绝、原因可区分，且失败不改变状态。
func TestPartitioner_AtomicRejection(t *testing.T) {
	t.Run("间隙非正", func(t *testing.T) {
		for _, gap := range []int64{0, -1, -100} {
			if _, err := session.NewPartitioner(gap, 10); !errors.Is(err, session.ErrInvalidGap) {
				t.Fatalf("gap=%d: 期望 ErrInvalidGap，实际 %v", gap, err)
			}
		}
	})
	t.Run("容量非正", func(t *testing.T) {
		if _, err := session.NewPartitioner(10, 0); !errors.Is(err, session.ErrInvalidCapacity) {
			t.Fatalf("期望 ErrInvalidCapacity，实际 %v", err)
		}
	})
	t.Run("空键拒绝且状态不变", func(t *testing.T) {
		p := mustNew(t, 10, 4)
		if err := p.Add(session.Event{Key: "k1", Time: 1}); err != nil {
			t.Fatal(err)
		}
		before := p.AllSessions()

		// 批量中混有空键：整批拒绝，合法事件也不得生效。
		err := p.Add(
			session.Event{Key: "k1", Time: 2},
			session.Event{Key: "", Time: 3},
			session.Event{Key: "k2", Time: 4},
		)
		if !errors.Is(err, session.ErrEmptyKey) {
			t.Fatalf("期望 ErrEmptyKey，实际 %v", err)
		}
		if errors.Is(err, session.ErrCapacityExceeded) || errors.Is(err, session.ErrInvalidGap) {
			t.Fatalf("拒绝原因不可区分: %v", err)
		}
		if after := p.AllSessions(); !reflect.DeepEqual(before, after) {
			t.Fatalf("失败后状态被改变:\n之前 %+v\n之后 %+v", before, after)
		}
	})
	t.Run("超容量新键拒绝且状态不变", func(t *testing.T) {
		p := mustNew(t, 10, 2)
		if err := p.Add(
			session.Event{Key: "k1", Time: 1},
			session.Event{Key: "k2", Time: 2},
		); err != nil {
			t.Fatal(err)
		}
		before := p.AllSessions()

		// 已有键仍可摄入。
		if err := p.Add(session.Event{Key: "k1", Time: 5}); err != nil {
			t.Fatalf("已有键摄入应成功: %v", err)
		}
		// 批量中混入新键 k3：整批拒绝，同批的 k1 事件也不得生效。
		before = p.AllSessions()
		err := p.Add(
			session.Event{Key: "k1", Time: 8},
			session.Event{Key: "k3", Time: 9},
		)
		if !errors.Is(err, session.ErrCapacityExceeded) {
			t.Fatalf("期望 ErrCapacityExceeded，实际 %v", err)
		}
		if errors.Is(err, session.ErrEmptyKey) {
			t.Fatalf("拒绝原因不可区分: %v", err)
		}
		if after := p.AllSessions(); !reflect.DeepEqual(before, after) {
			t.Fatalf("失败后状态被改变:\n之前 %+v\n之后 %+v", before, after)
		}
		if err := p.Check(); err != nil {
			t.Fatalf("自检失败: %v", err)
		}
	})
}

// 并发查询与自检：结果必须与基准逐字段相同（配合 -race 运行）。
func TestPartitioner_ConcurrentReads(t *testing.T) {
	const gap = 10
	p := mustNew(t, gap, 16)
	rng := rand.New(rand.NewSource(99))
	events := make([]session.Event, 0, 2000)
	for i := 0; i < 2000; i++ {
		events = append(events, session.Event{
			Key:  string(rune('a' + rng.Intn(8))),
			Time: int64(rng.Intn(5000)),
		})
	}
	if err := p.Add(events...); err != nil {
		t.Fatal(err)
	}
	if err := p.Check(); err != nil {
		t.Fatalf("自检失败: %v", err)
	}

	baselineAll := p.AllSessions()
	baselineKeys := p.Keys()
	keys := baselineKeys

	const workers = 32
	const iterations = 50
	var wg sync.WaitGroup
	errs := make(chan error, workers*iterations)
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				if got := p.AllSessions(); !reflect.DeepEqual(baselineAll, got) {
					errs <- errors.New("AllSessions 结果与基准不一致")
					return
				}
				if got := p.Keys(); !reflect.DeepEqual(baselineKeys, got) {
					errs <- errors.New("Keys 结果与基准不一致")
					return
				}
				for _, k := range keys {
					if got := p.Sessions(k); !reflect.DeepEqual(baselineAll[k], got) {
						errs <- errors.New("Sessions 结果与基准不一致")
						return
					}
				}
				if err := p.Check(); err != nil {
					errs <- err
					return
				}
			}
		}(w)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	t.Logf("%d 个 goroutine 各执行 %d 轮并发查询与自检，结果逐字段一致", workers, iterations)
}

// logSessions 打印每个会话的区间、事件数与相邻切分依据。
func logSessions(t *testing.T, p *session.Partitioner, gap int64) {
	t.Helper()
	for _, k := range p.Keys() {
		ss := p.Sessions(k)
		for i, s := range ss {
			t.Logf("  会话 %d: key=%s 区间=[%d,%d] 事件数=%d", i+1, k, s.Start, s.End, s.Count)
			if i > 0 {
				prev := ss[i-1]
				t.Logf("    与上一会话间隔 %d-(%d)=%d > gap=%d => 不相连，独立成会话",
					s.Start, prev.End, s.Start-prev.End, gap)
			}
		}
	}
}

// requireMatchRef 断言切分结果与参考实现逐字段一致。
func requireMatchRef(t *testing.T, got []session.Session, times []int64, gap int64) {
	t.Helper()
	want := referenceSplit(times, gap)
	if len(got) != len(want) {
		t.Fatalf("会话数不一致: got %d, want %d (参考实现)", len(got), len(want))
	}
	for i, w := range want {
		g := got[i]
		if g.Start != w.start || g.End != w.end || g.Count != w.count {
			t.Fatalf("会话 %d 不一致: got [%d,%d]x%d, want [%d,%d]x%d",
				i+1, g.Start, g.End, g.Count, w.start, w.end, w.count)
		}
	}
}

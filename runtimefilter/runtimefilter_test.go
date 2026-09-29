package runtimefilter

import (
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

// row 是探测/构建侧行：key 为 nil 表示空键。
type row struct {
	key     *int64
	payload string
}

func keyOfRow(r Row) (int64, bool) {
	rr := r.(row)
	if rr.key == nil {
		return 0, false
	}
	return *rr.key, true
}

func i64(v int64) *int64 { return &v }

type bufLogger struct {
	mu sync.Mutex
	b  strings.Builder
}

func (l *bufLogger) Logf(format string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.b.WriteString(fmt.Sprintf(format, args...) + "\n")
}

func (l *bufLogger) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

func setOf(keys ...int64) map[int64]struct{} {
	m := make(map[int64]struct{}, len(keys))
	for _, k := range keys {
		m[k] = struct{}{}
	}
	return m
}

func newTestCoordinator(t *testing.T, jt JoinType, n, maxDist int, wait time.Duration) (*Coordinator[int64], *bufLogger) {
	t.Helper()
	lg := &bufLogger{}
	c, err := NewCoordinator[int64](Config[int64]{
		JoinType: jt, ShardCount: n, MaxDistinct: maxDist, ReadyWait: wait, Logger: lg,
	})
	if err != nil {
		t.Fatalf("NewCoordinator: %v", err)
	}
	return c, lg
}

func reportShards(t *testing.T, c *Coordinator[int64], reports ...ShardReport[int64]) {
	t.Helper()
	for _, r := range reports {
		if err := c.Report(r); err != nil {
			t.Fatalf("Report shard %d: %v", r.Shard, err)
		}
	}
}

// batchOf: int 表示普通键，nil 表示空键。
func batchOf(vals ...any) []Row {
	rows := make([]Row, 0, len(vals))
	for _, v := range vals {
		switch x := v.(type) {
		case int:
			rows = append(rows, row{key: i64(int64(x))})
		case int64:
			rows = append(rows, row{key: i64(x)})
		case nil:
			rows = append(rows, row{})
		}
	}
	return rows
}

func keysOf(t *testing.T, rows []Row) []any {
	t.Helper()
	out := make([]any, 0, len(rows))
	for _, r := range rows {
		k, ok := keyOfRow(r)
		if !ok {
			out = append(out, nil)
		} else {
			out = append(out, k)
		}
	}
	return out
}

// 内连接：就绪前整批放行；就绪后空键与不在摘要内的行被丢弃。
func TestInnerJoinFilters(t *testing.T) {
	c, lg := newTestCoordinator(t, JoinInner, 2, 100, time.Second)
	sc := c.NewScanner("probe", keyOfRow)

	out, st := sc.FilterBatch(batchOf(1, 99, nil), time.Time{})
	if st.Passed != 3 || st.Dropped != 0 {
		t.Fatalf("before ready: %+v", st)
	}

	reportShards(t, c,
		ShardReport[int64]{Shard: 0, Min: 1, Max: 10, Distinct: setOf(1, 2, 3, 10)},
		ShardReport[int64]{Shard: 1, Min: 5, Max: 20, Distinct: setOf(5, 20)},
	)

	out, st = sc.FilterBatch(batchOf(1, 20, 99, nil), time.Time{})
	if got := keysOf(t, out); fmt.Sprint(got) != "[1 20]" {
		t.Fatalf("after ready keys=%v", got)
	}
	if st.Scanned != 4 || st.Passed != 2 || st.Dropped != 2 {
		t.Fatalf("after ready stats: %+v", st)
	}
	tot := sc.Stats()
	if tot.Passed+tot.Dropped != tot.Scanned || tot.Scanned != 7 {
		t.Fatalf("invariant broken: %+v", tot)
	}
	if !strings.Contains(lg.String(), "decision=drop reason=null-key") ||
		!strings.Contains(lg.String(), "decision=pass reason=in-summary") ||
		!strings.Contains(lg.String(), "filter ready") {
		t.Fatalf("log missing decision basis:\n%s", lg.String())
	}
}

// 左外/右外/全外连接与反连接：探测侧是保留侧，一律不过滤。
func TestOuterAndAntiNotFiltered(t *testing.T) {
	for _, jt := range []JoinType{JoinLeftOuter, JoinRightOuter, JoinFullOuter, JoinAnti} {
		c, _ := newTestCoordinator(t, jt, 1, 100, time.Millisecond)
		sc := c.NewScanner("probe", keyOfRow)
		if err := c.Report(ShardReport[int64]{Shard: 0, Min: 1, Max: 2, Distinct: setOf(1, 2)}); err != nil {
			t.Fatal(err)
		}
		out, st := sc.FilterBatch(batchOf(1, 99, nil), time.Time{})
		if st.Passed != 3 || len(out) != 3 {
			t.Fatalf("join=%s must not filter: %+v", jt, st)
		}
	}
	if !Filterable(JoinInner) || !Filterable(JoinSemi) ||
		Filterable(JoinAnti) || Filterable(JoinLeftOuter) {
		t.Fatal("Filterable derivation wrong")
	}
}

// 构建侧全部为空：内连接/半连接探测行全部丢弃。
func TestBuildSideEmpty(t *testing.T) {
	for _, jt := range []JoinType{JoinInner, JoinSemi} {
		c, _ := newTestCoordinator(t, jt, 2, 100, time.Millisecond)
		sc := c.NewScanner("probe", keyOfRow)
		reportShards(t, c,
			ShardReport[int64]{Shard: 0, Empty: true},
			ShardReport[int64]{Shard: 1, Empty: true},
		)
		out, st := sc.FilterBatch(batchOf(1, 2, nil), time.Time{})
		if len(out) != 0 || st.Dropped != 3 {
			t.Fatalf("join=%s empty build must drop all: %+v", jt, st)
		}
	}
}

// 去重键数超上限：降级为仅最小/最大值（区间内放行）。
func TestDistinctOverLimitDegrades(t *testing.T) {
	c, lg := newTestCoordinator(t, JoinInner, 2, 3, time.Millisecond)
	sc := c.NewScanner("probe", keyOfRow)
	reportShards(t, c,
		ShardReport[int64]{Shard: 0, Min: 1, Max: 10, Distinct: setOf(1, 2)},
		ShardReport[int64]{Shard: 1, Min: 1, Max: 10, Distinct: setOf(3, 4)},
	)
	// 并集 4 个键 > 上限 3：降级为 [1,10]，7 不在去重集内仍放行。
	out, _ := sc.FilterBatch(batchOf(1, 7, 11, nil), time.Time{})
	if got := keysOf(t, out); fmt.Sprint(got) != "[1 7]" {
		t.Fatalf("degraded filter keys=%v", got)
	}
	if !strings.Contains(lg.String(), "minmax(degraded)") {
		t.Fatalf("degradation not logged:\n%s", lg.String())
	}
}

// 某分片放弃：过滤器作废，全部放行。
func TestShardAbandon(t *testing.T) {
	c, lg := newTestCoordinator(t, JoinInner, 2, 100, time.Millisecond)
	sc := c.NewScanner("probe", keyOfRow)
	reportShards(t, c,
		ShardReport[int64]{Shard: 0, Min: 1, Max: 2, Distinct: setOf(1, 2)},
		ShardReport[int64]{Shard: 1, Abandon: true},
	)
	out, st := sc.FilterBatch(batchOf(1, 99, nil), time.Time{})
	if len(out) != 3 || st.Passed != 3 {
		t.Fatalf("abandoned filter must pass all: %+v", st)
	}
	if !strings.Contains(lg.String(), "filter voided") {
		t.Fatalf("void not logged:\n%s", lg.String())
	}
}

// 超时后当前批放行；晚到的过滤器只作用于其后的批。
func TestTimeoutThenLateFilter(t *testing.T) {
	c, lg := newTestCoordinator(t, JoinInner, 1, 100, 20*time.Millisecond)
	sc := c.NewScanner("probe", keyOfRow)

	start := time.Now()
	out, st := sc.FilterBatch(batchOf(1, 99), time.Time{})
	if len(out) != 2 || st.Passed != 2 {
		t.Fatalf("timed-out batch must pass all: %+v", st)
	}
	if d := time.Since(start); d < 15*time.Millisecond {
		t.Fatalf("did not wait, d=%v", d)
	}

	if err := c.Report(ShardReport[int64]{Shard: 0, Min: 1, Max: 5, Distinct: setOf(1, 5)}); err != nil {
		t.Fatal(err)
	}
	out, _ = sc.FilterBatch(batchOf(5, 99), time.Time{})
	if got := keysOf(t, out); fmt.Sprint(got) != "[5]" {
		t.Fatalf("late filter must apply to later batches, got %v", got)
	}
	if !strings.Contains(lg.String(), "not-ready-or-timeout") {
		t.Fatalf("timeout reason not logged:\n%s", lg.String())
	}
}

// 未知连接类型、编号越界、重复报告：整体拒绝且原因可区分，状态不变。
func TestReportRejections(t *testing.T) {
	if _, err := NewCoordinator[int64](Config[int64]{JoinType: JoinUnknown, ShardCount: 1}); err != ErrUnknownJoin {
		t.Fatalf("want ErrUnknownJoin, got %v", err)
	}

	c, _ := newTestCoordinator(t, JoinInner, 2, 100, time.Millisecond)
	if err := c.Report(ShardReport[int64]{Shard: 5}); err != ErrShardOutOfRange {
		t.Fatalf("want ErrShardOutOfRange, got %v", err)
	}
	if err := c.Report(ShardReport[int64]{Shard: -1}); err != ErrShardOutOfRange {
		t.Fatalf("want ErrShardOutOfRange, got %v", err)
	}
	if err := c.Report(ShardReport[int64]{Shard: 0, Min: 1, Max: 1, Distinct: setOf(1)}); err != nil {
		t.Fatal(err)
	}
	// 重复报告（且内容为放弃）必须被拒绝，且不改变状态。
	if err := c.Report(ShardReport[int64]{Shard: 0, Abandon: true}); err != ErrDuplicateShard {
		t.Fatalf("want ErrDuplicateShard, got %v", err)
	}
	c.mu.Lock()
	if c.abandoned || c.received != 1 {
		t.Fatalf("rejected report changed state: abandoned=%v received=%d", c.abandoned, c.received)
	}
	c.mu.Unlock()
	if err := c.Report(ShardReport[int64]{Shard: 1, Min: 2, Max: 2, Distinct: setOf(2)}); err != nil {
		t.Fatal(err)
	}
	sc := c.NewScanner("probe", keyOfRow)
	out, _ := sc.FilterBatch(batchOf(2, 9), time.Time{})
	if got := keysOf(t, out); fmt.Sprint(got) != "[2]" {
		t.Fatalf("rejected abandon must not affect filter, got %v", got)
	}
}

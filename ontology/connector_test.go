package ontology

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"math"
	"reflect"
	"strings"
	"sync"
	"testing"
)

// quietConnector 构造日志写入 io.Discard 的连接器，避免测试输出被日志淹没；
// 日志内容由 TestLogsContain... 专门断言。
func quietConnector(t *testing.T, cfg Config) *Connector {
	t.Helper()
	c, err := NewConnector(cfg, WithLogWriter(io.Discard))
	if err != nil {
		t.Fatalf("unexpected constructor error: %v", err)
	}
	return c
}

// ev 构造测试用期望事件。
func ev(seq int64, side Side, key string, t int64) Event {
	return Event{Seq: seq, Side: side, Key: key, Time: t}
}

// retainedSeqs 返回快照中某侧保留事件的编号集合，便于精确断言。
func retainedSeqs(s Snapshot, side Side) map[int64]bool {
	es := s.RetainedLeft
	if side == SideRight {
		es = s.RetainedRight
	}
	m := make(map[int64]bool, len(es))
	for _, e := range es {
		m[e.Seq] = true
	}
	return m
}

func TestNewConnectorInvalidConfig(t *testing.T) {
	cases := []struct {
		name string
		cfg  Config
	}{
		{"lower greater than upper", Config{LowerBound: 1, UpperBound: 0, MaxRetained: 10}},
		{"zero limit", Config{LowerBound: 0, UpperBound: 0, MaxRetained: 0}},
		{"negative limit", Config{LowerBound: -2, UpperBound: 2, MaxRetained: -1}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, err := NewConnector(tc.cfg)
			if !errors.Is(err, ErrInvalidArgument) {
				t.Fatalf("want ErrInvalidArgument, got %v", err)
			}
			if c != nil {
				t.Fatalf("connector must be nil on error, got %+v", c)
			}
		})
	}
}

func TestClosedIntervalBoundaries(t *testing.T) {
	// 条件：L <= R <= L+10，两端闭合。先放入左事件 L@100。
	c := quietConnector(t, Config{LowerBound: 0, UpperBound: 10, MaxRetained: 100})
	if _, err := c.ProcessLeft("k", 100); err != nil {
		t.Fatalf("seed L@100: %v", err)
	}

	// 逐对验证 R=99..111 的匹配结果：仅 [100,110] 闭合区间内命中。
	for _, r := range []int64{99, 100, 105, 110, 111} {
		pairs, perr := c.ProcessRight("k", r)
		if perr != nil {
			t.Fatalf("R(%d) unexpected error: %v", r, perr)
		}
		// 右事件时间必须非递减；每处理一条右事件都与 L@100 判定一次。
		want := r >= 100 && r <= 110
		if got := len(pairs) == 1; got != want {
			t.Fatalf("R=%d: matched=%v, want %v (closed interval [L+0, L+10])", r, got, want)
		}
		// 每条右事件判定后会把更早、不再可能匹配的右事件清掉，但 L@100 在
		// R<=110 期间必须始终保留（闭合边界 110 仍可命中）。
	}

	// 再用一个新键验证下界闭合：lower=-5, upper=5，L@1000 与 R@995 恰好命中。
	c2 := quietConnector(t, Config{LowerBound: -5, UpperBound: 5, MaxRetained: 100})
	if _, err := c2.ProcessLeft("b", 1000); err != nil {
		t.Fatal(err)
	}
	pairs, err := c2.ProcessRight("b", 995) // 1000-5 == 995，下界闭合命中
	if err != nil {
		t.Fatal(err)
	}
	if len(pairs) != 1 {
		t.Fatalf("lower boundary: want 1 pair, got %d", len(pairs))
	}
	if got := pairs[0].RightTime - pairs[0].LeftTime; got != -5 {
		t.Fatalf("want delta -5 at closed lower bound, got %d", got)
	}
}

func TestStepByStepCleanupPrecision(t *testing.T) {
	// lower=0, upper=10：L <= R <= L+10。
	c := quietConnector(t, Config{LowerBound: 0, UpperBound: 10, MaxRetained: 100})
	step := func(side Side, key string, at int64, wantPairs int) {
		t.Helper()
		var (
			pairs []Pair
			err   error
		)
		if side == SideLeft {
			pairs, err = c.ProcessLeft(key, at)
		} else {
			pairs, err = c.ProcessRight(key, at)
		}
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(pairs) != wantPairs {
			t.Fatalf("side %s t=%d: want %d new pairs, got %d", side, at, wantPairs, len(pairs))
		}
	}

	step(SideLeft, "k", 100, 0)
	s := c.Snapshot()
	if s.LeftWatermark != 100 || s.RightWatermark != math.MinInt64 {
		t.Fatalf("watermarks wrong: %+v", s)
	}
	if !reflect.DeepEqual(retainedSeqs(s, SideLeft), map[int64]bool{1: true}) {
		t.Fatalf("step1 retained left wrong: %v", retainedSeqs(s, SideLeft))
	}

	step(SideRight, "k", 105, 1) // 区间内
	s = c.Snapshot()
	if s.RightWatermark != 105 {
		t.Fatalf("right watermark = %d, want 105", s.RightWatermark)
	}
	if !reflect.DeepEqual(retainedSeqs(s, SideLeft), map[int64]bool{1: true}) ||
		!reflect.DeepEqual(retainedSeqs(s, SideRight), map[int64]bool{2: true}) {
		t.Fatalf("step2 retained wrong: L=%v R=%v",
			retainedSeqs(s, SideLeft), retainedSeqs(s, SideRight))
	}

	step(SideRight, "k", 110, 1) // 恰好上界闭合：L 仍须保留
	s = c.Snapshot()
	if !reflect.DeepEqual(retainedSeqs(s, SideLeft), map[int64]bool{1: true}) {
		t.Fatalf("step3: L@100 must survive at wmR=110 (closed upper bound), got %v",
			retainedSeqs(s, SideLeft))
	}
	if !reflect.DeepEqual(retainedSeqs(s, SideRight), map[int64]bool{2: true, 3: true}) {
		t.Fatalf("step3 retained right wrong: %v", retainedSeqs(s, SideRight))
	}

	step(SideRight, "k", 111, 0) // 超出上界 1ms：L@100 必须被精确清掉
	s = c.Snapshot()
	if len(s.RetainedLeft) != 0 {
		t.Fatalf("step4: L@100 must be cleaned at wmR=111, got %v",
			retainedSeqs(s, SideLeft))
	}
	if !reflect.DeepEqual(retainedSeqs(s, SideRight), map[int64]bool{2: true, 3: true, 4: true}) {
		t.Fatalf("step4 retained right wrong: %v", retainedSeqs(s, SideRight))
	}

	step(SideLeft, "k", 120, 0) // wmL=120：三个右事件全部越过下界，须精确清空
	s = c.Snapshot()
	if len(s.RetainedRight) != 0 {
		t.Fatalf("step5: all right events must be cleaned, got %v",
			retainedSeqs(s, SideRight))
	}
	if !reflect.DeepEqual(retainedSeqs(s, SideLeft), map[int64]bool{5: true}) {
		t.Fatalf("step5 retained left wrong: %v", retainedSeqs(s, SideLeft))
	}
	if s.Seq != 5 {
		t.Fatalf("seq = %d, want 5", s.Seq)
	}

	// 全程仅产生 (L1,R2)、(L1,R3) 两个配对。
	wantPairs := []Pair{
		{Key: "k", Left: ev(1, SideLeft, "k", 100), Right: ev(2, SideRight, "k", 105),
			LeftTime: 100, RightTime: 105},
		{Key: "k", Left: ev(1, SideLeft, "k", 100), Right: ev(3, SideRight, "k", 110),
			LeftTime: 100, RightTime: 110},
	}
	if got := c.Pairs(); !reflect.DeepEqual(got, wantPairs) {
		t.Fatalf("emitted pairs mismatch:\n got %+v\nwant %+v", got, wantPairs)
	}
}

func TestLowerBoundCleanupIsStrict(t *testing.T) {
	// lower=2, upper=10：L+2 <= R。
	c := quietConnector(t, Config{LowerBound: 2, UpperBound: 10, MaxRetained: 100})
	if _, err := c.ProcessRight("k", 110); err != nil {
		t.Fatal(err)
	}
	if pairs, err := c.ProcessLeft("k", 100); err != nil || len(pairs) != 1 {
		t.Fatalf("L@100 vs R@110: pairs=%d err=%v, want 1 pair", len(pairs), err)
	}
	// wmL 推进到 109：R@110 相对新下界 109+2=111 差 1ms，必须被清（严格不等）。
	if pairs, err := c.ProcessLeft("k", 109); err != nil || len(pairs) != 0 {
		t.Fatalf("L@109 vs R@110: pairs=%d err=%v, want 0 pair", len(pairs), err)
	}
	s := c.Snapshot()
	if len(s.RetainedRight) != 0 {
		t.Fatalf("R@110 must be cleaned when wmL+2=111 > 110, got %v",
			retainedSeqs(s, SideRight))
	}
}

func TestDifferentKeysDoNotJoin(t *testing.T) {
	c := quietConnector(t, Config{LowerBound: 0, UpperBound: 100, MaxRetained: 100})
	_, _ = c.ProcessLeft("a", 10)
	pairs, err := c.ProcessRight("b", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(pairs) != 0 || len(c.Pairs()) != 0 {
		t.Fatalf("events with different keys must not pair, got %+v", pairs)
	}
}

func TestRejectionsDoNotMutateState(t *testing.T) {
	// 容量 2，lower=upper=0：仅同时间戳配对，任何保留事件在对侧无水位线时都不会被清。
	c := quietConnector(t, Config{LowerBound: 0, UpperBound: 0, MaxRetained: 2})
	if _, err := c.ProcessLeft("k1", 1); err != nil {
		t.Fatal(err)
	}
	if _, err := c.ProcessLeft("k2", 2); err != nil {
		t.Fatal(err)
	}
	before := c.Snapshot()

	// 容量超限：第三事件清理后仍需保留 3 个 -> 拒绝。
	_, err := c.ProcessLeft("k3", 3)
	if !errors.Is(err, ErrRetentionExceeded) {
		t.Fatalf("want ErrRetentionExceeded, got %v", err)
	}
	if got := c.Snapshot(); !reflect.DeepEqual(got, before) {
		t.Fatalf("retention rejection mutated state:\nbefore %+v\nafter  %+v", before, got)
	}

	// 单侧时间倒退 -> 拒绝（水位线为 2，1 < 2）。
	before = c.Snapshot()
	_, err = c.ProcessLeft("k1", 1)
	if !errors.Is(err, ErrTimeRegressed) {
		t.Fatalf("want ErrTimeRegressed, got %v", err)
	}
	if got := c.Snapshot(); !reflect.DeepEqual(got, before) {
		t.Fatalf("regression rejection mutated state:\nbefore %+v\nafter  %+v", before, got)
	}

	// 右流独立水位线倒退 -> 拒绝。
	if _, err := c.ProcessRight("k1", 5); err != nil {
		t.Fatal(err)
	}
	before = c.Snapshot()
	_, err = c.ProcessRight("k1", math.MinInt64)
	if !errors.Is(err, ErrTimeRegressed) {
		t.Fatalf("want right-side ErrTimeRegressed, got %v", err)
	}
	if got := c.Snapshot(); !reflect.DeepEqual(got, before) {
		t.Fatalf("right regression mutated state:\nbefore %+v\nafter  %+v", before, got)
	}

	// 空键 -> 拒绝（即使时间非法也优先报空键，但这里时间合法以隔离原因）。
	_, err = c.ProcessRight("", 5)
	if !errors.Is(err, ErrEmptyKey) {
		t.Fatalf("want ErrEmptyKey, got %v", err)
	}
	if got := c.Snapshot(); !reflect.DeepEqual(got, before) {
		t.Fatalf("empty-key rejection mutated state:\nbefore %+v\nafter  %+v", before, got)
	}
}

func TestRetentionLimitAcrossBothSides(t *testing.T) {
	// 容量按两侧保留总数计。
	c := quietConnector(t, Config{LowerBound: 0, UpperBound: 0, MaxRetained: 2})
	if _, err := c.ProcessLeft("a", 1); err != nil {
		t.Fatal(err)
	}
	if _, err := c.ProcessRight("b", 1); err != nil { // 不同键不配对；两侧各 1 个，恰好 2
		t.Fatal(err)
	}
	_, err := c.ProcessLeft("c", 1) // 总数将达 3，且无任何可清理项 -> 拒绝
	if !errors.Is(err, ErrRetentionExceeded) {
		t.Fatalf("want ErrRetentionExceeded, got %v", err)
	}
}

func TestDeterministicReplay(t *testing.T) {
	script := []struct {
		side Side
		key  string
		time int64
	}{
		{SideLeft, "a", 100},  // 左水位线 100
		{SideRight, "a", 105}, // 命中 L a@100
		{SideLeft, "b", 200},  // 左水位线 200
		{SideRight, "a", 110}, // 上界闭合：100+10==110，命中 L a@100
		{SideRight, "b", 200}, // 下界闭合：200-5<=200，命中 L b@200
		{SideLeft, "a", 220},  // 左水位线 220；与更老的 R a@105/110 不再成配
		{SideRight, "a", 220}, // 命中 L a@220
		{SideLeft, "b", 230},  // 左水位线 230；R b@200 已越下界，不成配
	}
	run := func() struct {
		pairs [][]Pair
		snaps []Snapshot
		final Snapshot
	} {
		c := quietConnector(t, Config{LowerBound: -5, UpperBound: 10, MaxRetained: 50})
		out := struct {
			pairs [][]Pair
			snaps []Snapshot
			final Snapshot
		}{}
		for _, step := range script {
			var (
				p   []Pair
				err error
			)
			if step.side == SideLeft {
				p, err = c.ProcessLeft(step.key, step.time)
			} else {
				p, err = c.ProcessRight(step.key, step.time)
			}
			if err != nil {
				t.Fatalf("step %+v: %v", step, err)
			}
			out.pairs = append(out.pairs, append([]Pair(nil), p...))
			out.snaps = append(out.snaps, c.Snapshot())
		}
		out.final = c.Snapshot()
		return out
	}

	first := run()
	second := run()
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("replaying the same input sequence produced different results")
	}
}

func TestConcurrentReadersSeeFieldwiseConsistentPairs(t *testing.T) {
	c := quietConnector(t, Config{LowerBound: 0, UpperBound: 1_000_000, MaxRetained: 1_000_000})

	// 预填充使两侧都有保留事件。
	for i := int64(0); i < 200; i++ {
		if _, err := c.ProcessLeft(fmt.Sprintf("k%d", i%7), i*10); err != nil {
			t.Fatal(err)
		}
	}

	const readers = 8
	var wg sync.WaitGroup
	type sample struct {
		snap  Snapshot
		pairs []Pair // 同一时刻 Pairs() 的独立副本
	}
	results := make(chan []sample, readers)

	stop := make(chan struct{})
	for r := 0; r < readers; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			// 本地采样，绝不在持锁/向有界通道发送的状态下等待 stop，
			// 避免“读者把通道写满后再也收不到停止信号”的死锁。
			var local []sample
			iter := 0
			for {
				select {
				case <-stop:
					results <- local
					return
				default:
				}
				snap := c.Snapshot()
				now := c.Pairs()
				// 前 32 个时刻全量保留，之后每 512 轮滚动覆盖末槽。
				if len(local) < 32 {
					local = append(local, sample{snap: snap, pairs: now})
				} else if iter%512 == 0 {
					local[31] = sample{snap: snap, pairs: now}
				}
				iter++
			}
		}()
	} // 写者继续推进；读者拿到的配对集合必须始终是最终配对集合的某个已提交前缀。
	for i := int64(0); i < 200; i++ {
		if _, err := c.ProcessRight(fmt.Sprintf("k%d", i%7), i*10); err != nil {
			t.Fatal(err)
		}
	}
	close(stop)
	wg.Wait()
	close(results)

	finalPairs := c.Pairs()
	sawDistinctPrefixes := map[int]bool{}
	for samples := range results {
		for _, sm := range samples {
			snap := sm.snap
			n := len(snap.Pairs)
			sawDistinctPrefixes[n] = true
			if n > len(finalPairs) {
				t.Fatalf("snapshot has %d pairs, final has %d", n, len(finalPairs))
			}
			// 逐字段一致：每个历史快照的配对必须与最终结果的同序前缀完全相等。
			// n==0 时快照可能是 nil 切片，跳过 nil/空切片的表示差异。
			if n > 0 && !reflect.DeepEqual(snap.Pairs, finalPairs[:n]) {
				t.Fatalf("snapshot pairs diverge from committed prefix at len %d", n)
			}
			// Snapshot() 与紧随其后的 Pairs() 之间可能有新提交，
			// 只要求 Pairs() 包含快照所见到的完整前缀。
			if n > 0 && (len(sm.pairs) < n || !reflect.DeepEqual(sm.pairs[:n], snap.Pairs)) {
				t.Fatalf("Pairs() and Snapshot().Pairs disagree at len %d", n)
			}
		}
	}
	if len(sawDistinctPrefixes) < 2 {
		t.Fatalf("expected readers to observe multiple commit prefixes, saw %v", sawDistinctPrefixes)
	}
}

func TestOverflowSafeExtremeTimes(t *testing.T) {
	// MaxInt64 时间 + 正偏移不得溢出 panic，且闭合判定正确。
	c := quietConnector(t, Config{LowerBound: 0, UpperBound: 5, MaxRetained: 100})
	if _, err := c.ProcessLeft("a", math.MaxInt64); err != nil {
		t.Fatal(err)
	}
	pairs, err := c.ProcessRight("a", math.MaxInt64)
	if err != nil {
		t.Fatalf("MaxInt64 match failed: %v", err)
	}
	if len(pairs) != 1 {
		t.Fatalf("MaxInt64 closed-boundary match: want 1 pair, got %d", len(pairs))
	}
	s := c.Snapshot()
	if len(s.RetainedLeft) != 1 || len(s.RetainedRight) != 1 {
		t.Fatalf("extreme-time events must be retained, got L=%d R=%d",
			len(s.RetainedLeft), len(s.RetainedRight))
	}

	// MinInt64 时间 + 负偏移同样不得溢出。
	c2 := quietConnector(t, Config{LowerBound: -5, UpperBound: 0, MaxRetained: 100})
	if _, err := c2.ProcessRight("b", math.MinInt64); err != nil {
		t.Fatal(err)
	}
	pairs, err = c2.ProcessLeft("b", math.MinInt64)
	if err != nil {
		t.Fatalf("MinInt64 match failed: %v", err)
	}
	if len(pairs) != 1 {
		t.Fatalf("MinInt64 closed-boundary match: want 1 pair, got %d", len(pairs))
	}
}

func TestLogsContainInputsDecisionsPairsCleanupsAndRejections(t *testing.T) {
	var buf bytes.Buffer
	c, _ := NewConnector(Config{LowerBound: 0, UpperBound: 10, MaxRetained: 100},
		WithLogWriter(&buf))
	_, _ = c.ProcessLeft("k", 100)
	_, _ = c.ProcessRight("k", 110) // 上界闭合命中
	_, _ = c.ProcessRight("k", 111) // 未命中，且触发 L@100 清理
	_, _ = c.ProcessLeft("k", 0)    // 时间倒退被拒

	log := buf.String()
	for _, want := range []string{
		`msg="input accepted"`,           // 输入
		`msg="match check"`,              // 判定依据
		"matched=true",                   // 闭合边界命中
		"lower_closed_ok=true",           // 下界判定字段
		"upper_closed_ok=true",           // 上界判定字段
		"matched=false",                  // 越界未命中
		`msg="pair emitted"`,             // 输出配对
		`msg="event cleaned"`,            // 清理依据
		`msg="input rejected"`,           // 拒绝
		"ontology: event time regressed", // 可区分原因
	} {
		if !strings.Contains(log, want) {
			t.Errorf("log missing %q\n--- log ---\n%s", want, log)
		}
	}
}

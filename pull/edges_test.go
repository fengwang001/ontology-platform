package pull_test

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	"ontology/pull"
)

// hz<0：不查询、成功空结果、推进 maxNow。
func TestPullNegativeHorizon(t *testing.T) {
	src := &memSource{}
	p, _ := pull.New(src, 10, 0, 2, 1)
	src.reset()
	r, err := p.Pull(5)
	if err != nil {
		t.Fatal(err)
	}
	if r != (pull.Result{}) || src.calls != 0 || p.MaxNow() != 5 {
		t.Fatalf("r=%+v calls=%d maxNow=%d", r, src.calls, p.MaxNow())
	}
}

// 页边界落在同 ts 多行中间：复合游标不漏不重（5 行、limit=2 → 3 页）。
func TestSameTsPageBoundary(t *testing.T) {
	var rows []srcRow
	for i := int64(1); i <= 5; i++ {
		rows = append(rows, srcRow{i, 77, 1, 1, 77})
	}
	src := &memSource{rows: rows}
	p, _ := pull.New(src, 0, 0, 2, 1)
	src.reset()
	r, err := p.Pull(77)
	if err != nil {
		t.Fatal(err)
	}
	if r.Applied != 5 || r.Dup != 0 || r.Queries != 3 {
		t.Fatalf("r=%+v", r)
	}
	if ts, id := p.Cur(); ts != 77 || id != 5 {
		t.Fatalf("cur=(%d,%d)", ts, id)
	}
}

// n 恰为 limit 倍数时必须多读一次空页。
func TestExactMultipleRequiresEmptyPage(t *testing.T) {
	var rows []srcRow
	for i := int64(1); i <= 4; i++ {
		rows = append(rows, srcRow{i, 10, 1, 1, 10})
	}
	src := &memSource{rows: rows}
	p, _ := pull.New(src, 0, 0, 2, 1)
	src.reset()
	r, _ := p.Pull(10)
	if r.Applied != 4 || r.Queries != 3 {
		t.Fatalf("r=%+v, 4=2*2 时应为 3 次 Query", r)
	}
}

// cur.ts<B 起点钳 0；起点 ts 恰等 cur.ts-B 的行（含同 ts 全部行）被重读。
func TestLookbackClampAndBoundary(t *testing.T) {
	src := &memSource{rows: []srcRow{
		{1, 3, 1, 1, 3},
		{2, 8, 1, 1, 8},
	}}
	p, _ := pull.New(src, 0, 5, 10, 1)
	src.reset()
	if r, err := p.Pull(8); err != nil || r.Applied != 2 {
		t.Fatalf("first: r=%+v err=%v", r, err)
	}
	src.reset()
	r, err := p.Pull(9) // 起点 (3,0)，ts=3 的 id=1 在边界上必须重读
	if err != nil {
		t.Fatal(err)
	}
	if r.Dup != 2 || r.Applied != 0 {
		t.Fatalf("r=%+v, 边界行 (ts=3) 必须重读", r)
	}

	// cur.ts<B 时钳制：cur=(8,2)，B=100，起点 ts 钳为 0。
	src.reset()
	r2, _ := p.Pull(10)
	if r2.Queries != 1 || r2.Dup != 2 {
		t.Fatalf("r2=%+v", r2)
	}
}

// hz 恰等行 ts：ts<=maxTs 含等号，行应被读到。
func TestHorizonEqualsTs(t *testing.T) {
	src := &memSource{rows: []srcRow{{7, 40, 1, 1, 40}}}
	p, _ := pull.New(src, 10, 0, 10, 1)
	src.reset()
	r, err := p.Pull(50) // hz=40
	if err != nil {
		t.Fatal(err)
	}
	if r.Applied != 1 {
		t.Fatalf("r=%+v", r)
	}
}

// 时钟回退与参数非法均拒绝，且不改游标/maxNow。
func TestRejectionsDoNotChangeState(t *testing.T) {
	src := &memSource{rows: []srcRow{{1, 10, 1, 1, 10}}}
	p, _ := pull.New(src, 0, 0, 2, 1)
	src.reset()
	if _, err := p.Pull(20); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Pull(19); !errors.Is(err, pull.ErrClockRollback) {
		t.Fatalf("回退 err=%v", err)
	}
	if p.MaxNow() != 20 {
		t.Fatalf("回退后 maxNow=%d 应仍为 20", p.MaxNow())
	}
	if _, err := p.Pull(-1); !errors.Is(err, pull.ErrInvalidParam) {
		t.Fatalf("now 越界 err=%v", err)
	}
	if _, err := p.Pull(1_000_000_000_001); !errors.Is(err, pull.ErrInvalidParam) {
		t.Fatalf("now 上界 err=%v", err)
	}

	// SetSchema：权限不足、降级、越界。
	if err := p.SetSchema(1, 2); !errors.Is(err, pull.ErrForbidden) {
		t.Fatalf("role 不足 err=%v", err)
	}
	if err := p.SetSchema(2, 0); !errors.Is(err, pull.ErrInvalidParam) {
		t.Fatalf("S 越界 err=%v", err)
	}
	if err := p.SetSchema(2, 101); !errors.Is(err, pull.ErrInvalidParam) {
		t.Fatalf("S 上界 err=%v", err)
	}
	if err := p.SetSchema(2, 2); err != nil {
		t.Fatalf("合法升级失败: %v", err)
	}
	if err := p.SetSchema(2, 1); !errors.Is(err, pull.ErrDowngrade) {
		t.Fatalf("升级后降级 err=%v", err)
	}
	if p.Schema() != 2 {
		t.Fatalf("S=%d want 2", p.Schema())
	}

	// 构造参数非法。
	for _, c := range [][5]int64{
		{-1, 0, 1, 1, 0},
		{0, -1, 1, 1, 0},
		{0, 0, 0, 1, 0},
		{0, 0, 1, 0, 0},
		{1_000_000_001, 0, 1, 1, 0},
		{0, 0, 1, 101, 0},
	} {
		if _, err := pull.New(&memSource{}, c[0], c[1], c[2], c[3]); !errors.Is(err, pull.ErrInvalidParam) {
			t.Fatalf("New(%v) err=%v", c, err)
		}
	}
}

// badSource 返回各种违反源端契约的页，拉取器必须按参数非法拒绝且不改状态。
type badSource struct{ rows []pull.Row }

func (b *badSource) Query(_, _ int64, _ int64, _, _ int64) ([]pull.Row, error) {
	return b.rows, nil
}

func TestBadSourceRowsRejected(t *testing.T) {
	cases := map[string][]pull.Row{
		"id 越界":       {{ID: 0, Ts: 1, Ver: 1, Sv: 1}},
		"id 超上界":      {{ID: 1_000_000_001, Ts: 1, Ver: 1, Sv: 1}},
		"ts 负":        {{ID: 1, Ts: -1, Ver: 1, Sv: 1}},
		"ver 越界":      {{ID: 1, Ts: 1, Ver: 0, Sv: 1}},
		"sv 越界":       {{ID: 1, Ts: 1, Ver: 1, Sv: 101}},
		"ts 超 maxTs":  {{ID: 1, Ts: 11, Ver: 1, Sv: 1}},
		"首行不大于 after": {{ID: 0, Ts: 0, Ver: 1, Sv: 1}}, // 起点 (0,0)，此行等于 after
		"页内非严格升序": {
			{ID: 1, Ts: 5, Ver: 1, Sv: 1},
			{ID: 1, Ts: 5, Ver: 1, Sv: 1},
		},
		"页内逆序": {
			{ID: 2, Ts: 5, Ver: 1, Sv: 1},
			{ID: 1, Ts: 5, Ver: 1, Sv: 1},
		},
	}
	for name, rows := range cases {
		t.Run(name, func(t *testing.T) {
			p, err := pull.New(&badSource{rows: rows}, 0, 0, 10, 1)
			if err != nil {
				t.Fatal(err)
			}
			if _, perr := p.Pull(10); !errors.Is(perr, pull.ErrInvalidParam) {
				t.Fatalf("err=%v want ErrInvalidParam", perr)
			}
			if ts, id := p.Cur(); ts != 0 || id != 0 {
				t.Fatalf("拒绝后游标=(%d,%d)", ts, id)
			}
			if p.MaxNow() != 0 || p.DLQSize() != 0 {
				t.Fatalf("拒绝后 maxNow=%d dlq=%d", p.MaxNow(), p.DLQSize())
			}
		})
	}
}

// 页行数超过 limit 也是参数非法。
type oversizedSource struct{}

func (oversizedSource) Query(_, _ int64, limit int64, _, _ int64) ([]pull.Row, error) {
	out := make([]pull.Row, limit+1)
	for i := range out {
		out[i] = pull.Row{ID: int64(i) + 1, Ts: 1, Ver: 1, Sv: 1}
	}
	return out, nil
}

func TestOversizedPageRejected(t *testing.T) {
	p, _ := pull.New(oversizedSource{}, 0, 0, 2, 1)
	if _, err := p.Pull(1); !errors.Is(err, pull.ErrInvalidParam) {
		t.Fatalf("err=%v", err)
	}
}

// 并发冒烟：多个 goroutine 并发 Pull（now 乱序执行时回退被合法拒绝），
// 不发生数据竞争；收尾时以大于全部 ts 的 now 串行拉一轮，全部行恰好应用一次。
func TestConcurrentPulls(t *testing.T) {
	var rows []srcRow
	for i := int64(1); i <= 20; i++ {
		rows = append(rows, srcRow{i, i, 1, 1, i})
	}
	src := &memSource{rows: rows}
	p, _ := pull.New(src, 0, 0, 3, 1)
	var clock int64
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for k := 1; k <= 20; k++ {
				now := atomic.AddInt64(&clock, 1) // 1..160 严格递增
				src.reset()
				if _, err := p.Pull(now); err != nil && !errors.Is(err, pull.ErrClockRollback) {
					t.Errorf("Pull(%d): %v", now, err)
					return
				}
			}
		}()
	}
	wg.Wait()
	src.reset()
	if _, err := p.Pull(161); err != nil {
		t.Fatalf("收尾 Pull: %v", err)
	}
	ts, id := p.Cur()
	if ts != 20 || id != 20 {
		t.Fatalf("最终游标=(%d,%d) want (20,20)", ts, id)
	}
	for i := int64(1); i <= 20; i++ {
		if p.Applied(i) != 1 {
			t.Fatalf("applied[%d]=%d want 1", i, p.Applied(i))
		}
	}
	if p.DLQSize() != 0 {
		t.Fatal("最终不应有死信")
	}
}

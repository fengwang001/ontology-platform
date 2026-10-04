package pull_test

import (
	"errors"
	"fmt"
	"sort"
	"sync"
	"sync/atomic"
	"testing"

	"ontology/cursor"
	"ontology/pull"
	"ontology/sink"
)

// srcRow 是源端的一行（含提交可见时刻 commitAt）。
type srcRow struct {
	id, ts, ver, commitAt, sv int64
}

type queryCall struct {
	after            cursor.Cursor
	limit            int
	maxTS, visibleAt int64
}

// fakeSource 按源端语义从行集合中应答 Query；errAt 以 1 起始的调用序号为键注入错误。
type fakeSource struct {
	rows  []srcRow
	errAt map[int]bool
	calls []queryCall
}

func (f *fakeSource) Query(after cursor.Cursor, limit int, maxTS, visibleAt int64) ([]sink.Row, error) {
	f.calls = append(f.calls, queryCall{after: after, limit: limit, maxTS: maxTS, visibleAt: visibleAt})
	if f.errAt[len(f.calls)] {
		return nil, fmt.Errorf("注入错误（第 %d 次调用）", len(f.calls))
	}
	best := map[int64]sink.Row{}
	for _, r := range f.rows {
		if r.commitAt > visibleAt || r.ts > maxTS {
			continue
		}
		if r.ts < after.TS || (r.ts == after.TS && r.id <= after.ID) {
			continue
		}
		if cur, ok := best[r.id]; !ok || r.ver > cur.Ver {
			best[r.id] = sink.Row{ID: r.id, TS: r.ts, Ver: r.ver, SV: r.sv}
		}
	}
	out := make([]sink.Row, 0, len(best))
	for _, r := range best {
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].TS != out[j].TS {
			return out[i].TS < out[j].TS
		}
		return out[i].ID < out[j].ID
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// staticSource 依序返回预设页，用于构造非法源端数据。
type staticSource struct {
	pages [][]sink.Row
	calls int
}

func (s *staticSource) Query(after cursor.Cursor, limit int, maxTS, visibleAt int64) ([]sink.Row, error) {
	if s.calls >= len(s.pages) {
		return nil, nil
	}
	p := s.pages[s.calls]
	s.calls++
	return p, nil
}

func mustPuller(t *testing.T, src pull.Source, D, B, limit, S int64) *pull.Puller {
	t.Helper()
	p, err := pull.New(src, D, B, limit, S)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return p
}

func mustPull(t *testing.T, p *pull.Puller, now int64) pull.Stats {
	t.Helper()
	st, err := p.Pull(now)
	if err != nil {
		t.Fatalf("Pull(%d): %v", now, err)
	}
	return st
}

// 例二：页边界落在同 ts 多行中间；B=0 时同 ts 行被重读为 Dup，不漏不重。
func TestPageBoundarySameTS(t *testing.T) {
	src := &fakeSource{rows: []srcRow{
		{1, 50, 1, 50, 1},
		{2, 50, 1, 50, 1},
		{3, 50, 1, 50, 1},
	}}
	p := mustPuller(t, src, 0, 0, 2, 1)

	st := mustPull(t, p, 50)
	if want := (pull.Stats{Applied: 3, Queries: 2}); st != want {
		t.Fatalf("Pull(50) = %+v, want %+v", st, want)
	}
	if cur := p.Snapshot().Cur; cur != (cursor.Cursor{TS: 50, ID: 3}) {
		t.Fatalf("cur = %+v, want (50,3)", cur)
	}

	st = mustPull(t, p, 51)
	if want := (pull.Stats{Dup: 3, Queries: 2}); st != want {
		t.Fatalf("Pull(51) = %+v, want %+v", st, want)
	}
	if cur := p.Snapshot().Cur; cur != (cursor.Cursor{TS: 50, ID: 3}) {
		t.Fatalf("cur 回退或漂移: %+v", cur)
	}
}

// 例一：晚提交行落在回看窗口内被补回。
func TestLateCommitCaughtInWindow(t *testing.T) {
	src := &fakeSource{rows: []srcRow{
		{1, 100, 1, 100, 1},
		{2, 100, 1, 100, 1},
		{3, 100, 1, 100, 1},
		{4, 103, 1, 112, 1},
		{5, 102, 1, 125, 1},
	}}
	p := mustPuller(t, src, 10, 5, 2, 1)

	if st, want := mustPull(t, p, 111), (pull.Stats{Applied: 3, Queries: 2}); st != want {
		t.Fatalf("Pull(111) = %+v, want %+v", st, want)
	}
	if cur := p.Snapshot().Cur; cur != (cursor.Cursor{TS: 100, ID: 3}) {
		t.Fatalf("cur = %+v, want (100,3)", cur)
	}
	if st, want := mustPull(t, p, 113), (pull.Stats{Applied: 1, Dup: 3, Queries: 3}); st != want {
		t.Fatalf("Pull(113) = %+v, want %+v", st, want)
	}
	if cur := p.Snapshot().Cur; cur != (cursor.Cursor{TS: 103, ID: 4}) {
		t.Fatalf("cur = %+v, want (103,4)", cur)
	}
	// r5(102) 比游标早但在窗口内，被补回；5 行 → 页 [2,2,1]，⌊5/2⌋+1=3 次查询。
	if st, want := mustPull(t, p, 130), (pull.Stats{Applied: 1, Dup: 4, Queries: 3}); st != want {
		t.Fatalf("Pull(130) = %+v, want %+v", st, want)
	}
	if got := p.Snapshot().Applied[5]; got != 1 {
		t.Fatalf("applied[5] = %d, want 1", got)
	}
}

// 例一变体：r5 的 ts=97 早于窗口起点 98，永久漏拉。
func TestLateCommitMissedOutsideWindow(t *testing.T) {
	src := &fakeSource{rows: []srcRow{
		{1, 100, 1, 100, 1},
		{2, 100, 1, 100, 1},
		{3, 100, 1, 100, 1},
		{4, 103, 1, 112, 1},
		{5, 97, 1, 125, 1},
	}}
	p := mustPuller(t, src, 10, 5, 2, 1)

	mustPull(t, p, 111)
	mustPull(t, p, 113)
	if st, want := mustPull(t, p, 130), (pull.Stats{Dup: 4, Queries: 3}); st != want {
		t.Fatalf("Pull(130) = %+v, want %+v", st, want)
	}
	if st, want := mustPull(t, p, 200), (pull.Stats{Dup: 4, Queries: 3}); st != want {
		t.Fatalf("Pull(200) = %+v, want %+v", st, want)
	}
	if got := p.Snapshot().Applied[5]; got != 0 {
		t.Fatalf("applied[5] = %d, want 0（永久漏拉）", got)
	}
}

// hz 恰等于行 ts：含边界。
func TestHZBoundaryInclusive(t *testing.T) {
	src := &fakeSource{rows: []srcRow{{1, 101, 1, 101, 1}}}
	p := mustPuller(t, src, 10, 0, 10, 1)
	if st, want := mustPull(t, p, 111), (pull.Stats{Applied: 1, Queries: 1}); st != want {
		t.Fatalf("Pull(111) = %+v, want %+v", st, want)
	}
}

// 起点 ts 恰等 cur.ts-B：含边界，窗口下沿的行被重读。
func TestLookbackStartBoundaryInclusive(t *testing.T) {
	src := &fakeSource{rows: []srcRow{
		{1, 100, 1, 100, 1},
		{2, 95, 1, 105, 1},
	}}
	p := mustPuller(t, src, 0, 5, 10, 1)
	if st, want := mustPull(t, p, 100), (pull.Stats{Applied: 1, Queries: 1}); st != want {
		t.Fatalf("Pull(100) = %+v, want %+v", st, want)
	}
	// 起点 (95,0)：b 的 ts=95 恰在边界上，必须被读到并应用。
	if st, want := mustPull(t, p, 105), (pull.Stats{Applied: 1, Dup: 1, Queries: 1}); st != want {
		t.Fatalf("Pull(105) = %+v, want %+v", st, want)
	}
	if got := p.Snapshot().Applied[2]; got != 1 {
		t.Fatalf("applied[2] = %d, want 1", got)
	}
}

// cur.ts 小于 B 时起点钳制为 (0,0)。
func TestLookbackStartClampToZero(t *testing.T) {
	src := &fakeSource{rows: []srcRow{{1, 50, 1, 50, 1}}}
	p := mustPuller(t, src, 0, 1000, 10, 1)
	mustPull(t, p, 50)
	mustPull(t, p, 60)
	if len(src.calls) != 2 {
		t.Fatalf("Query 次数 = %d, want 2", len(src.calls))
	}
	if got := src.calls[1].after; got != (cursor.Cursor{}) {
		t.Fatalf("第二次查询起点 = %+v, want (0,0)", got)
	}
}

// n 恰为 limit 倍数时多一次空页查询。
func TestEmptyPageExtraQuery(t *testing.T) {
	src := &fakeSource{rows: []srcRow{
		{1, 1, 1, 1, 1},
		{2, 2, 1, 2, 1},
		{3, 3, 1, 3, 1},
		{4, 4, 1, 4, 1},
	}}
	p := mustPuller(t, src, 0, 0, 2, 1)
	if st, want := mustPull(t, p, 10), (pull.Stats{Applied: 4, Queries: 3}); st != want {
		t.Fatalf("Pull(10) = %+v, want %+v", st, want)
	}
}

// hz 小于 0：不查询、返回空、推进最大 now。
func TestHZNegativeNoQuery(t *testing.T) {
	src := &fakeSource{rows: []srcRow{{1, 1, 1, 1, 1}}}
	p := mustPuller(t, src, 10, 0, 10, 1)
	st, err := p.Pull(5)
	if err != nil || st != (pull.Stats{}) {
		t.Fatalf("Pull(5) = %+v, %v; want 空统计", st, err)
	}
	if got := p.Snapshot().MaxNow; got != 5 {
		t.Fatalf("maxNow = %d, want 5", got)
	}
	if len(src.calls) != 0 {
		t.Fatalf("hz<0 时不应查询，实际 %d 次", len(src.calls))
	}
	if _, err := p.Pull(4); !errors.Is(err, pull.ErrClock) {
		t.Fatalf("Pull(4) err = %v, want ErrClock", err)
	}
}

// 例三：sv 超限进死信不重复入列；SetSchema 升级后落入窗口的行被补应用。
func TestDeadLetterAndUpgrade(t *testing.T) {
	src := &fakeSource{rows: []srcRow{{9, 200, 2, 200, 2}}}
	p := mustPuller(t, src, 0, 10, 10, 1)

	if st, want := mustPull(t, p, 200), (pull.Stats{Dead: 1, Queries: 1}); st != want {
		t.Fatalf("Pull(200) = %+v, want %+v", st, want)
	}
	snap := p.Snapshot()
	if snap.Dead[sink.Key{ID: 9, Ver: 2}] != (sink.Row{ID: 9, TS: 200, Ver: 2, SV: 2}) {
		t.Fatalf("死信 = %+v", snap.Dead)
	}
	if len(snap.Applied) != 0 {
		t.Fatalf("applied 应为空, got %+v", snap.Applied)
	}

	// 窗口内再次读到：已在死信，不重复计数。
	if st, want := mustPull(t, p, 201), (pull.Stats{Queries: 1}); st != want {
		t.Fatalf("Pull(201) = %+v, want %+v", st, want)
	}
	if got := len(p.Snapshot().Dead); got != 1 {
		t.Fatalf("死信数 = %d, want 1", got)
	}

	if err := p.SetSchema(2, 2); err != nil {
		t.Fatalf("SetSchema(2,2): %v", err)
	}
	// 升级后不重放死信，但行落在回看窗口内被重新读到并应用。
	if st, want := mustPull(t, p, 202), (pull.Stats{Applied: 1, Queries: 1}); st != want {
		t.Fatalf("Pull(202) = %+v, want %+v", st, want)
	}
	if got := p.Snapshot().Applied[9]; got != 2 {
		t.Fatalf("applied[9] = %d, want 2", got)
	}
}

// 例四：某页 Query 出错，整轮作废，游标/applied/死信/maxNow 零改动。
func TestErrSourceZeroChange(t *testing.T) {
	src := &fakeSource{
		rows: []srcRow{
			{1, 1, 1, 1, 1},
			{2, 2, 1, 2, 1},
			{3, 3, 1, 3, 1},
		},
		errAt: map[int]bool{2: true},
	}
	p := mustPuller(t, src, 0, 0, 2, 1)
	before := p.Snapshot()

	if _, err := p.Pull(10); !errors.Is(err, pull.ErrSource) {
		t.Fatalf("Pull(10) err = %v, want ErrSource", err)
	}
	after := p.Snapshot()
	if after.Cur != before.Cur || after.MaxNow != before.MaxNow ||
		len(after.Applied) != 0 || len(after.Dead) != 0 {
		t.Fatalf("ErrSource 后状态被改动: %+v", after)
	}

	delete(src.errAt, 2)
	if st, want := mustPull(t, p, 10), (pull.Stats{Applied: 3, Queries: 2}); st != want {
		t.Fatalf("Pull(10) = %+v, want %+v", st, want)
	}
}

// 构造参数校验。
func TestNewInvalidParams(t *testing.T) {
	src := &fakeSource{}
	cases := [][4]int64{
		{-1, 0, 1, 1},            // D 越界
		{1_000_000_001, 0, 1, 1}, // D 越界
		{0, -1, 1, 1},            // B 越界
		{0, 1_000_000_001, 1, 1}, // B 越界
		{0, 0, 0, 1},             // limit 越界
		{0, 0, 1001, 1},          // limit 越界
		{0, 0, 1, 0},             // S 越界
		{0, 0, 1, 101},           // S 越界
	}
	for _, c := range cases {
		if _, err := pull.New(src, c[0], c[1], c[2], c[3]); !errors.Is(err, pull.ErrInvalid) {
			t.Fatalf("New%v err = %v, want ErrInvalid", c, err)
		}
	}
	if _, err := pull.New(nil, 0, 0, 1, 1); !errors.Is(err, pull.ErrInvalid) {
		t.Fatalf("New(nil) err = %v, want ErrInvalid", err)
	}
}

// 拒绝次序与零改动：now 越界、时钟回退、SetSchema 权限/降级/越界。
func TestRejectionsNoStateChange(t *testing.T) {
	src := &fakeSource{rows: []srcRow{{1, 10, 1, 10, 1}}}
	p := mustPuller(t, src, 0, 0, 10, 1)
	mustPull(t, p, 100)
	before := p.Snapshot()

	checkUnchanged := func(name string) {
		t.Helper()
		after := p.Snapshot()
		if after.Cur != before.Cur || after.MaxNow != before.MaxNow || after.S != before.S ||
			len(after.Applied) != len(before.Applied) || len(after.Dead) != len(before.Dead) {
			t.Fatalf("%s 后状态被改动: %+v", name, after)
		}
	}

	if _, err := p.Pull(-1); !errors.Is(err, pull.ErrInvalid) {
		t.Fatalf("Pull(-1) err = %v, want ErrInvalid", err)
	}
	checkUnchanged("Pull(-1)")
	if _, err := p.Pull(1_000_000_000_001); !errors.Is(err, pull.ErrInvalid) {
		t.Fatalf("Pull(1e12+1) err = %v, want ErrInvalid", err)
	}
	checkUnchanged("Pull(1e12+1)")
	if _, err := p.Pull(99); !errors.Is(err, pull.ErrClock) {
		t.Fatalf("Pull(99) err = %v, want ErrClock", err)
	}
	checkUnchanged("Pull(99)")
	if err := p.SetSchema(1, 2); !errors.Is(err, pull.ErrPerm) {
		t.Fatalf("SetSchema(1,2) err = %v, want ErrPerm", err)
	}
	checkUnchanged("SetSchema(1,2)")
	if err := p.SetSchema(2, 0); !errors.Is(err, pull.ErrInvalid) {
		t.Fatalf("SetSchema(2,0) err = %v, want ErrInvalid", err)
	}
	checkUnchanged("SetSchema(2,0)")
	if err := p.SetSchema(2, 101); !errors.Is(err, pull.ErrInvalid) {
		t.Fatalf("SetSchema(2,101) err = %v, want ErrInvalid", err)
	}
	checkUnchanged("SetSchema(2,101)")
	if err := p.SetSchema(2, 2); err != nil {
		t.Fatalf("SetSchema(2,2): %v", err)
	}
	if err := p.SetSchema(2, 1); !errors.Is(err, pull.ErrDowngrade) {
		t.Fatalf("SetSchema(2,1) err = %v, want ErrDowngrade", err)
	}
	if got := p.Snapshot().S; got != 2 {
		t.Fatalf("S = %d, want 2", got)
	}
}

// 源端返回非法数据一律按参数非法拒绝且零改动。
func TestInvalidSourceRowsRejected(t *testing.T) {
	good := sink.Row{ID: 1, TS: 5, Ver: 1, SV: 1}
	cases := map[string][][]sink.Row{
		"id 越界":      {{{ID: 0, TS: 5, Ver: 1, SV: 1}}},
		"id 超上界":     {{{ID: 1_000_000_001, TS: 5, Ver: 1, SV: 1}}},
		"ts 越界":      {{{ID: 1, TS: -1, Ver: 1, SV: 1}}},
		"ver 越界":     {{{ID: 1, TS: 5, Ver: 0, SV: 1}}},
		"sv 越界":      {{{ID: 1, TS: 5, Ver: 1, SV: 0}}},
		"ts 超 maxTs": {{{ID: 1, TS: 1000, Ver: 1, SV: 1}}},
		"页超 limit":   {{good, {ID: 2, TS: 6, Ver: 1, SV: 1}}},
		"非严格升序":      {{{ID: 2, TS: 5, Ver: 1, SV: 1}, {ID: 1, TS: 5, Ver: 1, SV: 1}}},
		"同位置重复":      {{good, good}},
	}
	for name, pages := range cases {
		limit := int64(10)
		if name == "页超 limit" {
			limit = 1
		}
		p := mustPuller(t, &staticSource{pages: pages}, 0, 0, limit, 1)
		if _, err := p.Pull(10); !errors.Is(err, pull.ErrInvalid) {
			t.Fatalf("%s: err = %v, want ErrInvalid", name, err)
		}
		snap := p.Snapshot()
		if snap.Cur != (cursor.Cursor{}) || snap.MaxNow != 0 || len(snap.Applied) != 0 || len(snap.Dead) != 0 {
			t.Fatalf("%s: 状态被改动 %+v", name, snap)
		}
	}
}

// 首行不大于 after 拒绝：先推进游标，再让源端返回不合法页。
func TestFirstRowNotAfterRejected(t *testing.T) {
	src := &staticSource{pages: [][]sink.Row{
		{{ID: 1, TS: 5, Ver: 1, SV: 1}, {ID: 2, TS: 6, Ver: 1, SV: 1}},
		{{ID: 1, TS: 5, Ver: 9, SV: 1}},
	}}
	p := mustPuller(t, src, 0, 0, 10, 1)
	mustPull(t, p, 10) // cur=(6,2)
	before := p.Snapshot()
	if _, err := p.Pull(20); !errors.Is(err, pull.ErrInvalid) {
		t.Fatalf("Pull(20) err = %v, want ErrInvalid", err)
	}
	after := p.Snapshot()
	if after.Cur != before.Cur || after.MaxNow != before.MaxNow || len(after.Applied) != len(before.Applied) {
		t.Fatalf("状态被改动: %+v", after)
	}
}

// 并发调用等价于某个串行顺序；commitAt-ts<=D 时所有 id 的最大 ver 都被应用。
func TestConcurrencyCompleteness(t *testing.T) {
	var rows []srcRow
	maxVer := map[int64]int64{}
	for id := int64(1); id <= 20; id++ {
		vers := id%3 + 1
		for v := int64(1); v <= vers; v++ {
			ts := id*10 + v
			commitAt := ts + (id+v)%6 // 0..5 <= D
			sv := 1 + (id+v)%2
			rows = append(rows, srcRow{id, ts, v, commitAt, sv})
			if sv <= 2 && v > maxVer[id] {
				maxVer[id] = v
			}
		}
	}
	p := mustPuller(t, &fakeSource{rows: rows}, 5, 3, 3, 2)

	var wg sync.WaitGroup
	var nowCtr atomic.Int64
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 50; i++ {
				now := nowCtr.Add(1)
				_, _ = p.Pull(now) // 并发下 ErrClock 属正常
				_ = p.SetSchema(2, 2)
				_ = p.Snapshot()
			}
		}()
	}
	wg.Wait()

	mustPull(t, p, 1_000_000_000_000)
	snap := p.Snapshot()
	for id, want := range maxVer {
		if got := snap.Applied[id]; got != want {
			t.Fatalf("applied[%d] = %d, want %d", id, got, want)
		}
	}
}

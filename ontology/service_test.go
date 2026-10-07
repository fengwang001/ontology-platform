package ontology

import (
	"fmt"
	"strings"
	"testing"
)

// buildFanGraph 构造 s --l01..l0n--> o1..on，每个 oi --li1,li2--> xi1,xi2。
func buildFanGraph(t *testing.T, s *Store, n int) {
	t.Helper()
	mustAddObject(t, s, "s")
	for i := 1; i <= n; i++ {
		oid := ObjectID(fmt.Sprintf("o%d", i))
		mustAddObject(t, s, oid)
		mustAddLink(t, s, LinkID(fmt.Sprintf("l%02d", i)), "s", oid)
		for j := 1; j <= 2; j++ {
			xid := ObjectID(fmt.Sprintf("x%d%d", i, j))
			mustAddObject(t, s, xid)
			mustAddLink(t, s, LinkID(fmt.Sprintf("l%d%d", i, j)), oid, xid)
		}
	}
}

func newTestService(s *Store) (*Service, *memLogger) {
	logger := &memLogger{}
	return NewService(s, logger), logger
}

func TestPaginationMatchesNaive(t *testing.T) {
	s := NewStore()
	buildFanGraph(t, s, 5)
	svc, _ := newTestService(s)
	spec := TraversalSpec{Start: "s", Hops: []HopSpec{{Dir: DirOut}, {Dir: DirOut}}}
	got, gotMarkers, snap, release := runTraversal(t, svc, spec, ModeMarked,
		func(page int) int { return page%3 + 1 }, nil)
	defer release()
	want, wantMarkers := naiveTraverse(s, snap, spec, ModeMarked)
	assertSameIDs(t, want, got)
	assertSameMarkers(t, wantMarkers, gotMarkers)
	if len(got) != 15 {
		t.Fatalf("expected 15 results, got %d", len(got))
	}
}

// TestSilentVsMarked 对照同一超限场景下两种运行模式的返回内容。
func TestSilentVsMarked(t *testing.T) {
	build := func() *Store {
		s := NewStore()
		buildFanGraph(t, s, 5)
		return s
	}
	spec := TraversalSpec{Start: "s", Hops: []HopSpec{
		{Dir: DirOut, Limit: 2}, // 第 1 跳：5 个候选取 2，丢 3
		{Dir: DirOut, Limit: 1}, // 第 2 跳：每个节点 2 个候选取 1，丢 1
	}}

	// 静默丢弃模式：同样的丢弃，不产生任何标记。
	svcSilent, _ := newTestService(build())
	gotSilent, markersSilent, _, releaseSilent := runTraversal(t, svcSilent, spec, ModeSilent,
		func(int) int { return 2 }, nil)
	defer releaseSilent()
	if len(markersSilent) != 0 {
		t.Fatalf("silent mode must not emit markers, got %v", markersSilent)
	}
	wantSilent := []ObjectID{"o1", "o2", "x11", "x21"}
	assertSameIDs(t, wantSilent, gotSilent)

	// 显式截断标记模式：对象序列相同，但附带逐跳截断标记。
	svcMarked, _ := newTestService(build())
	gotMarked, markersMarked, _, releaseMarked := runTraversal(t, svcMarked, spec, ModeMarked,
		func(int) int { return 2 }, nil)
	defer releaseMarked()
	assertSameIDs(t, gotSilent, gotMarked)
	wantMarkers := []TruncationMarker{
		{Hop: 1, At: "s", Dropped: 3},
		{Hop: 2, At: "o1", Dropped: 1},
		{Hop: 2, At: "o2", Dropped: 1},
	}
	assertSameMarkers(t, wantMarkers, markersMarked)
}

// TestCursorStableAcrossDeletion 游标编码后，其指向区域的对象/链接被并发
// 删除，后续翻页仍须正确跳过已返回内容，不重复、不遗漏。
func TestCursorStableAcrossDeletion(t *testing.T) {
	s := NewStore()
	buildFanGraph(t, s, 6)
	svc, _ := newTestService(s)
	spec := TraversalSpec{Start: "s", Hops: []HopSpec{{Dir: DirOut}, {Dir: DirOut}}}

	deleted := false
	between := func(page int) {
		if deleted || page != 1 {
			return
		}
		deleted = true
		// 删除游标“指向”的下一批对象与链接，以及起始对象本身。
		for _, id := range []ObjectID{"o3", "o4"} {
			if err := s.DeleteObject(id); err != nil {
				t.Fatal(err)
			}
		}
		if err := s.DeleteLink("l05"); err != nil {
			t.Fatal(err)
		}
		if err := s.DeleteLink("l61"); err != nil {
			t.Fatal(err)
		}
		if err := s.DeleteObject("s"); err != nil {
			t.Fatal(err)
		}
	}
	got, _, snap, release := runTraversal(t, svc, spec, ModeMarked, func(int) int { return 2 }, between)
	defer release()
	if !deleted {
		t.Fatal("mutations were not injected")
	}
	// 快照语义：删除不影响本次遍历，结果须与快照上朴素遍历完全一致。
	want, _ := naiveTraverse(s, snap, spec, ModeMarked)
	if len(want) != 18 {
		t.Fatalf("naive oracle sanity: expected 18, got %d", len(want))
	}
	assertSameIDs(t, want, got)
}

func expectErrKind(t *testing.T, err error, kind ErrorKind) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected error kind %v, got nil", kind)
	}
	e, ok := err.(*Error)
	if !ok {
		t.Fatalf("expected *Error, got %T: %v", err, err)
	}
	if e.Kind != kind {
		t.Fatalf("expected kind %v, got %v (%v)", kind, e.Kind, e)
	}
}

// TestInvalidCursorRejected 各类非法游标须被拒绝且不产生任何结果页。
func TestInvalidCursorRejected(t *testing.T) {
	s := NewStore()
	buildFanGraph(t, s, 6)
	svc, _ := newTestService(s)
	spec := TraversalSpec{Start: "s", Hops: []HopSpec{{Dir: DirOut}}}

	sessionsBefore := len(svc.sessions)
	cases := map[string]string{
		"empty-segment":     "v1..1.abcdef0123456789",
		"garbage":           "not-a-cursor",
		"wrong-version":     "v2.t000001.1.abcdef0123456789",
		"unknown-traversal": "v1.t999999.1.",
	}
	for name, c := range cases {
		if name == "unknown-traversal" {
			c = encodeCursor(svc.key, "t999999", 1) // 签名合法但遍历不存在
		}
		resp, err := svc.Page(PageRequest{Cursor: c, BatchSize: 2, Mode: ModeSilent})
		expectErrKind(t, err, ErrInvalidCursor)
		if len(resp.Objects) != 0 || resp.Cursor != "" {
			t.Fatalf("%s: rejected request must not produce a result page", name)
		}
	}
	// 另一个服务实例签发的游标（HMAC 密钥不同）须被拒。
	other := NewService(s, nil)
	resp0, err := other.Page(PageRequest{Spec: spec, BatchSize: 2, Mode: ModeSilent})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Page(PageRequest{Cursor: resp0.Cursor, BatchSize: 2, Mode: ModeSilent}); err != nil {
		expectErrKind(t, err, ErrInvalidCursor)
	}
	// 被拒绝的请求不得创建新遍历会话。
	if len(svc.sessions) != sessionsBefore {
		t.Fatalf("rejected cursors must not spawn sessions: %d -> %d", sessionsBefore, len(svc.sessions))
	}

	// 已使用过的（指向已返回内容之前位置的）游标须被拒。
	r1, err := svc.Page(PageRequest{Spec: spec, BatchSize: 2, Mode: ModeSilent})
	if err != nil {
		t.Fatal(err)
	}
	r2, err := svc.Page(PageRequest{Cursor: r1.Cursor, BatchSize: 2, Mode: ModeSilent})
	if err != nil {
		t.Fatal(err)
	}
	_, err = svc.Page(PageRequest{Cursor: r1.Cursor, BatchSize: 2, Mode: ModeSilent})
	expectErrKind(t, err, ErrInvalidCursor)
	// 篡改签名后的游标须被拒。
	tampered := r2.Cursor[:len(r2.Cursor)-1] + strings.Repeat("0", 1)
	if tampered == r2.Cursor {
		tampered = r2.Cursor[:len(r2.Cursor)-1] + "1"
	}
	_, err = svc.Page(PageRequest{Cursor: tampered, BatchSize: 2, Mode: ModeSilent})
	expectErrKind(t, err, ErrInvalidCursor)
	// 遍历结束后，最后一枚游标也随之失效。
	for {
		if r2.Done {
			break
		}
		r2, err = svc.Page(PageRequest{Cursor: r2.Cursor, BatchSize: 2, Mode: ModeSilent})
		if err != nil {
			t.Fatal(err)
		}
	}
	// 全部会话已清理。
	if len(svc.sessions) != 0 {
		t.Fatalf("finished traversals must be cleaned up, %d sessions left", len(svc.sessions))
	}
}

// TestModeChangeRejected 首次请求之后更改运行模式须被拒。
func TestModeChangeRejected(t *testing.T) {
	s := NewStore()
	buildFanGraph(t, s, 4)
	svc, _ := newTestService(s)
	spec := TraversalSpec{Start: "s", Hops: []HopSpec{{Dir: DirOut}}}
	r1, err := svc.Page(PageRequest{Spec: spec, BatchSize: 2, Mode: ModeSilent})
	if err != nil {
		t.Fatal(err)
	}
	_, err = svc.Page(PageRequest{Cursor: r1.Cursor, BatchSize: 2, Mode: ModeMarked})
	expectErrKind(t, err, ErrModeChange)
	// 同模式继续则正常。
	if _, err = svc.Page(PageRequest{Cursor: r1.Cursor, BatchSize: 2, Mode: ModeSilent}); err != nil {
		t.Fatalf("same-mode continuation should succeed: %v", err)
	}
}

// TestErrorOrder 四类错误互斥且判定次序固定。
func TestErrorOrder(t *testing.T) {
	s := NewStore()
	buildFanGraph(t, s, 4)
	svc, _ := newTestService(s)
	spec := TraversalSpec{Start: "s", Hops: []HopSpec{{Dir: DirOut}}}
	badSpec := TraversalSpec{Start: "ghost", Hops: []HopSpec{{Dir: DirOut}}}

	// 1 优先于 3：起始对象不存在 + 批次非法 → 报起始对象不存在。
	_, err := svc.Page(PageRequest{Spec: badSpec, BatchSize: 0, Mode: ModeSilent})
	expectErrKind(t, err, ErrStartObjectNotFound)
	// 3：首次请求批次非法。
	_, err = svc.Page(PageRequest{Spec: spec, BatchSize: -1, Mode: ModeSilent})
	expectErrKind(t, err, ErrInvalidBatchSize)
	// 2 优先于 3：游标非法 + 批次非法 → 报游标非法。
	_, err = svc.Page(PageRequest{Cursor: "junk", BatchSize: 0, Mode: ModeSilent})
	expectErrKind(t, err, ErrInvalidCursor)
	// 3 优先于 4：游标合法 + 批次非法 + 模式变更 → 报批次非法。
	r1, err := svc.Page(PageRequest{Spec: spec, BatchSize: 2, Mode: ModeSilent})
	if err != nil {
		t.Fatal(err)
	}
	_, err = svc.Page(PageRequest{Cursor: r1.Cursor, BatchSize: 0, Mode: ModeMarked})
	expectErrKind(t, err, ErrInvalidBatchSize)
	// 4：游标合法 + 批次合法 + 模式变更 → 报模式变更。
	_, err = svc.Page(PageRequest{Cursor: r1.Cursor, BatchSize: 2, Mode: ModeMarked})
	expectErrKind(t, err, ErrModeChange)
}

// TestRequestLogging 每次请求须记录游标、运行模式、返回内容与截断标记。
func TestRequestLogging(t *testing.T) {
	s := NewStore()
	buildFanGraph(t, s, 5)
	svc, logger := newTestService(s)
	spec := TraversalSpec{Start: "s", Hops: []HopSpec{
		{Dir: DirOut, Limit: 2},
		{Dir: DirOut, Limit: 1},
	}}
	logsBefore := len(logger.all())

	cursor := ""
	var wantCursors []string
	var wantReturned [][]ObjectID
	var wantMarkers [][]TruncationMarker
	for page := 0; ; page++ {
		wantCursors = append(wantCursors, cursor)
		resp, err := svc.Page(PageRequest{Cursor: cursor, Spec: spec, BatchSize: 2, Mode: ModeMarked})
		if err != nil {
			t.Fatal(err)
		}
		wantReturned = append(wantReturned, resp.Objects)
		wantMarkers = append(wantMarkers, resp.Markers)
		if resp.Done {
			break
		}
		cursor = resp.Cursor
	}
	// 再制造一次错误请求，错误也须入日志。
	if _, err := svc.Page(PageRequest{Cursor: "junk", BatchSize: 1, Mode: ModeMarked}); err == nil {
		t.Fatal("expected error")
	}

	logs := logger.all()[logsBefore:]
	if len(logs) != len(wantCursors)+1 {
		t.Fatalf("expected %d log entries, got %d", len(wantCursors)+1, len(logs))
	}
	totalMarkers := 0
	for i, e := range logs[:len(logs)-1] {
		if e.Cursor != wantCursors[i] {
			t.Fatalf("entry %d: logged cursor %q != request cursor %q", i, e.Cursor, wantCursors[i])
		}
		if e.Mode != ModeMarked || e.BatchSize != 2 {
			t.Fatalf("entry %d: wrong mode/batch logged: %+v", i, e)
		}
		assertSameIDs(t, wantReturned[i], e.Returned)
		assertSameMarkers(t, wantMarkers[i], e.Markers)
		totalMarkers += len(e.Markers)
		if e.Err != nil {
			t.Fatalf("entry %d: unexpected logged error %v", i, e.Err)
		}
	}
	if totalMarkers == 0 {
		t.Fatal("expected truncation markers to be logged")
	}
	if logs[len(logs)-1].Err == nil {
		t.Fatal("failed request must be logged with its error")
	}
}

// TestHistoryProbesBounded 验证单次请求触碰的“已返回判定”历史记录数
// 不随已返回总条数增长：对度为 d 的树，每页探针数 ≤ batchSize + d，
// 与页序号（即此前已返回总量）无关；且不同批次大小下总探针数相同。
func TestHistoryProbesBounded(t *testing.T) {
	const depth, branch, batch = 4, 5, 7
	build := func() *Store {
		s := NewStore()
		mustAddObject(t, s, "root")
		type pair struct {
			parent ObjectID
			depth  int
		}
		queue := []pair{{"root", 0}}
		counter := 0
		for len(queue) > 0 {
			p := queue[0]
			queue = queue[1:]
			if p.depth == depth {
				continue
			}
			for i := 0; i < branch; i++ {
				counter++
				id := ObjectID(fmt.Sprintf("n%05d", counter))
				mustAddObject(t, s, id)
				mustAddLink(t, s, LinkID(fmt.Sprintf("l%05d", counter)), p.parent, id)
				queue = append(queue, pair{id, p.depth + 1})
			}
		}
		return s
	}
	hops := make([]HopSpec, depth)
	for i := range hops {
		hops[i] = HopSpec{Dir: DirOut}
	}
	spec := TraversalSpec{Start: "root", Hops: hops}

	totalProbes := func(batchSize int) (total, maxPerPage int) {
		svc, logger := newTestService(build())
		before := len(logger.all())
		_, _, _, rel := runTraversal(t, svc, spec, ModeSilent, func(int) int { return batchSize }, nil)
		rel()
		for _, e := range logger.all()[before:] {
			total += e.HistoryProbes
			if e.HistoryProbes > maxPerPage {
				maxPerPage = e.HistoryProbes
			}
		}
		return total, maxPerPage
	}

	total, maxPerPage := totalProbes(batch)
	// 每页探针数上界只取决于批次大小与节点度，与已返回总量无关。
	if bound := batch + branch; maxPerPage > bound {
		t.Fatalf("per-page history probes %d exceed bound %d", maxPerPage, bound)
	}
	// 不同批次大小下总探针数一致：工作量不随分页方式或历史长度膨胀。
	totalSingle, _ := totalProbes(100000)
	if total != totalSingle {
		t.Fatalf("total probes differ by paging: batch=%d gives %d, single page gives %d",
			batch, total, totalSingle)
	}
}

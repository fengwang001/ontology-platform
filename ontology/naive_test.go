package ontology

import (
	"sync"
	"testing"
)

// memLogger 是测试用的内存日志器，记录每次分页请求。
type memLogger struct {
	mu      sync.Mutex
	entries []RequestLog
}

func (l *memLogger) LogRequest(e RequestLog) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.entries = append(l.entries, e)
}

func (l *memLogger) all() []RequestLog {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]RequestLog{}, l.entries...)
}

// naiveTraverse 是独立的朴素参照实现：在确定快照上一次性执行完整遍历，
// 不走迭代器、不分页，直接物化全部结果与截断标记。
// 测试用它手工切页，与分页服务的输出做对照。
func naiveTraverse(store *Store, snap uint64, spec TraversalSpec, mode Mode) ([]ObjectID, []TruncationMarker) {
	visited := map[ObjectID]struct{}{spec.Start: {}}
	type node struct {
		obj   ObjectID
		depth int
	}
	frontier := []node{{obj: spec.Start, depth: 0}}
	var objects []ObjectID
	var markers []TruncationMarker
	for head := 0; head < len(frontier); head++ {
		n := frontier[head]
		if n.depth >= len(spec.Hops) {
			continue
		}
		hop := spec.Hops[n.depth]
		all := store.neighborsAt(snap, n.obj, hop.LinkType, hop.Dir)
		take := len(all)
		if hop.Limit > 0 && hop.Limit < take {
			take = hop.Limit
		}
		if dropped := len(all) - take; dropped > 0 && mode == ModeMarked {
			markers = append(markers, TruncationMarker{Hop: n.depth + 1, At: n.obj, Dropped: dropped})
		}
		for _, c := range all[:take] {
			if _, seen := visited[c.neighbor]; seen {
				continue
			}
			visited[c.neighbor] = struct{}{}
			objects = append(objects, c.neighbor)
			frontier = append(frontier, node{obj: c.neighbor, depth: n.depth + 1})
		}
	}
	return objects, markers
}

// runTraversal 通过分页服务跑完一次完整遍历，batchOf 按页序号给出每页
// 批次大小；between 在每次翻页前回调（用于注入并发修改）。
// 返回拼接后的全部结果、全部截断标记、首页日志中的快照版本，以及快照
// 钉的释放函数：首页之后会自动钉住遍历快照，调用方在完成朴素参照
// 对照之后须调用释放函数。
func runTraversal(t *testing.T, svc *Service, spec TraversalSpec, mode Mode,
	batchOf func(page int) int, between func(page int)) ([]ObjectID, []TruncationMarker, uint64, func()) {
	t.Helper()
	logger, _ := svc.logger.(*memLogger)
	logsBefore := 0
	if logger != nil {
		logsBefore = len(logger.all())
	}
	var release func()
	var all []ObjectID
	var allMarkers []TruncationMarker
	cursor := ""
	for page := 0; ; page++ {
		if page > 0 && between != nil {
			between(page)
		}
		batch := 3
		if batchOf != nil {
			batch = batchOf(page)
		}
		resp, err := svc.Page(PageRequest{Cursor: cursor, Spec: spec, BatchSize: batch, Mode: mode})
		if err != nil {
			t.Fatalf("page %d: unexpected error: %v", page, err)
		}
		if page == 0 && logger != nil {
			logs := logger.all()
			snap := logs[len(logs)-1].Snapshot
			release = svc.store.Pin(snap)
		}
		if len(resp.Objects) > batch {
			t.Fatalf("page %d: returned %d objects exceeds batch %d", page, len(resp.Objects), batch)
		}
		all = append(all, resp.Objects...)
		allMarkers = append(allMarkers, resp.Markers...)
		if resp.Done {
			if resp.Cursor != "" {
				t.Fatalf("page %d: done but cursor non-empty", page)
			}
			break
		}
		if resp.Cursor == "" {
			t.Fatalf("page %d: not done but cursor empty", page)
		}
		cursor = resp.Cursor
		if page > 100000 {
			t.Fatal("traversal did not terminate")
		}
	}
	var snap uint64
	if logger != nil {
		logs := logger.all()
		if len(logs) > logsBefore {
			snap = logs[logsBefore].Snapshot
		}
	}
	if release == nil {
		release = func() {}
	}
	return all, allMarkers, snap, release
}

// assertSameIDs 断言两个 ID 序列完全一致（次序敏感）。
func assertSameIDs(t *testing.T, want, got []ObjectID) {
	t.Helper()
	if len(want) != len(got) {
		t.Fatalf("length mismatch: want %d (%v), got %d (%v)", len(want), want, len(got), got)
	}
	seen := make(map[ObjectID]int)
	for i := range want {
		if want[i] != got[i] {
			t.Fatalf("position %d: want %q, got %q\nwant=%v\ngot =%v", i, want[i], got[i], want, got)
		}
		seen[got[i]]++
		if seen[got[i]] > 1 {
			t.Fatalf("duplicate id %q in results", got[i])
		}
	}
}

// assertSameMarkers 断言两个截断标记序列完全一致。
func assertSameMarkers(t *testing.T, want, got []TruncationMarker) {
	t.Helper()
	if len(want) != len(got) {
		t.Fatalf("marker count mismatch: want %v, got %v", want, got)
	}
	for i := range want {
		if want[i] != got[i] {
			t.Fatalf("marker %d: want %+v, got %+v", i, want[i], got[i])
		}
	}
}

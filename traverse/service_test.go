package traverse

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"testing"

	"ontology/graph"
)

// 快照隔离：遍历开始后对图结构的并发修改不得影响本次遍历结果，
// 结果必须等价于在遍历开始时的快照上执行的结果。
func TestSnapshotIsolationUnderConcurrentModification(t *testing.T) {
	links := [][3]string{
		{"l1", "a", "b"}, {"l2", "b", "c"},
		{"l3", "a", "d"}, {"l4", "d", "e"},
		{"l5", "a", "f"},
	}
	s := buildStore(t, []string{"a", "b", "c", "d", "e", "f"}, links)
	req := outReq("a", 2, 2)

	// 先在原图上算出期望结果。
	want, err := NewService(s, nil).Traverse(req)
	if err != nil {
		t.Fatal(err)
	}
	wantVersion := s.Version()

	// 再构造一个服务：快照获取后、遍历开始前并发修改图结构。
	svc := NewService(s, nil)
	svc.OnSnapshot = func() {
		_ = s.RemoveLink("l2") // 删除一条已在快照里的链接
		_ = s.AddLink(graph.Link{ID: "l9", Type: "t", SourceID: "a", TargetID: "f"})
		_ = s.RemoveObject("e")
	}
	got, err := svc.Traverse(req)
	if err != nil {
		t.Fatal(err)
	}
	if got.Stats.SnapshotVersion != wantVersion {
		t.Fatalf("snapshot version = %d, want %d", got.Stats.SnapshotVersion, wantVersion)
	}
	if fmt.Sprintf("%+v", want.Returned) != fmt.Sprintf("%+v", got.Returned) ||
		fmt.Sprintf("%+v", want.Truncated) != fmt.Sprintf("%+v", got.Truncated) {
		t.Fatalf("遍历结果受并发修改影响:\nwant=%+v\ngot=%+v", want, got)
	}
}

// 并发压力：遍历与图修改并发执行，配合 -race 验证无数据竞争、无 panic。
func TestConcurrentTraverseAndMutate(t *testing.T) {
	s := buildStore(t, []string{"a", "b", "c"}, [][3]string{
		{"l1", "a", "b"}, {"l2", "b", "c"},
	})
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				if _, err := NewService(s, nil).Traverse(outReq("a", 3, 4)); err != nil {
					t.Error(err)
					return
				}
			}
		}(i)
	}
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				id := fmt.Sprintf("x%d-%d", i, j)
				if err := s.AddObject(graph.Object{ID: id}); err != nil {
					return
				}
				_ = s.AddLink(graph.Link{ID: "lk-" + id, Type: "t", SourceID: "a", TargetID: id})
				_ = s.RemoveLink("lk-" + id)
				_ = s.RemoveObject(id)
			}
		}(i)
	}
	wg.Wait()
}

// 簿记开销证明：维护「已收集路径数是否达到条数上限」的操作次数
// 只随遍历规模增长，与上限数值本身的大小无关。
// 固定图（K 条终端路径），N 取 K, 10K, 100K, 1000K，BookkeepingOps 必须完全一致。
func TestBookkeepingOpsIndependentOfLimitMagnitude(t *testing.T) {
	const K = 500
	objects := []string{"a"}
	links := make([][3]string, 0, K)
	for i := 0; i < K; i++ {
		id := fmt.Sprintf("x%04d", i)
		objects = append(objects, id)
		links = append(links, [3]string{fmt.Sprintf("l%04d", i), "a", id})
	}
	s := buildStore(t, objects, links)
	svc := NewService(s, nil)

	var baseOps int64 = -1
	for _, n := range []int64{K, 10 * K, 100 * K, 1000 * K} {
		res, err := svc.Traverse(Request{
			StartID:     "a",
			Directions:  []graph.Direction{graph.Outgoing},
			DepthLimit:  5,
			ResultLimit: int(n),
		})
		if err != nil {
			t.Fatal(err)
		}
		if res.Stats.ReturnedCount != K {
			t.Fatalf("N=%d: returned = %d, want %d", n, res.Stats.ReturnedCount, K)
		}
		if baseOps == -1 {
			baseOps = res.Stats.BookkeepingOps
		} else if res.Stats.BookkeepingOps != baseOps {
			t.Fatalf("N=%d: BookkeepingOps = %d, 与 N=%d 时的 %d 不一致；簿记开销随上限数值增长",
				n, res.Stats.BookkeepingOps, K, baseOps)
		}
	}
	// 精确断言：ops = 每个分支入口一次比较(1+K) + 每条路径计入两次操作(K*2)，
	// 即 O(遍历规模)，而非 O(N)。
	wantOps := int64(1+K) + int64(2*K)
	if baseOps != wantOps {
		t.Fatalf("BookkeepingOps = %d, want %d (与遍历规模成线性，与 N 无关)", baseOps, wantOps)
	}
}

// 日志：每次遍历记录输入上限、各条路径及其截断标记归类依据。
func TestTraversalLogging(t *testing.T) {
	s := buildStore(t, []string{"a", "b", "c", "d"}, [][3]string{
		{"l1", "a", "b"}, {"l2", "b", "c"}, {"l3", "a", "d"},
	})
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	svc := NewService(s, logger)

	res, err := svc.Traverse(outReq("a", 2, 1))
	if err != nil {
		t.Fatal(err)
	}

	type record map[string]any
	var starts, branches, ends []record
	for _, line := range bytes.Split(bytes.TrimSpace(buf.Bytes()), []byte("\n")) {
		var rec record
		if err := json.Unmarshal(line, &rec); err != nil {
			t.Fatalf("bad log line %q: %v", line, err)
		}
		switch rec["msg"] {
		case "traverse start":
			starts = append(starts, rec)
		case "traverse branch":
			branches = append(branches, rec)
		case "traverse end":
			ends = append(ends, rec)
		}
	}
	if len(starts) != 1 || len(ends) != 1 {
		t.Fatalf("start/end 日志条数 = %d/%d, want 1/1", len(starts), len(ends))
	}
	if starts[0]["depthLimit"] != float64(2) || starts[0]["resultLimit"] != float64(1) {
		t.Fatalf("start 日志未记录输入上限: %v", starts[0])
	}
	wantBranches := len(res.Returned) + len(res.Truncated)
	if len(branches) != wantBranches {
		t.Fatalf("branch 日志条数 = %d, want %d", len(branches), wantBranches)
	}
	for _, rec := range branches {
		if rec["marker"] == nil || rec["reason"] == nil || rec["path"] == nil {
			t.Fatalf("branch 日志缺少 marker/reason/path: %v", rec)
		}
		if rec["reason"] == "" {
			t.Fatalf("branch 日志缺少归类依据: %v", rec)
		}
	}
}

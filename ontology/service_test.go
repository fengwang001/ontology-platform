package ontology

import (
	"fmt"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
)

func newReadyService(t *testing.T, logPath string) *Service {
	t.Helper()
	svc := New(logPath)
	if err := svc.Replay(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { svc.Close() })
	return svc
}

func mustApply(t *testing.T, svc *Service, op Operation, caller UserID) uint64 {
	t.Helper()
	seq, err := svc.Apply(op, caller)
	if err != nil {
		t.Fatalf("apply %+v: %v", op, err)
	}
	return seq
}

func grant(svc *Service, t *testing.T, user UserID, action Action, kind ResourceKind, id string) {
	t.Helper()
	mustApply(t, svc, Operation{
		Kind: OpGrantPermission,
		Permission: &Permission{
			User: user, Action: action,
			Resource: Resource{Kind: kind, ID: id},
		},
	}, AdminUser)
}

// 搭建菱形图：A->B->D 与 A->C->D 代价相同，平局应选字典序更小的 A,B,D。
// 返回 caller 用户 ro（只读）。
func buildDiamond(t *testing.T, svc *Service) {
	t.Helper()
	mustApply(t, svc, Operation{Kind: OpDeclareObjectType, ObjectType: "N"}, AdminUser)
	mustApply(t, svc, Operation{Kind: OpDeclareLinkType, LinkSpec: &LinkType{
		ID: "e", FromType: "N", ToType: "N", Cost: 1, Directed: true,
	}}, AdminUser)
	mustApply(t, svc, Operation{Kind: OpDeclareLinkType, LinkSpec: &LinkType{
		ID: "f", FromType: "N", ToType: "N", Cost: 1, Directed: true,
	}}, AdminUser)
	for _, id := range []ObjectID{"A", "B", "C", "D"} {
		mustApply(t, svc, Operation{Kind: OpCreateObject, ObjectType: "N", Object: id}, AdminUser)
	}
	// 交替使用两种链接类型，保证结果不依赖单一类型。
	mustApply(t, svc, Operation{Kind: OpCreateLink, LinkType: "e", From: "A", To: "B"}, AdminUser)
	mustApply(t, svc, Operation{Kind: OpCreateLink, LinkType: "f", From: "A", To: "C"}, AdminUser)
	mustApply(t, svc, Operation{Kind: OpCreateLink, LinkType: "e", From: "B", To: "D"}, AdminUser)
	mustApply(t, svc, Operation{Kind: OpCreateLink, LinkType: "f", From: "C", To: "D"}, AdminUser)
	grant(svc, t, "ro", ActionRead, ResourceObjectType, "N")
	grant(svc, t, "ro", ActionRead, ResourceLinkType, "e")
	grant(svc, t, "ro", ActionRead, ResourceLinkType, "f")
}

// 平局消解在重放前后必须选中同一条路径。
func TestTieBreakConsistentAcrossReplay(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "svc.log")
	svc := newReadyService(t, logPath)
	buildDiamond(t, svc)

	before, _, err := svc.ShortestPath("A", "D", "ro")
	if err != nil {
		t.Fatal(err)
	}
	want := Path{Objects: []ObjectID{"A", "B", "D"}, Cost: 2, Found: true}
	if !reflect.DeepEqual(before, want) {
		t.Fatalf("before replay: got %+v, want %+v", before, want)
	}
	svc.Close()

	// 多次重放，每次结果都必须一致。
	for i := 0; i < 3; i++ {
		svc2 := newReadyService(t, logPath)
		after, _, err := svc2.ShortestPath("A", "D", "ro")
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(after, before) {
			t.Fatalf("replay %d: got %+v, want %+v", i, after, before)
		}
		svc2.Close()
	}
}

// 被拒绝的操作不进入日志；拒绝判定次序为 参数非法 > 权限不足 > 基数上限。
func TestRejectedOpsNotLoggedAndOrder(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "svc.log")
	svc := newReadyService(t, logPath)
	mustApply(t, svc, Operation{Kind: OpDeclareObjectType, ObjectType: "N"}, AdminUser)
	mustApply(t, svc, Operation{Kind: OpDeclareLinkType, LinkSpec: &LinkType{
		ID: "e", FromType: "N", ToType: "N", Cost: 1, Directed: true, MaxOutgoing: 1,
	}}, AdminUser)
	mustApply(t, svc, Operation{Kind: OpCreateObject, ObjectType: "N", Object: "A"}, AdminUser)
	mustApply(t, svc, Operation{Kind: OpCreateObject, ObjectType: "N", Object: "B"}, AdminUser)
	mustApply(t, svc, Operation{Kind: OpCreateObject, ObjectType: "N", Object: "C"}, AdminUser)
	mustApply(t, svc, Operation{Kind: OpCreateLink, LinkType: "e", From: "A", To: "B"}, AdminUser)
	entriesBefore, _, err := readLog(logPath)
	if err != nil {
		t.Fatal(err)
	}

	rejectKind := func(op Operation, caller UserID) RejectKind {
		t.Helper()
		_, err := svc.Apply(op, caller)
		if err == nil {
			t.Fatalf("op %+v unexpectedly accepted", op)
		}
		rej, ok := err.(*RejectError)
		if !ok {
			t.Fatalf("op %+v: want *RejectError, got %T (%v)", op, err, err)
		}
		return rej.Kind
	}

	// 1) 同时参数非法且无权限：必须报参数非法（次序最高）。
	if k := rejectKind(Operation{Kind: OpCreateObject, ObjectType: "UNKNOWN", Object: "X"}, "nobody"); k != RejectInvalidParams {
		t.Fatalf("want invalid_params, got %s", k)
	}
	// 2) 参数合法、无权限、且会超基数：必须报权限不足（先于基数判定）。
	if k := rejectKind(Operation{Kind: OpCreateLink, LinkType: "e", From: "A", To: "C"}, "nobody"); k != RejectPermission {
		t.Fatalf("want permission_denied, got %s", k)
	}
	// 3) 有权限但会突破 MaxOutgoing=1：报基数上限。
	grant(svc, t, "w", ActionWrite, ResourceLinkType, "e")
	if k := rejectKind(Operation{Kind: OpCreateLink, LinkType: "e", From: "A", To: "C"}, "w"); k != RejectCardinality {
		t.Fatalf("want cardinality_exceeded, got %s", k)
	}
	// 4) 撤销不存在的授权：参数非法。
	if k := rejectKind(Operation{
		Kind: OpRevokePermission,
		Permission: &Permission{
			User: "u", Action: ActionRead,
			Resource: Resource{Kind: ResourceObjectType, ID: "N"},
		},
	}, AdminUser); k != RejectInvalidParams {
		t.Fatalf("want invalid_params, got %s", k)
	}

	// 日志中不允许出现任何被拒绝的操作。
	entriesAfter, validBytes, err := readLog(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(entriesAfter) != len(entriesBefore)+1 { // 只有那次成功的 grant
		t.Fatalf("log grew by %d entries, want 1", len(entriesAfter)-len(entriesBefore))
	}
	last := entriesAfter[len(entriesAfter)-1]
	if last.Op.Kind != OpGrantPermission {
		t.Fatalf("last entry is %s, want grant_permission", last.Op.Kind)
	}
	// 序号连续无空洞。
	for i, e := range entriesAfter {
		if e.Seq != uint64(i+1) {
			t.Fatalf("entry %d has seq %d", i, e.Seq)
		}
	}
	// 下一条被接受的操作必须紧接着上一个序号。
	seq := mustApply(t, svc, Operation{Kind: OpCreateObject, ObjectType: "N", Object: "Z"}, AdminUser)
	if seq != uint64(len(entriesAfter)+1) {
		t.Fatalf("next seq = %d, want %d", seq, len(entriesAfter)+1)
	}
	_ = validBytes
}

// 重放完成前，一切请求都必须以独立的「服务未就绪」原因被拒绝。
func TestNotReadyBeforeReplay(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "svc.log")
	svc := New(logPath)
	if _, err := svc.Apply(Operation{Kind: OpDeclareObjectType, ObjectType: "N"}, AdminUser); !IsNotReady(err) {
		t.Fatalf("Apply before replay: got %v, want not-ready", err)
	}
	if _, _, err := svc.ShortestPath("A", "B", "ro"); !IsNotReady(err) {
		t.Fatalf("ShortestPath before replay: got %v, want not-ready", err)
	}
	// 「服务未就绪」不属于四级拒绝分类。
	if _, isReject := error(ErrNotReady).(*RejectError); isReject {
		t.Fatal("ErrNotReady must not be a *RejectError")
	}
	if err := svc.Replay(); err != nil {
		t.Fatal(err)
	}
	defer svc.Close()
	if _, err := svc.Apply(Operation{Kind: OpDeclareObjectType, ObjectType: "N"}, AdminUser); err != nil {
		t.Fatalf("Apply after replay: %v", err)
	}
}

// 对象失效后其链接不可遍历。
func TestInvalidateRemovesLinks(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "svc.log")
	svc := newReadyService(t, logPath)
	buildDiamond(t, svc)
	mustApply(t, svc, Operation{Kind: OpInvalidateObject, Object: "B"}, AdminUser)
	p, _, err := svc.ShortestPath("A", "D", "ro")
	if err != nil {
		t.Fatal(err)
	}
	want := Path{Objects: []ObjectID{"A", "C", "D"}, Cost: 2, Found: true}
	if !reflect.DeepEqual(p, want) {
		t.Fatalf("got %+v, want %+v", p, want)
	}
}

// 度量只统计本次查询实际访问的对象与链接数，
// 与日志总长度和图总对象数无关。
func TestMetricsIndependentOfLogAndGraphSize(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "svc.log")
	svc := newReadyService(t, logPath)
	buildDiamond(t, svc)

	_, m1, err := svc.ShortestPath("A", "D", "ro")
	if err != nil {
		t.Fatal(err)
	}

	// 追加大量与查询邻域无关的日志项（远处独立子图 + 授权 churn）。
	mustApply(t, svc, Operation{Kind: OpDeclareObjectType, ObjectType: "FAR"}, AdminUser)
	mustApply(t, svc, Operation{Kind: OpDeclareLinkType, LinkSpec: &LinkType{
		ID: "far_e", FromType: "FAR", ToType: "FAR", Cost: 1, Directed: true,
	}}, AdminUser)
	for i := 0; i < 300; i++ {
		id := ObjectID(fmt.Sprintf("far-%d", i))
		mustApply(t, svc, Operation{Kind: OpCreateObject, ObjectType: "FAR", Object: id}, AdminUser)
		if i > 0 {
			mustApply(t, svc, Operation{
				Kind: OpCreateLink, LinkType: "far_e",
				From: ObjectID(fmt.Sprintf("far-%d", i-1)), To: id,
			}, AdminUser)
		}
	}
	entries, _, err := readLog(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) < 600 {
		t.Fatalf("expected long log, got %d entries", len(entries))
	}

	_, m2, err := svc.ShortestPath("A", "D", "ro")
	if err != nil {
		t.Fatal(err)
	}
	if *m1 != *m2 {
		t.Fatalf("metrics changed with unrelated log/graph growth: %+v -> %+v", m1, m2)
	}

	// 重启重放后度量仍然一致：度量不随日志总长度增长。
	svc.Close()
	svc2 := newReadyService(t, logPath)
	defer svc2.Close()
	p3, m3, err := svc2.ShortestPath("A", "D", "ro")
	if err != nil {
		t.Fatal(err)
	}
	if *m3 != *m1 {
		t.Fatalf("metrics changed after replay: %+v -> %+v", m1, m3)
	}
	if !p3.Found || p3.Cost != 2 {
		t.Fatalf("unexpected path after replay: %+v", p3)
	}
}

// 并发变更与并发查询的结果须等价于按日志序号顺序的串行执行。
func TestConcurrentSerialization(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "svc.log")
	svc := newReadyService(t, logPath)
	mustApply(t, svc, Operation{Kind: OpDeclareObjectType, ObjectType: "N"}, AdminUser)
	mustApply(t, svc, Operation{Kind: OpDeclareLinkType, LinkSpec: &LinkType{
		ID: "e", FromType: "N", ToType: "N", Cost: 1, Directed: false,
	}}, AdminUser)

	const workers = 8
	const perWorker = 25
	var wg sync.WaitGroup
	seqs := make([]uint64, workers*perWorker)
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < perWorker; i++ {
				id := ObjectID(fmt.Sprintf("w%d-%d", w, i))
				seq, err := svc.Apply(Operation{Kind: OpCreateObject, ObjectType: "N", Object: id}, AdminUser)
				if err != nil {
					t.Error(err)
					return
				}
				seqs[w*perWorker+i] = seq
				// 并发查询不应报错（未找到路径是合法结果）。
				if _, _, err := svc.ShortestPath(id, id, AdminUser); err != nil {
					t.Error(err)
					return
				}
			}
		}(w)
	}
	wg.Wait()

	// 日志序号必须是 1..N 的排列：无空洞、无重复，且与串行顺序一致。
	entries, _, err := readLog(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2+workers*perWorker {
		t.Fatalf("got %d entries, want %d", len(entries), 2+workers*perWorker)
	}
	for i, e := range entries {
		if e.Seq != uint64(i+1) {
			t.Fatalf("entry %d has seq %d: gap or dup", i, e.Seq)
		}
	}
	seen := map[uint64]bool{}
	for _, s := range seqs {
		if s == 0 || seen[s] {
			t.Fatalf("dup or zero seq %d", s)
		}
		seen[s] = true
	}
	// 每条被接受的操作都按返回序号落在日志对应位置。
	for _, s := range seqs {
		if entries[s-1].Seq != s || entries[s-1].Op.Kind != OpCreateObject {
			t.Fatalf("entry at seq %d mismatch", s)
		}
	}
}
